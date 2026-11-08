// Package usecases tests drive every policy exactly as cmd/api wires it:
// the REAL use cases over the in-memory doubles (the Postgres adapters get
// their own integration-tagged suite), asserting outcomes, aggregate states
// and the outbox messages the relay would publish.
package usecases

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// The fixture instants (UTC, like every stamp the service writes).
var (
	tDock   = time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	tCutoff = time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
	tDepart = time.Date(2026, 11, 6, 16, 1, 30, 0, time.UTC)
)

// clockStub is the deterministic ports.Clock.
type clockStub struct{ t time.Time }

func (c clockStub) Now() time.Time { return c.t }

// idsStub mints deterministic load ids and BOL numbers.
type idsStub struct{ n, b int }

func (i *idsStub) NewLoadID() shared.LoadID {
	i.n++
	return shared.LoadID(fmt.Sprintf("load-t%d", i.n))
}

func (i *idsStub) NewBolNumber(date outboundload.OperatingDate) shared.BolNumber {
	i.b++
	return shared.BolNumber(fmt.Sprintf("BOL-%s-%02d", date, i.b))
}

// rig is one wired service over the in-memory doubles.
type rig struct {
	loads     *memory.LoadRepo
	pkgs      *memory.PackageRepo
	bols      *memory.BolRepo
	copies    *memory.Copies
	appts     *memory.Appointments
	processed *memory.ProcessedEventRepo
	board     *memory.BoardStore
	uow       *memory.UnitOfWork
	rec       *memory.OutboxRecorder
	sink      *events.Sink
	clock     clockStub
	ids       *idsStub

	expect      *ExpectPackage
	divert      *DivertPackage
	book        *BookAppointment
	dock        *DockTrailer
	doorWork    *RecordDoorWork
	depart      *OnDeparture
	applyCpt    *ApplyCptSchedule
	applyLayout *ApplyLayout
	project     *ProjectBoard
	queries     *Queries

	stage   *StagePackage
	load    *LoadPackage
	unload  *UnloadPackage
	close   *CloseLoad
	reopen  *ReopenLoad
	cancel  *CancelLoad
	voidBol *VoidBillOfLading
	sweep   *SweepMissedLoads
}

func newRig(t *testing.T) *rig {
	t.Helper()
	loads := memory.NewLoadRepo()
	pkgs := memory.NewPackageRepo(loads)
	copies, appts := memory.NewCopies(), memory.NewAppointments()
	processed := memory.NewProcessedEventRepo()
	rec := memory.NewOutboxRecorder()
	uow := memory.NewUnitOfWork(copies, processed, rec)
	sink := events.NewSink(rec)
	clock := clockStub{t: tDock}
	intake := Intake{UoW: uow, Processed: processed}
	r := &rig{
		loads: loads, pkgs: pkgs, bols: memory.NewBolRepo(), copies: copies,
		appts: appts, processed: processed, board: memory.NewBoardStore(),
		uow: uow, rec: rec, sink: sink, clock: clock, ids: &idsStub{},
	}
	r.expect = &ExpectPackage{Intake: intake, Loads: loads, Packages: pkgs, Copies: copies, Sink: sink, IDs: r.ids, Clock: clock}
	r.divert = &DivertPackage{Intake: intake, Packages: pkgs, Clock: clock}
	r.book = &BookAppointment{Intake: intake, Loads: loads, Copies: appts, Sink: sink, Clock: clock}
	r.dock = &DockTrailer{Intake: intake, Loads: loads, Sink: sink, Clock: clock}
	r.doorWork = &RecordDoorWork{Intake: intake, Copies: appts}
	r.depart = &OnDeparture{Intake: intake, Loads: loads, Packages: pkgs, Bols: r.bols, Copies: appts, Sink: sink, IDs: r.ids, Clock: clock}
	r.applyCpt = &ApplyCptSchedule{Intake: intake, Copies: copies}
	r.applyLayout = &ApplyLayout{Intake: intake, Copies: copies}
	r.project = &ProjectBoard{Intake: intake, Board: r.board}
	r.queries = &Queries{Loads: loads, Pkgs: pkgs, Bols: r.bols, Board: r.board, Clock: clock}
	r.stage = &StagePackage{UoW: uow, Pkgs: pkgs, Layout: copies, Sink: sink, Clock: clock}
	r.load = &LoadPackage{UoW: uow, Pkgs: pkgs, Loads: loads, Sink: sink, Clock: clock}
	r.unload = &UnloadPackage{UoW: uow, Pkgs: pkgs, Sink: sink, Clock: clock}
	r.close = &CloseLoad{UoW: uow, Loads: loads, Pkgs: pkgs, Sink: sink, Clock: clock}
	r.reopen = &ReopenLoad{UoW: uow, Loads: loads, Sink: sink, Clock: clock}
	r.cancel = &CancelLoad{UoW: uow, Loads: loads, Pkgs: pkgs, Sink: sink, Clock: clock}
	r.voidBol = &VoidBillOfLading{UoW: uow, Bols: r.bols, Sink: sink, Clock: clock}
	r.sweep = &SweepMissedLoads{UoW: uow, Pkgs: pkgs, Sink: sink, Clock: clock}
	return r
}

