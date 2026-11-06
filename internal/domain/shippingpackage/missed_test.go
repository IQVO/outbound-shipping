package shippingpackage

import (
	"testing"
	"time"
)

var sweepCutoff = time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)

func TestFlagMissedLoadRaisesOnce(t *testing.T) {
	p := expectedPackage(t)
	mustDo(t, p.FlagMissedLoad(sweepCutoff, sweepCutoff))
	if p.State() != StateExpected {
		t.Fatalf("flagging changed the state to %s", p.State())
	}
	if !p.MissedFlagged() {
		t.Fatal("missedFlagged not set")
	}
	evs := p.PullEvents()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev, ok := evs[0].(PackageMissedLoad)
	if !ok {
		t.Fatalf("event = %T", evs[0])
	}
	if ev.LoadID != "load-7f3a" || ev.SourceOrderID != "order-88421" {
		t.Fatalf("PackageMissedLoad fields wrong: %+v", ev)
	}
	if !ev.Cutoff.Equal(sweepCutoff) || !ev.DetectedAt.Equal(sweepCutoff) || ev.PackageVersion() != 2 {
		t.Fatalf("PackageMissedLoad header wrong: %+v", ev)
	}
	// The once-guard: a re-run sweep raises nothing and bumps nothing.
	before := p.Version()
	mustDo(t, p.FlagMissedLoad(sweepCutoff, sweepCutoff.Add(time.Minute)))
	if p.Version() != before {
		t.Fatalf("re-run bumped the version to %d", p.Version())
	}
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("re-run raised %+v", evs)
	}
}

func TestFlagMissedLoadAlsoFlagsStaged(t *testing.T) {
	p := stagedPackage(t)
	p.PullEvents()
	mustDo(t, p.FlagMissedLoad(sweepCutoff, sweepCutoff))
	if evs := p.PullEvents(); len(evs) != 1 || evs[0].EventName() != "PackageMissedLoad" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestFlagMissedLoadRefusesLoadedAndShipped(t *testing.T) {
	wantErr(t, loadedPackage(t).FlagMissedLoad(sweepCutoff, sweepCutoff), ErrInvalidTransition)
	shipped := loadedPackage(t)
	mustDo(t, shipped.MarkShipped(pkgAt))
	wantErr(t, shipped.FlagMissedLoad(sweepCutoff, sweepCutoff), ErrInvalidTransition)
}

func TestFlagMissedLoadValidates(t *testing.T) {
	unassigned, err := Expect(ExpectInput{PackageID: "pkg-9", Site: "SITE-1", At: pkgAt})
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	wantErr(t, unassigned.FlagMissedLoad(sweepCutoff, sweepCutoff), ErrLoadRequired)
	wantErr(t, expectedPackage(t).FlagMissedLoad(sweepCutoff, time.Time{}), ErrNoTimestamp)
}

func validRehydrateParams() RehydrateParams {
	return RehydrateParams{
		ID: "pkg-1029", Site: "SITE-1", SourceOrderID: "order-88421",
		WorkUnitRef: "order-88421-line-1", LoadID: "load-7f3a",
		StagingLoc: "STG-D04-A", State: StateStaged, Version: 2,
	}
}

func TestRehydrateRoundTripsEveryState(t *testing.T) {
	for _, state := range validStates {
		p := validRehydrateParams()
		p.State = state
		pkg, err := Rehydrate(p)
		if err != nil {
			t.Fatalf("Rehydrate(%s): %v", state, err)
		}
		if pkg.State() != state || pkg.Version() != 2 || pkg.ID() != "pkg-1029" {
			t.Fatalf("roundtrip lost data: %+v", pkg)
		}
		if evs := pkg.PullEvents(); len(evs) != 0 {
			t.Fatalf("rehydrated package has events: %+v", evs)
		}
	}
}

func TestRehydrateValidates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RehydrateParams)
		want error
	}{
		{"no id", func(p *RehydrateParams) { p.ID = "" }, ErrRequiredField},
		{"no site", func(p *RehydrateParams) { p.Site = "" }, ErrRequiredField},
		{"unknown state", func(p *RehydrateParams) { p.State = "PACKED" }, ErrInvalidState},
		{"empty state", func(p *RehydrateParams) { p.State = "" }, ErrInvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validRehydrateParams()
			tc.mut(&p)
			_, err := Rehydrate(p)
			wantErr(t, err, tc.want)
		})
	}
}

// Edge values of the version bound: 1 restores, 0 and -1 refuse.
func TestRehydrateVersionEdges(t *testing.T) {
	p := validRehydrateParams()
	p.Version = 1
	if _, err := Rehydrate(p); err != nil {
		t.Fatalf("version 1 refused: %v", err)
	}
	p.Version = 0
	_, err := Rehydrate(p)
	wantErr(t, err, ErrInvalidVersion)
	p.Version = -1
	_, err = Rehydrate(p)
	wantErr(t, err, ErrInvalidVersion)
}

func TestPullEventsDrains(t *testing.T) {
	p := stagedPackage(t)
	p.PullEvents()
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("second PullEvents returned %+v", evs)
	}
}
