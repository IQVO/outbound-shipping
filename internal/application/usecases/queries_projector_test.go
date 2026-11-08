package usecases

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// fold builds one BoardFold exactly as the analytics consumer decodes it.
func fold(name string, load shared.LoadID, at time.Time) BoardFold {
	return BoardFold{
		Name: name, Load: load, Site: "SITE-1", Cpt: "cpt-1600",
		Date: "2026-11-06", Method: "STDOUT-2DAY", Cutoff: tCutoff, At: at,
	}
}

// projectOne folds one named event and returns the outcome.
func projectOne(t *testing.T, r *rig, eventID string, f BoardFold) Outcome {
	t.Helper()
	out, err := r.project.Handle(context.Background(), eventID, f)
	if err != nil {
		t.Fatalf("project %s: %v", f.Name, err)
	}
	return out
}

// confirmedLoad runs the full path on a fresh rig -- planned, loading, one
// package staged+loaded, closed, departed, SHIP_CONFIRMED -- and returns the
// rig and the load it confirmed.
func confirmedLoad(t *testing.T) (*rig, shared.LoadID) {
	t.Helper()
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.depart.Handle(ctx, "ev-dep-1", Departure{
		TrailerID: "trl-91", Site: "SITE-1", SealNumber: "SL-4471", DepartedAt: tDepart,
	}); err != nil {
		t.Fatal(err)
	}
	return r, loadID
}

// TestProjectBoardFoldsTheStream pins the projector's intake: acted-on names
// apply, kept-but-unacted names are ignored, a re-delivered id is a
// duplicate.
func TestProjectBoardFoldsTheStream(t *testing.T) {
	r, loadID := confirmedLoad(t)

	// The projector folds the stream. Names it acts on apply; names it
	// merely keeps for future folds (the door assignment, the loading start)
	// are claimed and ignored; a re-delivered id is a duplicate.
	folds := []struct {
		name string
		want Outcome
	}{
		{"LoadPlanned", OutcomeApplied},
		{"LoadDoorAssigned", OutcomeIgnored},
		{"LoadingStarted", OutcomeIgnored},
		{"PackageStaged", OutcomeApplied},
		{"PackageLoaded", OutcomeApplied},
		{"LoadClosed", OutcomeApplied},
		{"ShipmentConfirmed", OutcomeApplied},
	}
	for i, f := range folds {
		if out := projectOne(t, r, foldEventID("bd", i), fold(f.name, loadID, tCutoff)); out != f.want {
			t.Fatalf("fold %s: outcome %s, want %s", f.name, out, f.want)
		}
	}
	if out := projectOne(t, r, "bd-dupe", fold("WeirdEvent", loadID, tCutoff)); out != OutcomeIgnored {
		t.Fatalf("unknown fold: outcome %s, want ignored", out)
	}
	if out := projectOne(t, r, foldEventID("bd", 0), fold("LoadPlanned", loadID, tCutoff)); out != OutcomeDuplicate {
		t.Fatalf("re-folded id: outcome %s, want duplicate", out)
	}
}

