// Package features_test hosts the godog (Cucumber for Go) acceptance suite.
// Wave 5: the scenarios drive the REAL service harness -- the same use cases
// cmd/api wires (intake dedupe, the policies, the transactional outbox), over
// the in-memory doubles, with the outbox recorder standing in for the relay.
// Producer facts arrive as fixture events: fulfillment, process-path,
// facility-layout and dock-scheduling per their develop contracts, yard facts
// (TrailerDocked, TrailerDeparted) per the proposed yard model (decision D5)
// until the yard contract merges. Nothing drives the domain aggregates
// directly: every step goes through a use case and every assertion reads the
// repositories or the recorded outbox messages.
package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// TestFeatures runs every Gherkin feature under features/.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

var (
	bddDock   = time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	bddCutoff = time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
	bddDepart = time.Date(2026, 11, 6, 16, 1, 30, 0, time.UTC)
)

// clockStub is the deterministic ports.Clock: every stamp the service writes
// in a scenario comes from this fixed instant.
type clockStub struct{ t time.Time }

func (c clockStub) Now() time.Time { return c.t }

// seqIDs mints deterministic load ids and BOL numbers.
type seqIDs struct{ n, b int }

func (i *seqIDs) NewLoadID() shared.LoadID {
	i.n++
	return shared.LoadID(fmt.Sprintf("load-bdd%d", i.n))
}

func (i *seqIDs) NewBolNumber(date outboundload.OperatingDate) shared.BolNumber {
	i.b++
	return shared.BolNumber(fmt.Sprintf("BOL-%s-%02d", date, i.b))
}

// service is the wired service under test: every use case exactly as cmd/api
// builds it, over the in-memory doubles (the Postgres adapters get the
// integration-tagged suite). The outbox recorder is what the "was published"
// assertions read.
type service struct {
	loads   *memory.LoadRepo
	pkgs    *memory.PackageRepo
	bols    *memory.BolRepo
	copies  *memory.Copies
	appts   *memory.Appointments
	rec     *memory.OutboxRecorder
	appt    usecases.Appointment
	docking usecases.Docking

	expect   *usecases.ExpectPackage
	divert   *usecases.DivertPackage
	book     *usecases.BookAppointment
	dock     *usecases.DockTrailer
	depart   *usecases.OnDeparture
	applyCpt *usecases.ApplyCptSchedule
	applyLay *usecases.ApplyLayout
	stage    *usecases.StagePackage
	loadPkg  *usecases.LoadPackage
	closeCmd *usecases.CloseLoad
	sweep    *usecases.SweepMissedLoads
}

