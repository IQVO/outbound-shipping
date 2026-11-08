//go:build integration

// Postgres integration suite: the real adapters behind cmd/api, each test on
// its own fresh database (testcontainers via internal/testing/pgtest — never
// env-gated, never skipped). Two proofs live here:
//
//   - TestExpectPackagePlansExactlyOneLoadUnderConcurrency: the one-open-load
//     race (PLAN wave 5 / risk "load-creation race"). N packages of one
//     (site, cpt, date, ship method) manifest simultaneously; the partial
//     unique index uq_open_load_per_cutoff must leave exactly ONE load, with
//     every package on it and one LoadPlanned published.
//   - TestServicePathOverPostgres: manifest -> door -> dock -> stage -> load
//     -> close (with an in-command short-ship, the CloseLoad regression) ->
//     depart, against the real schema, ending SHIP_CONFIRMED with one BOL and
//     exactly one ShipmentConfirmed even after the departure is re-delivered.
package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/postgres"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/system"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The fixture instants (UTC): a cutoff today so the schedule resolves, a
// departure just after it.
var (
	itDock   = time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	itCutoff = time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
	itDepart = time.Date(2026, 11, 6, 16, 1, 30, 0, time.UTC)
)

// wired boots a fresh database and builds every Postgres-backed use case.
func wired(t *testing.T) *wiring {
	t.Helper()
	pool := pgtest.NewPool(t)
	uow := postgres.NewUnitOfWork(pool)
	loads := postgres.NewLoadRepo(pool)
	pkgs := postgres.NewPackageRepo(pool)
	bols := postgres.NewBolRepo(pool)
	copies := postgres.NewCopies(pool)
	appts := postgres.NewAppointments(pool)
	processed := postgres.NewProcessedEventRepo(pool)
	outboxRepo := postgres.NewOutboxRepo(pool)
	sink := events.NewSink(outboxRepo)
	clock := system.Clock{}
	ids := system.RandomIDs{}
	intake := usecases.Intake{UoW: uow, Processed: processed}
	return &wiring{
		loads: loads, pkgs: pkgs, bols: bols, copies: copies,
		appts: appts,
		countTable: func(t *testing.T, table string) int {
			return pgtest.Count(t, pool, table)
		},
		countOutbox: func(t *testing.T, fullType string) int {
			var n int
			if err := pool.QueryRow(context.Background(),
				`SELECT count(*) FROM outbox_events WHERE event_type = $1 AND topic = $2`,
				fullType, events.TopicEvents).Scan(&n); err != nil {
				t.Fatalf("count outbox %s: %v", fullType, err)
			}
			return n
		},
		scanInt: func(t *testing.T, query string, args ...any) int {
			var n int
			if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			return n
		},
		applyCpt: &usecases.ApplyCptSchedule{Intake: intake, Copies: copies},
		applyLay: &usecases.ApplyLayout{Intake: intake, Copies: copies},
		book:     &usecases.BookAppointment{Intake: intake, Loads: loads, Copies: appts, Sink: sink, Clock: clock},
		expect:   &usecases.ExpectPackage{Intake: intake, Loads: loads, Packages: pkgs, Copies: copies, Sink: sink, IDs: ids, Clock: clock},
		dock:     &usecases.DockTrailer{Intake: intake, Loads: loads, Sink: sink, Clock: clock},
		depart:   &usecases.OnDeparture{Intake: intake, Loads: loads, Packages: pkgs, Bols: bols, Copies: appts, Sink: sink, IDs: ids, Clock: clock},
		stage:    &usecases.StagePackage{UoW: uow, Pkgs: pkgs, Layout: copies, Sink: sink, Clock: clock},
		load:     &usecases.LoadPackage{UoW: uow, Pkgs: pkgs, Loads: loads, Sink: sink, Clock: clock},
		close:    &usecases.CloseLoad{UoW: uow, Loads: loads, Pkgs: pkgs, Sink: sink, Clock: clock},
	}
}

// wiring holds the wired use cases plus pool-backed query closures (typed
// as funcs so this file never names a pgx type; pgtest owns the container).
type wiring struct {
	loads  *postgres.LoadRepo
	pkgs   *postgres.PackageRepo
	bols   *postgres.BolRepo
	copies *postgres.Copies
	appts  *postgres.Appointments

	// countTable rows of one table; countOutbox rows of one full
	// CloudEvents type on the integration topic; scanInt one integer query.
	countTable  func(t *testing.T, table string) int
	countOutbox func(t *testing.T, fullType string) int
	scanInt     func(t *testing.T, query string, args ...any) int

	applyCpt *usecases.ApplyCptSchedule
	applyLay *usecases.ApplyLayout
	book     *usecases.BookAppointment
	expect   *usecases.ExpectPackage
	dock     *usecases.DockTrailer
	depart   *usecases.OnDeparture
	stage    *usecases.StagePackage
	load     *usecases.LoadPackage
	close    *usecases.CloseLoad
}

