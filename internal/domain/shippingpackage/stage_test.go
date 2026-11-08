package shippingpackage

import (
	"errors"
	"testing"
	"time"
)

var pkgAt = time.Date(2026, 11, 6, 14, 20, 0, 0, time.UTC)
var dock = pkgAt

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// expectedPackage is pkg-1029 of SITE-1 on load-7f3a.
func expectedPackage(t *testing.T) *ShippingPackage {
	t.Helper()
	p, err := Expect(ExpectInput{
		PackageID: "pkg-1029", Site: "SITE-1",
		SourceOrderID: "order-88421", WorkUnitRef: "order-88421-line-1", At: pkgAt,
	})
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	mustDo(t, p.AssignToLoad("load-7f3a"))
	return p
}

func stagedPackage(t *testing.T) *ShippingPackage {
	t.Helper()
	p := expectedPackage(t)
	mustDo(t, p.Stage(StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}))
	p.PullEvents()
	return p
}

func loadedPackage(t *testing.T) *ShippingPackage {
	t.Helper()
	p := stagedPackage(t)
	mustDo(t, p.Load(LoadInput{LoadID: "load-7f3a", DoorCode: "D04", LoadDoor: "D04", At: pkgAt}))
	p.PullEvents()
	return p
}

func loadInput() LoadInput {
	return LoadInput{LoadID: "load-7f3a", DoorCode: "D04", LoadDoor: "D04", At: pkgAt}
}

func TestExpectRecordsExpected(t *testing.T) {
	p, err := Expect(ExpectInput{PackageID: "pkg-1", Site: "SITE-1", At: pkgAt})
	mustDo(t, err)
	if p.State() != StateExpected || p.Version() != 1 {
		t.Fatalf("state = %s version = %d", p.State(), p.Version())
	}
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("Expect raised %+v", evs)
	}
	if p.SourceOrderID() != "" || p.WorkUnitRef() != "" {
		t.Fatalf("correlation not optional: %+v", p)
	}
}

func TestExpectValidates(t *testing.T) {
	_, err := Expect(ExpectInput{PackageID: "", Site: "SITE-1", At: pkgAt})
	wantErr(t, err, ErrRequiredField)
	_, err = Expect(ExpectInput{PackageID: "pkg-1", Site: "", At: pkgAt})
	wantErr(t, err, ErrRequiredField)
	_, err = Expect(ExpectInput{PackageID: "pkg-1", Site: "SITE-1", At: time.Time{}})
	wantErr(t, err, ErrNoTimestamp)
}

func TestAssignToLoadOneLoadAtATime(t *testing.T) {
	p := expectedPackage(t)
	wantErr(t, p.AssignToLoad("load-9999"), ErrPackageOnAnotherLoad)
	if p.LoadID() != "load-7f3a" {
		t.Fatalf("load = %q", p.LoadID())
	}
	mustDo(t, p.AssignToLoad("load-7f3a")) // same load: no-op
	wantErr(t, p.AssignToLoad(""), ErrRequiredField)
}

func TestStageAtStagingLocation(t *testing.T) {
	p := expectedPackage(t)
	mustDo(t, p.Stage(StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}))
	if p.State() != StateStaged || p.StagingLocation() != "STG-D04-A" {
		t.Fatalf("state = %s loc = %q", p.State(), p.StagingLocation())
	}
	evs := p.PullEvents()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev, ok := evs[0].(PackageStaged)
	if !ok || ev.LoadID != "load-7f3a" {
		t.Fatalf("PackageStaged wrong: %+v", evs[0])
	}
	if ev.StagingLocation != "STG-D04-A" || ev.PackageVersion() != 2 || !ev.StagedAt.Equal(pkgAt) {
		t.Fatalf("PackageStaged fields wrong: %+v", ev)
	}
	// Staging again at the same location is a no-op.
	mustDo(t, p.Stage(StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}))
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("re-stage raised %+v", evs)
	}
}

func TestStageRefusesNonStagingLocations(t *testing.T) {
	cases := []struct {
		name string
		in   StageInput
	}{
		{"storage role", StageInput{Location: "A-01-02", IsStaging: false, LocationSite: "SITE-1", At: pkgAt}},
		{"another site", StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-2", At: pkgAt}},
		{"empty location", StageInput{Location: "", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := expectedPackage(t)
			wantErr(t, p.Stage(tc.in), ErrNotStagingLocation)
		})
	}
}

func TestStageFromWrongStates(t *testing.T) {
	loaded := loadedPackage(t)
	wantErr(t, loaded.Stage(StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}), ErrInvalidTransition)
	shortShipped := expectedPackage(t)
	mustDo(t, shortShipped.MarkShortShipped("DIVERTED", pkgAt))
	wantErr(t, shortShipped.Stage(StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}), ErrInvalidTransition)
	relocated := stagedPackage(t)
	wantErr(t, relocated.Stage(StageInput{Location: "STG-D05-B", IsStaging: true, LocationSite: "SITE-1", At: pkgAt}), ErrInvalidTransition)
	wantErr(t, expectedPackage(t).Stage(StageInput{Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: time.Time{}}), ErrNoTimestamp)
}

func TestExpectCarriesWeight(t *testing.T) {
	p, err := Expect(ExpectInput{PackageID: "pkg-1", Site: "S1", WeightG: 10300, At: dock})
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	if p.WeightG() != 10300 {
		t.Fatalf("WeightG = %v, want 10300", p.WeightG())
	}
	r, err := Rehydrate(RehydrateParams{ID: "pkg-1", Site: "S1", WeightG: 250, State: StateExpected, Version: 1})
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if r.WeightG() != 250 {
		t.Fatalf("rehydrated WeightG = %v, want 250", r.WeightG())
	}
	if _, err := Expect(ExpectInput{PackageID: "pkg-1", Site: "S1", WeightG: -1, At: dock}); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("negative weight err = %v, want ErrInvalidWeight", err)
	}
}