// TestBoardReportReads is the load-adherence report over the folded rows.
func TestBoardReportReads(t *testing.T) {
	ctx := context.Background()
	r, loadID := confirmedLoad(t)
	for i, name := range []string{"LoadPlanned", "PackageStaged", "PackageLoaded", "LoadClosed", "ShipmentConfirmed"} {
		if out := projectOne(t, r, foldEventID("rep", i), fold(name, loadID, tCutoff)); out != OutcomeApplied {
			t.Fatalf("fold %s: outcome %s", name, out)
		}
	}

	// A second, still-open load of the same cutoff plans on the next
	// manifestation (the confirmed one no longer blocks it), gets staged, and
	// its package misses the cutoff sweep.
	r.manifest(ctx, t, "ev-man-9", "pkg-9001", "order-9001")
	openID := r.loadID(ctx, t)
	stagedAt(t, r, "pkg-9001")
	if out := projectOne(t, r, "bd-l4-plan", fold("LoadPlanned", openID, tCutoff)); out != OutcomeApplied {
		t.Fatalf("load 4 plan fold: %s", out)
	}
	if out := projectOne(t, r, "bd-l4-staged", fold("PackageStaged", openID, tCutoff)); out != OutcomeApplied {
		t.Fatalf("load 4 staged fold: %s", out)
	}
	if _, _, err := r.sweep.Handle(ctx, "SITE-1", tCutoff); err != nil {
		t.Fatal(err)
	}
	if out := projectOne(t, r, "bd-l4-missed", fold("PackageMissedLoad", openID, tCutoff)); out != OutcomeApplied {
		t.Fatalf("load 4 missed fold: %s", out)
	}

	// The load-adherence report groups (site, cpt, date): the confirmed load
	// closed on time, the swept one never closed, one package missed.
	rows, at, err := r.queries.AdherenceReport(ctx, "SITE-1", "2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(tDock) {
		t.Fatalf("report generated at %s, want the clock's %s", at, tDock)
	}
	if len(rows) != 1 {
		t.Fatalf("adherence rows = %d, want 1: %+v", len(rows), rows)
	}
	got := rows[0]
	want := repository.AdherenceRow{
		Site: "SITE-1", Cpt: "cpt-1600", OperatingDate: "2026-11-06",
		LoadsClosed: 1, LoadsTotal: 2, PackagesMissed: 1, Adherence: 0.5,
	}
	if got != want {
		t.Fatalf("adherence row = %+v, want %+v", got, want)
	}

	// An invalid operating date refuses the report.
	if _, _, err := r.queries.AdherenceReport(ctx, "SITE-1", "2026-13-40"); !errors.Is(err, ErrInvalidListQuery) {
		t.Fatalf("invalid date: %v, want ErrInvalidListQuery", err)
	}
}

// TestBoardRiskAndMissedReads pins the at-risk and missed-load reads: the
// still-open load within the horizon, and the swept package once.
func TestBoardRiskAndMissedReads(t *testing.T) {
	ctx := context.Background()
	r, _ := confirmedLoad(t)

	// The same second-load fixture as the report test: open, staged, swept.
	r.manifest(ctx, t, "ev-man-9", "pkg-9001", "order-9001")
	openID := r.loadID(ctx, t)
	stagedAt(t, r, "pkg-9001")
	if out := projectOne(t, r, "bd-l5-plan", fold("LoadPlanned", openID, tCutoff)); out != OutcomeApplied {
		t.Fatalf("open load plan fold: %s", out)
	}
	if out := projectOne(t, r, "bd-l5-staged", fold("PackageStaged", openID, tCutoff)); out != OutcomeApplied {
		t.Fatalf("open load staged fold: %s", out)
	}
	if _, _, err := r.sweep.Handle(ctx, "SITE-1", tCutoff); err != nil {
		t.Fatal(err)
	}

	// Loads at risk: only the still-open load, within the horizon.
	risk, err := r.queries.LoadsAtRisk(ctx, "SITE-1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(risk) != 1 || risk[0].LoadID != openID {
		t.Fatalf("loads at risk = %+v, want only %s", risk, openID)
	}
	if risk[0].StagedCount != 1 {
		t.Fatalf("staged count = %d, want 1", risk[0].StagedCount)
	}

	// Missed loads: the swept package, once.
	missed, err := r.queries.MissedLoads(ctx, "SITE-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(missed) != 1 || missed[0].ID() != "pkg-9001" {
		t.Fatalf("missed loads = %v, want pkg-9001", missed)
	}
}

// foldEventID mints one stable projector event id per stream index.
func foldEventID(prefix string, i int) string {
	return fmt.Sprintf("%s-%d", prefix, i)
}

// TestQueriesServeTheLoadReads pins the load reads: single lookups and the
// keyset pagination.
func TestQueriesServeTheLoadReads(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)

	if l, err := r.queries.GetLoad(ctx, loadID); err != nil || l.ID() != loadID {
		t.Fatalf("get load: %v %v", l, err)
	}
	if _, err := r.queries.GetLoad(ctx, "load-nope"); !errors.Is(err, repository.ErrLoadNotFound) {
		t.Fatalf("unknown load: %v", err)
	}

	// Keyset pagination: a full page yields the cursor, the next page is empty.
	page, next, err := r.queries.ListLoads(ctx, repository.LoadFilter{}, "", 1)
	if err != nil || len(page) != 1 || next == "" {
		t.Fatalf("list loads page 1: %d %q %v", len(page), next, err)
	}
	page2, next2, err := r.queries.ListLoads(ctx, repository.LoadFilter{}, next, 1)
	if err != nil || len(page2) != 0 || next2 != "" {
		t.Fatalf("list loads page 2: %d %q %v", len(page2), next2, err)
	}
	// limit < 1 falls back to the default page size.
	if page, _, err := r.queries.ListLoads(ctx, repository.LoadFilter{}, "", 0); err != nil || len(page) != 1 {
		t.Fatalf("list loads default limit: %d %v", len(page), err)
	}

}