// published counts the recorded outbox messages of the INTEGRATION topic by
// event name (the type's last dot-segment): what the relay drains to the
// events topic. The analytics twin (one extra message per fact, the
// projector's feed) is deliberately not counted.
func (r *rig) published(name string) int {
	n := 0
	for _, m := range r.rec.Messages() {
		if suffix(m.EventType) == name && m.Topic == events.TopicEvents {
			n++
		}
	}
	return n
}

func suffix(t string) string {
	for i := len(t) - 1; i >= 0; i-- {
		if t[i] == '.' {
			return t[i+1:]
		}
	}
	return t
}

// seedCpt applies one CPTScheduleChanged: SITE-1 runs a single 16:00 cutoff
// shipped STDOUT-2DAY, every day.
func (r *rig) seedCpt(ctx context.Context, t *testing.T) {
	t.Helper()
	out, err := r.applyCpt.Handle(ctx, "ev-cpt-1", CptChange{
		SiteID: "SITE-1", Timezone: "UTC",
		Cutoffs: []repository.CutoffEntry{{CptID: "cpt-1600", LocalTime: "16:00", ShipMethod: "STDOUT-2DAY"}},
		Version: 1, Changed: tDock,
	})
	if err != nil || out != OutcomeApplied {
		t.Fatalf("seed cpt: out=%v err=%v", out, err)
	}
}

// seedLayout applies the zone and the staging lane the scans use.
func (r *rig) seedLayout(ctx context.Context, t *testing.T) {
	t.Helper()
	if out, err := r.applyLayout.HandleZone(ctx, "ev-zone-1", ZoneChange{ZoneID: "zone-1", SiteCode: "SITE-1", ZoneCode: "DOCK-D"}); err != nil || out != OutcomeApplied {
		t.Fatalf("seed zone: out=%v err=%v", out, err)
	}
	if out, err := r.applyLayout.HandleLocation(ctx, "ev-loc-1", LocationChange{Code: "STG-D04-A", ZoneID: "zone-1", Role: repository.RoleStaging}); err != nil || out != OutcomeApplied {
		t.Fatalf("seed location: out=%v err=%v", out, err)
	}
}

// seedAppt books appointment appt-2211 at door D04 of cpt-1600 (carrier
// STDOUT). Before a load of that cpt exists the copy is saved and the
// booking ignored; once one exists the door is assigned.
func (r *rig) seedAppt(ctx context.Context, t *testing.T, eventID string) Outcome {
	t.Helper()
	out, err := r.book.Handle(ctx, eventID, Appointment{
		AppointmentID: "appt-2211", Site: "SITE-1", Direction: "OUTBOUND",
		DoorCode: "D04", CptID: "cpt-1600", Carrier: "STDOUT", CarrierScac: "STDOUT",
		WindowStart: tDock, WindowEnd: tCutoff, Version: 1, BookedAt: tDock,
	})
	if err != nil {
		t.Fatalf("seed appointment: %v", err)
	}
	return out
}

// manifest manifests one package of an order onto the 16:00 cutoff load.
func (r *rig) manifest(ctx context.Context, t *testing.T, eventID, pkg, order string) Outcome {
	t.Helper()
	out, err := r.expect.Handle(ctx, eventID, Manifest{
		PackageID: shared.PackageID(pkg), SiteID: "SITE-1", SourceOrderID: order,
		OrderRef: order + "-w1", Cutoff: tCutoff, WeightG: 1200, ManifestedAt: tDock,
	})
	if err != nil {
		t.Fatalf("manifest %s: %v", pkg, err)
	}
	return out
}

// openLoad runs the standard prologue: schedule, layout, appointment copy,
// one manifested package, the door assigned and the trailer docked, so the
// load is LOADING at door D04 with trl-91 attached and pkg EXPECTED.
func (r *rig) openLoad(ctx context.Context, t *testing.T) {
	t.Helper()
	r.seedCpt(ctx, t)
	r.seedLayout(ctx, t)
	r.seedAppt(ctx, t, "ev-book-early") // copy saved, no load yet: ignored
	r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421")
	if out := r.seedAppt(ctx, t, "ev-book-assign"); out != OutcomeApplied {
		t.Fatalf("assign appointment: out=%v", out)
	}
	if out, err := r.dock.Handle(ctx, "ev-dock-1", Docking{TrailerID: "trl-91", Site: "SITE-1", DoorCode: "D04", DockedAt: tDock}); err != nil || out != OutcomeApplied {
		t.Fatalf("dock: out=%v err=%v", out, err)
	}
}

// loadID returns the single open load's id.
func (r *rig) loadID(ctx context.Context, t *testing.T) shared.LoadID {
	t.Helper()
	keys, err := r.loads.OpenLoadKeys(ctx, "SITE-1")
	if err != nil || len(keys) != 1 {
		t.Fatalf("open load keys: %v %v", keys, err)
	}
	l, err := r.loads.FindOpenByKey(ctx, keys[0])
	if err != nil {
		t.Fatalf("find open load: %v", err)
	}
	return l.ID()
}