// seedSchedule applies one CPTScheduleChanged: SITE-1 runs a single 16:00
// cutoff shipped STDOUT-2DAY.
func (w *wiring) seedSchedule(ctx context.Context, t *testing.T) {
	t.Helper()
	out, err := w.applyCpt.Handle(ctx, "it-cpt-1", usecases.CptChange{
		SiteID: "SITE-1", Timezone: "UTC",
		Cutoffs: []repository.CutoffEntry{{
			CptID: "cpt-1600", LocalTime: "16:00", ShipMethod: "STDOUT-2DAY",
		}},
		Version: 1, Changed: itDock,
	})
	if err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("seed cpt: out=%v err=%v", out, err)
	}
}

func fullType(entity, event string) string {
	return fmt.Sprintf("com.warehouse.wms.outbound-shipping.%s.%s", entity, event)
}

// TestExpectPackagePlansExactlyOneLoadUnderConcurrency is the partial-unique-
// index proof: eight simultaneous manifestations of one cutoff key must end
// with ONE open load carrying all eight packages, and ONE LoadPlanned.
func TestExpectPackagePlansExactlyOneLoadUnderConcurrency(t *testing.T) {
	w := wired(t)
	ctx := context.Background()
	w.seedSchedule(ctx, t)

	const racers = 8
	start := make(chan struct{})
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := w.expect.Handle(ctx, fmt.Sprintf("it-race-man-%d", i), usecases.Manifest{
				PackageID: shared.PackageID(fmt.Sprintf("pkg-race-%d", i)),
				SiteID:    "SITE-1", SourceOrderID: fmt.Sprintf("order-race-%d", i),
				OrderRef: fmt.Sprintf("order-race-%d-w1", i),
				Cutoff:   itCutoff, WeightG: 900, ManifestedAt: itDock,
			})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("raced manifestation failed: %v", err)
		}
	}

	if got := w.countTable(t, "outbound_loads"); got != 1 {
		t.Fatalf("outbound_loads rows = %d, want exactly 1 (the one-open-load rule)", got)
	}
	keys, err := w.loads.OpenLoadKeys(ctx, "SITE-1")
	if err != nil || len(keys) != 1 {
		t.Fatalf("open load keys: %v %v", keys, err)
	}
	l, err := w.loads.FindOpenByKey(ctx, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	assigned := w.scanInt(t, `SELECT count(*) FROM shipping_packages WHERE load_id = $1`, l.ID())
	if assigned != racers {
		t.Fatalf("packages on the one load = %d, want %d", assigned, racers)
	}
	if got := w.countOutbox(t, fullType("outboundload", "LoadPlanned")); got != 1 {
		t.Fatalf("LoadPlanned published x%d, want 1", got)
	}
}