// newService wires the harness. The appointment and docking fixtures are
// built here once; the scenario steps replay them.
func newService() *service {
	loads := memory.NewLoadRepo()
	pkgs := memory.NewPackageRepo(loads)
	copies, appts := memory.NewCopies(), memory.NewAppointments()
	processed := memory.NewProcessedEventRepo()
	rec := memory.NewOutboxRecorder()
	uow := memory.NewUnitOfWork(copies, processed, rec)
	sink := events.NewSink(rec)
	clock := clockStub{t: bddDock}
	ids := &seqIDs{}
	intake := usecases.Intake{UoW: uow, Processed: processed}
	s := &service{
		loads: loads, pkgs: pkgs, bols: memory.NewBolRepo(),
		copies: copies, appts: appts, rec: rec,
		appt: usecases.Appointment{
			AppointmentID: "appt-2211", Site: "SITE-1", Direction: "OUTBOUND",
			DoorCode: "D04", CptID: "cpt-1600", Carrier: "STDOUT", CarrierScac: "STDOUT",
			WindowStart: bddDock, WindowEnd: bddCutoff, Version: 1, BookedAt: bddDock,
		},
		docking: usecases.Docking{TrailerID: "trl-91", Site: "SITE-1", DoorCode: "D04", DockedAt: bddDock},
	}
	s.expect = &usecases.ExpectPackage{Intake: intake, Loads: loads, Packages: pkgs, Copies: copies, Sink: sink, IDs: ids, Clock: clock}
	s.divert = &usecases.DivertPackage{Intake: intake, Packages: pkgs, Clock: clock}
	s.book = &usecases.BookAppointment{Intake: intake, Loads: loads, Copies: appts, Sink: sink, Clock: clock}
	s.dock = &usecases.DockTrailer{Intake: intake, Loads: loads, Sink: sink, Clock: clock}
	s.depart = &usecases.OnDeparture{Intake: intake, Loads: loads, Packages: pkgs, Bols: s.bols, Copies: appts, Sink: sink, IDs: ids, Clock: clock}
	s.applyCpt = &usecases.ApplyCptSchedule{Intake: intake, Copies: copies}
	s.applyLay = &usecases.ApplyLayout{Intake: intake, Copies: copies}
	s.stage = &usecases.StagePackage{UoW: uow, Pkgs: pkgs, Layout: copies, Sink: sink, Clock: clock}
	s.loadPkg = &usecases.LoadPackage{UoW: uow, Pkgs: pkgs, Loads: loads, Sink: sink, Clock: clock}
	s.closeCmd = &usecases.CloseLoad{UoW: uow, Loads: loads, Pkgs: pkgs, Sink: sink, Clock: clock}
	s.sweep = &usecases.SweepMissedLoads{UoW: uow, Pkgs: pkgs, Sink: sink, Clock: clock}
	return s
}

// world is the per-scenario state on top of one wired service.
type world struct {
	ctx        context.Context
	svc        *service
	loadID     shared.LoadID
	planned    bool // the first manifestation planned the load (prologue ran)
	manifested map[string]string
	lastDepart string // CloudEvents id of the last applied departure
	lastInfo   usecases.Departure
}

func newWorld() *world {
	return &world{svc: newService(), manifested: map[string]string{}}
}

// --- helpers -----------------------------------------------------------

// published counts the recorded integration-topic messages by event name
// (the CloudEvents type's last dot-segment): what the relay would drain to
// warehouse.outbound-shipping.events. The analytics twin is excluded.
func (w *world) published(name string) int {
	n := 0
	for _, m := range w.svc.rec.Messages() {
		if m.Topic == events.TopicEvents && typeSuffix(m.EventType) == name {
			n++
		}
	}
	return n
}