// TestQueriesServePackageReads pins the package reads (single + listing,
// including the packages of an unknown load).
func TestQueriesServePackageReads(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if p, err := r.queries.GetPackage(ctx, "pkg-1029"); err != nil || p.State() != shippingpackage.StateLoaded {
		t.Fatalf("get package: %v %v", p, err)
	}
	if _, err := r.queries.GetPackage(ctx, "pkg-nope"); !errors.Is(err, repository.ErrPackageNotFound) {
		t.Fatalf("unknown package: %v", err)
	}

	pkgs, _, err := r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, "", 10)
	if err != nil || len(pkgs) != 1 || pkgs[0].ID() != "pkg-1029" {
		t.Fatalf("list load packages: %d %v", len(pkgs), err)
	}
	if _, _, err := r.queries.ListLoadPackages(ctx, "load-nope", repository.PackageFilter{}, "", 10); !errors.Is(err, repository.ErrLoadNotFound) {
		t.Fatalf("packages of unknown load: %v", err)
	}

}

// TestQueriesServeTheBolRead pins the BOL read.
func TestQueriesServeTheBolRead(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.depart.Handle(ctx, "ev-dep-bol", Departure{
		TrailerID: "trl-91", Site: "SITE-1", SealNumber: "SL-4471", DepartedAt: tDepart,
	}); err != nil {
		t.Fatal(err)
	}
	bol, err := r.bols.GetByLoad(ctx, loadID)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := r.queries.GetBillOfLading(ctx, bol.BolNumber()); err != nil || b.LoadID() != loadID {
		t.Fatalf("get bol: %v %v", b, err)
	}
	if _, err := r.queries.GetBillOfLading(ctx, "BOL-nope"); !errors.Is(err, repository.ErrBolNotFound) {
		t.Fatalf("unknown bol: %v", err)
	}
}

// TestParseLimit pins the pagination bounds ("" = default, 1..200 valid).
func TestParseLimit(t *testing.T) {
	cases := []struct {
		in   string
		want int
		bad  bool
	}{
		{in: "", want: DefaultPageLimit},
		{in: "1", want: 1},
		{in: "200", want: MaxPageLimit},
		{in: "0", bad: true},
		{in: "201", bad: true},
		{in: "ten", bad: true},
	}
	for _, c := range cases {
		got, err := ParseLimit(c.in)
		if c.bad {
			if !errors.Is(err, ErrInvalidListQuery) {
				t.Fatalf("ParseLimit(%q) = %v, want ErrInvalidListQuery", c.in, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("ParseLimit(%q) = %d %v, want %d", c.in, got, err, c.want)
		}
	}
}

// TestRecordDoorWork pins the DoorWorkCompleted policy: a valid completion
// applies, an invalid one refuses ErrInvalidEvent, and a re-delivery is a
// duplicate.
func TestRecordDoorWork(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	done := repository.DoorWorkCompletion{
		TrailerID: "trl-91", Site: "SITE-1", DoorCode: "D04",
		AppointmentID: "appt-2211", CompletedAt: tDepart,
	}
	if out, err := r.doorWork.Handle(ctx, "ev-door-1", done); err != nil || out != OutcomeApplied {
		t.Fatalf("door work: %v %v", out, err)
	}
	if out, err := r.doorWork.Handle(ctx, "ev-door-1", done); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered door work: %v %v", out, err)
	}
	bad := done
	bad.DoorCode = ""
	if _, err := r.doorWork.Handle(ctx, "ev-door-2", bad); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("door work without a door: %v, want ErrInvalidEvent", err)
	}
}