// TestServicePathOverPostgres drives the full wave-5 command path against the
// real schema: manifest (plans the load), appointment assigns the door, the
// docking starts loading, stage + load the package, close WITH an in-command
// short-ship (the CloseLoad regression), depart, and the departure
// re-delivered changes nothing.
func TestServicePathOverPostgres(t *testing.T) {
	w := wired(t)
	ctx := context.Background()
	w.seedSchedule(ctx, t)

	if out, err := w.applyLay.HandleZone(ctx, "it-zone-1", usecases.ZoneChange{
		ZoneID: "zone-1", SiteCode: "SITE-1", ZoneCode: "DOCK-D",
	}); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("seed zone: %v %v", out, err)
	}
	if _, err := w.applyLay.HandleLocation(ctx, "it-loc-1", usecases.LocationChange{
		Code: "STG-D04-A", ZoneID: "zone-1", Role: repository.RoleStaging,
	}); err != nil {
		t.Fatalf("seed location: %v", err)
	}

	appt := usecases.Appointment{
		AppointmentID: "appt-3301", Site: "SITE-1", Direction: "OUTBOUND",
		DoorCode: "D04", CptID: "cpt-1600", Carrier: "STDOUT", CarrierScac: "STDOUT",
		WindowStart: itDock, WindowEnd: itCutoff, Version: 1, BookedAt: itDock,
	}
	if out, err := w.book.Handle(ctx, "it-book-early", appt); err != nil || out != usecases.OutcomeIgnored {
		t.Fatalf("book before load: %v %v", out, err)
	}

	manifest := func(eventID, pkg, order string) {
		t.Helper()
		if _, err := w.expect.Handle(ctx, eventID, usecases.Manifest{
			PackageID: shared.PackageID(pkg), SiteID: "SITE-1", SourceOrderID: order,
			OrderRef: order + "-w1", Cutoff: itCutoff, WeightG: 1200, ManifestedAt: itDock,
		}); err != nil {
			t.Fatalf("manifest %s: %v", pkg, err)
		}
	}
	manifest("it-man-1", "pkg-path-1", "order-path-1")

	keys, err := w.loads.OpenLoadKeys(ctx, "SITE-1")
	if err != nil || len(keys) != 1 {
		t.Fatalf("open load keys: %v %v", keys, err)
	}
	l, err := w.loads.FindOpenByKey(ctx, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	loadID := l.ID()

	if out, err := w.book.Handle(ctx, "it-book-assign", appt); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("assign door: %v %v", out, err)
	}
	if out, err := w.dock.Handle(ctx, "it-dock-1", usecases.Docking{
		TrailerID: "trl-path-91", Site: "SITE-1", DoorCode: "D04", DockedAt: itDock,
	}); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("dock: %v %v", out, err)
	}

	// A second package stays EXPECTED: the close below short-ships it IN THE
	// SAME COMMAND (the CloseLoad regression over Postgres).
	manifest("it-man-2", "pkg-path-2", "order-path-2")

	if _, err := w.stage.Handle(ctx, "pkg-path-1", "STG-D04-A"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := w.load.Handle(ctx, "pkg-path-1", loadID, "D04"); err != nil {
		t.Fatalf("load package: %v", err)
	}
	if _, err := w.close.Handle(ctx, loadID, []shared.ShortShip{{
		PackageID: "pkg-path-2", Reason: "NO-STOCK",
	}}); err != nil {
		t.Fatalf("close with an in-command short-ship: %v", err)
	}

	out, err := w.depart.Handle(ctx, "it-dep-1", usecases.Departure{
		TrailerID: "trl-path-91", Site: "SITE-1", SealNumber: "SL-IT-4471", DepartedAt: itDepart,
	})
	if err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("depart: %v %v", out, err)
	}

	// The load is SHIP_CONFIRMED, the loaded package SHIPPED, the short-shipped
	// one SHORT_SHIPPED, and one BOL row exists.
	confirmed, err := w.loads.Get(ctx, loadID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.State() != "SHIP_CONFIRMED" {
		t.Fatalf("load state = %s, want SHIP_CONFIRMED", confirmed.State())
	}
	for pkg, want := range map[string]string{"pkg-path-1": "SHIPPED", "pkg-path-2": "SHORT_SHIPPED"} {
		p, err := w.pkgs.Get(ctx, shared.PackageID(pkg))
		if err != nil {
			t.Fatal(err)
		}
		if string(p.State()) != want {
			t.Fatalf("%s state = %s, want %s", pkg, p.State(), want)
		}
	}
	if got := w.countTable(t, "bills_of_lading"); got != 1 {
		t.Fatalf("bills_of_lading rows = %d, want 1", got)
	}
	if got := w.countOutbox(t, fullType("outboundload", "ShipmentConfirmed")); got != 1 {
		t.Fatalf("ShipmentConfirmed published x%d, want 1", got)
	}
	if got := w.countOutbox(t, fullType("billoflading", "BillOfLadingIssued")); got != 1 {
		t.Fatalf("BillOfLadingIssued published x%d, want 1", got)
	}

	// The same departure re-delivered (same CloudEvents id) changes nothing.
	if out, err := w.depart.Handle(ctx, "it-dep-1", usecases.Departure{
		TrailerID: "trl-path-91", Site: "SITE-1", SealNumber: "SL-IT-4471", DepartedAt: itDepart,
	}); err != nil || out != usecases.OutcomeDuplicate {
		t.Fatalf("re-delivered departure: out=%v err=%v, want duplicate", out, err)
	}
	if got := w.countOutbox(t, fullType("outboundload", "ShipmentConfirmed")); got != 1 {
		t.Fatalf("ShipmentConfirmed after re-delivery x%d, want still 1", got)
	}
}