// dataOf returns the CloudEvents `data` payloads of the integration-topic
// messages of one event name.
func (w *world) dataOf(name string) []map[string]any {
	var out []map[string]any
	for _, m := range w.svc.rec.Messages() {
		if m.Topic != events.TopicEvents || typeSuffix(m.EventType) != name {
			continue
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(m.Value, &env); err != nil {
			continue
		}
		out = append(out, env.Data)
	}
	return out
}

// firstPayload returns the data payload of event's first occurrence (nil
// when none were published).
func (w *world) firstPayload(event string) map[string]any {
	for _, data := range w.dataOf(event) {
		return data
	}
	return nil
}

func typeSuffix(t string) string {
	for i := len(t) - 1; i >= 0; i-- {
		if t[i] == '.' {
			return t[i+1:]
		}
	}
	return t
}

// resolveLoad finds the scenario's single open load and caches its id.
func (w *world) resolveLoad(ctx context.Context) error {
	if w.loadID != "" {
		return nil
	}
	keys, err := w.svc.loads.OpenLoadKeys(ctx, "SITE-1")
	if err != nil || len(keys) != 1 {
		return fmt.Errorf("open load keys: %v %v", keys, err)
	}
	l, err := w.svc.loads.FindOpenByKey(ctx, keys[0])
	if err != nil {
		return err
	}
	w.loadID = l.ID()
	return nil
}

// manifest runs the PackageManifested policy; on the FIRST manifestation of a
// scenario it also replays the door assignment (a later appointment event
// finds the now-existing load) and the docking that starts loading -- exactly
// the event order the real stream delivers.
func (w *world) manifest(ctx context.Context, pkg, order string) error {
	if _, done := w.manifested[pkg]; done {
		return nil // a re-quoted package is already on the load
	}
	if _, err := w.svc.expect.Handle(ctx, "fx-man-"+pkg, usecases.Manifest{
		PackageID: shared.PackageID(pkg), SiteID: "SITE-1", SourceOrderID: order,
		OrderRef: order + "-w1", Cutoff: bddCutoff, WeightG: 1200, ManifestedAt: bddDock,
	}); err != nil {
		return err
	}
	w.manifested[pkg] = order
	if err := w.resolveLoad(ctx); err != nil {
		return err
	}
	if w.planned {
		return nil
	}
	w.planned = true
	// The appointment re-delivery now finds the load and assigns door D04.
	if _, err := w.svc.book.Handle(ctx, "fx-book-assign", w.svc.appt); err != nil {
		return err
	}
	// TrailerDocked attaches trl-91 and starts loading (the model's only
	// StartLoading trigger).
	if _, err := w.svc.dock.Handle(ctx, "fx-dock-1", w.svc.docking); err != nil {
		return err
	}
	return nil
}

// applyDeparture feeds one TrailerDeparted through OnDeparture, remembering
// its id so a later "re-delivered" step can replay the exact occurrence.
func (w *world) applyDeparture(ctx context.Context, eventID string, d usecases.Departure) error {
	if d.Site == "" {
		d.Site = "SITE-1"
	}
	if _, err := w.svc.depart.Handle(ctx, eventID, d); err != nil {
		return err
	}
	w.lastDepart = eventID
	w.lastInfo = d
	return nil
}

// loadedPackages verifies the count of packages in a LOADED-or-SHIPPED state
// against the number the scenario quoted.
func (w *world) loadedPackages(ctx context.Context, want int) error {
	loaded := 0
	for pkg := range w.manifested {
		p, err := w.svc.pkgs.Get(ctx, shared.PackageID(pkg))
		if err != nil {
			return err
		}
		switch p.State() {
		case shippingpackage.StateLoaded, shippingpackage.StateShipped:
			loaded++
		}
	}
	if loaded != want {
		return fmt.Errorf("loaded packages = %d, want %d", loaded, want)
	}
	return nil
}

// --- scenario wiring ---------------------------------------------------

// InitializeScenario binds the Gherkin steps to the wired service. godog
// matches step text against every registered regex regardless of keyword, so
// each step is registered exactly once.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	w.ctx = context.Background()
	registerSeedSteps(sc, w)
	registerScanSteps(sc, w)
	registerDepartureSteps(sc, w)
	registerAssertionSteps(sc, w)
}

func registerSeedSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^site "([^"]*)" runs the "([^"]*)" cutoff shipped "([^"]*)"$`,
		func(site, localTime, method string) error {
			if site != "SITE-1" || localTime != "16:00" {
				return fmt.Errorf("fixture mismatch: %s %s", site, localTime)
			}
			out, err := w.svc.applyCpt.Handle(w.ctx, "fx-cpt-1", usecases.CptChange{
				SiteID: shared.SiteID(site), Timezone: "UTC",
				Cutoffs: []repository.CutoffEntry{{
					CptID: "cpt-1600", LocalTime: localTime, ShipMethod: shared.ShipMethod(method),
				}},
				Version: 1, Changed: bddDock,
			})
			if err != nil {
				return err
			}
			if out != usecases.OutcomeApplied {
				return fmt.Errorf("cpt seed: outcome = %s", out)
			}
			return nil
		})

	sc.Step(`^dock zone "([^"]*)" and staging lane "([^"]*)" exist at site "([^"]*)"$`,
		func(zone, lane, site string) error {
			if site != "SITE-1" {
				return fmt.Errorf("fixture mismatch: %s", site)
			}
			if out, err := w.svc.applyLay.HandleZone(w.ctx, "fx-zone-1", usecases.ZoneChange{
				ZoneID: "zone-1", SiteCode: site, ZoneCode: zone,
			}); err != nil || out != usecases.OutcomeApplied {
				return fmt.Errorf("zone seed: %v %v", out, err)
			}
			_, err := w.svc.applyLay.HandleLocation(w.ctx, "fx-loc-1", usecases.LocationChange{
				Code: lane, ZoneID: "zone-1", Role: repository.RoleStaging,
			})
			return err
		})

	sc.Step(`^appointment "([^"]*)" is booked at door "([^"]*)" for the "([^"]*)" cutoff$`,
		func(appt, door, localTime string) error {
			if appt != string(w.svc.appt.AppointmentID) || door != string(w.svc.appt.DoorCode) || localTime != "16:00" {
				return fmt.Errorf("fixture mismatch: %s %s %s", appt, door, localTime)
			}
			// No load exists yet: the copy is saved, the booking ignored --
			// the assignment replay happens on the first manifestation.
			out, err := w.svc.book.Handle(w.ctx, "fx-book-early", w.svc.appt)
			if err != nil {
				return err
			}
			if out != usecases.OutcomeIgnored {
				return fmt.Errorf("booking before any load: outcome = %s, want ignored", out)
			}
			return nil
		})
}

func registerScanSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^package "([^"]*)" of order "([^"]*)" is manifested on the load$`,
		func(pkg, order string) error { return w.manifest(w.ctx, pkg, order) })

	sc.Step(`^package "([^"]*)" stages at "([^"]*)"$`, func(pkg, loc string) error {
		_, err := w.svc.stage.Handle(w.ctx, shared.PackageID(pkg), loc)
		return err
	})

	sc.Step(`^package "([^"]*)" loads through door "([^"]*)"$`, func(pkg, door string) error {
		if err := w.resolveLoad(w.ctx); err != nil {
			return err
		}
		_, err := w.svc.loadPkg.Handle(w.ctx, shared.PackageID(pkg), w.loadID, shared.DoorCode(door))
		return err
	})

	sc.Step(`^package "([^"]*)" is diverted before the cutoff$`, func(pkg string) error {
		_, err := w.svc.divert.Handle(w.ctx, "fx-divert-"+pkg, shared.PackageID(pkg), bddDepart)
		return err
	})

	sc.Step(`^the load closes with (\d+) packages? loaded$`, func(loaded int) error {
		if err := w.resolveLoad(w.ctx); err != nil {
			return err
		}
		if _, err := w.svc.closeCmd.Handle(w.ctx, w.loadID, nil); err != nil {
			return err
		}
		return w.loadedPackages(w.ctx, loaded)
	})

	sc.Step(`^the supervisor short-ships package "([^"]*)" with reason "([^"]*)" and closes the load$`,
		func(pkg, reason string) error {
			if err := w.resolveLoad(w.ctx); err != nil {
				return err
			}
			_, err := w.svc.closeCmd.Handle(w.ctx, w.loadID, []shared.ShortShip{{
				PackageID: shared.PackageID(pkg), Reason: reason,
			}})
			return err
		})

	sc.Step(`^the cutoff sweep runs at the cutoff$`, func() error {
		_, _, err := w.svc.sweep.Handle(w.ctx, "SITE-1", bddCutoff)
		return err
	})
}

func registerDepartureSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^trailer "([^"]*)" departs with seal "([^"]*)"( early, before the load closes| again)?$`,
		func(trailer, seal, mode string) error {
			if mode == " again" {
				if w.lastDepart == "" {
					return fmt.Errorf("no departure to re-deliver")
				}
				// Same CloudEvents id: the inbox claim must dedupe it.
				return w.applyDeparture(w.ctx, w.lastDepart, w.lastInfo)
			}
			return w.applyDeparture(w.ctx, "fx-depart-"+trailer+"-"+seal, usecases.Departure{
				TrailerID: shared.TrailerID(trailer), SealNumber: seal, DepartedAt: bddDepart,
			})
		})

	sc.Step(`^the same departure is re-delivered$`, func() error {
		if w.lastDepart == "" {
			return fmt.Errorf("no departure to re-deliver")
		}
		return w.applyDeparture(w.ctx, w.lastDepart, w.lastInfo)
	})
}

func registerAssertionSteps(sc *godog.ScenarioContext, w *world) {
	registerStateAssertions(sc, w)
	registerPublicationAssertions(sc, w)
}

func registerStateAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the load is "([^"]*)"$`, func(state string) error {
		if err := w.resolveLoad(w.ctx); err != nil {
			return err
		}
		l, err := w.svc.loads.Get(w.ctx, w.loadID)
		if err != nil {
			return err
		}
		if got := string(l.State()); got != state {
			return fmt.Errorf("load state = %s, want %s", got, state)
		}
		return nil
	})

	sc.Step(`^package "([^"]*)" is "([^"]*)"$`, func(pkg, state string) error {
		p, err := w.svc.pkgs.Get(w.ctx, shared.PackageID(pkg))
		if err != nil {
			return err
		}
		if got := string(p.State()); got != state {
			return fmt.Errorf("package %s state = %s, want %s", pkg, got, state)
		}
		return nil
	})

	sc.Step(`^no bill of lading exists$`, func() error {
		if err := w.resolveLoad(w.ctx); err != nil {
			return err
		}
		if _, err := w.svc.bols.GetByLoad(w.ctx, w.loadID); err == nil {
			return fmt.Errorf("a bill of lading was issued for a load that never confirmed")
		}
		return nil
	})
}

func registerPublicationAssertions(sc *godog.ScenarioContext, w *world) {
	registerPublicationCountAssertions(sc, w)
	registerPublicationPayloadAssertions(sc, w)
}

func registerPublicationCountAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^exactly (\d+) "([^"]*)" events? (?:was|were) published$`, func(n int, event string) error {
		if got := w.published(event); got != n {
			return fmt.Errorf("%s = %d, want %d", event, got, n)
		}
		return nil
	})

	sc.Step(`^exactly (\d+) "([^"]*)" events? (?:was|were) published for package "([^"]*)"$`,
		func(n int, event, pkg string) error {
			got := 0
			for _, data := range w.dataOf(event) {
				if data["package_id"] == pkg {
					got++
				}
			}
			if got != n {
				return fmt.Errorf("%s for %s = %d, want %d", event, pkg, got, n)
			}
			return nil
		})
}

func registerPublicationPayloadAssertions(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^exactly (\d+) "([^"]*)" events? (?:was|were) published, listing order "([^"]*)"$`,
		func(n int, event, order string) error {
			if got := w.published(event); got != n {
				return fmt.Errorf("%s = %d, want %d", event, got, n)
			}
			data := w.firstPayload(event)
			if data == nil {
				return fmt.Errorf("no %s published to inspect", event)
			}
			listed, _ := data["orders"].([]any)
			if len(listed) != 1 {
				return fmt.Errorf("confirmed orders = %v, want exactly [%s]", listed, order)
			}
			row, _ := listed[0].(map[string]any)
			if got := row["order_id"]; got != order {
				return fmt.Errorf("confirmed order = %v, want %s", got, order)
			}
			return nil
		})

	sc.Step(`^exactly (\d+) "([^"]*)" events? (?:was|were) published with seal "([^"]*)"$`,
		func(n int, event, seal string) error {
			if got := w.published(event); got != n {
				return fmt.Errorf("%s = %d, want %d", event, got, n)
			}
			data := w.firstPayload(event)
			if data == nil {
				return fmt.Errorf("no %s published to inspect", event)
			}
			if got, _ := data["seal_number"].(string); got != seal {
				return fmt.Errorf("issued seal = %q, want %q", got, seal)
			}
			return nil
		})
}
