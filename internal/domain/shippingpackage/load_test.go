package shippingpackage

import (
	"testing"
	"time"
)

func TestLoadFromStagedAndExpected(t *testing.T) {
	for _, setup := range []struct {
		name  string
		state State
		pkg   func(t *testing.T) *ShippingPackage
	}{
		{"from staged", StateStaged, stagedPackage},
		{"from expected", StateExpected, expectedPackage},
	} {
		t.Run(setup.name, func(t *testing.T) {
			p := setup.pkg(t)
			if err := p.Load(loadInput()); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if p.State() != StateLoaded {
				t.Fatalf("state = %s", p.State())
			}
			evs := p.PullEvents()
			if len(evs) != 1 {
				t.Fatalf("events = %d, want 1", len(evs))
			}
			ev, ok := evs[0].(PackageLoaded)
			if !ok || ev.LoadID != "load-7f3a" || !ev.LoadedAt.Equal(pkgAt) {
				t.Fatalf("PackageLoaded wrong: %+v", evs[0])
			}
		})
	}
}

// Required table case: loading through another load's door refuses with
// ErrWrongLoad.
func TestLoadThroughAnotherLoadsDoorRefused(t *testing.T) {
	p := stagedPackage(t)
	in := loadInput()
	in.DoorCode = "D05" // the scanner went through another load's door
	err := p.Load(in)
	wantErr(t, err, ErrWrongLoad)
	if p.State() != StateStaged {
		t.Fatalf("refused load changed the state to %s", p.State())
	}
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("refused load raised %+v", evs)
	}
}

func TestLoadRefusesWrongLoad(t *testing.T) {
	p := stagedPackage(t)
	in := loadInput()
	in.LoadID = "load-9999"
	wantErr(t, p.Load(in), ErrWrongLoad)
	unassigned, err := Expect(ExpectInput{PackageID: "pkg-2", Site: "SITE-1", At: pkgAt})
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	in2 := LoadInput{LoadID: "", DoorCode: "D04", LoadDoor: "D04", At: pkgAt}
	wantErr(t, unassigned.Load(in2), ErrWrongLoad)
}

func TestLoadFromWrongStates(t *testing.T) {
	loaded := loadedPackage(t)
	wantErr(t, loaded.Load(loadInput()), ErrInvalidTransition)
	short := expectedPackage(t)
	if err := short.MarkShortShipped("NO-STOCK", pkgAt); err != nil {
		t.Fatalf("MarkShortShipped: %v", err)
	}
	wantErr(t, short.Load(loadInput()), ErrInvalidTransition)
	shipped, err := Expect(ExpectInput{PackageID: "pkg-3", Site: "SITE-1", At: pkgAt})
	if err != nil {
		t.Fatalf("Expect: %v", err)
	}
	if err := shipped.AssignToLoad("load-7f3a"); err != nil {
		t.Fatalf("AssignToLoad: %v", err)
	}
	mustDo(t, shipped.Load(loadInput()))
	mustDo(t, shipped.MarkShipped(pkgAt))
	wantErr(t, shipped.Load(loadInput()), ErrAlreadyShipped)
}

func TestLoadValidates(t *testing.T) {
	p := stagedPackage(t)
	in := loadInput()
	in.DoorCode = ""
	wantErr(t, p.Load(in), ErrWrongLoad)
	in = loadInput()
	in.At = time.Time{}
	wantErr(t, p.Load(in), ErrNoTimestamp)
}

func TestUnloadBeforeShipConfirm(t *testing.T) {
	p := loadedPackage(t)
	mustDo(t, p.Unload("damaged-carton", pkgAt))
	if p.State() != StateStaged {
		t.Fatalf("state = %s, want STAGED", p.State())
	}
	ev, ok := p.PullEvents()[0].(PackageUnloaded)
	if !ok || ev.Reason != "damaged-carton" || ev.LoadID != "load-7f3a" {
		t.Fatalf("PackageUnloaded wrong: %+v", ev)
	}
	if ev.PackageVersion() != 4 || !ev.UnloadedAt.Equal(pkgAt) {
		t.Fatalf("PackageUnloaded header wrong: %+v", ev.Header)
	}
}

// Required table case: unloading after ship-confirm refuses with
// ErrAlreadyShipped.
func TestUnloadAfterShipConfirmRefused(t *testing.T) {
	p := loadedPackage(t)
	mustDo(t, p.MarkShipped(pkgAt))
	err := p.Unload("late", pkgAt)
	wantErr(t, err, ErrAlreadyShipped)
	if p.State() != StateShipped {
		t.Fatalf("refused unload changed the state to %s", p.State())
	}
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("refused unload raised %+v", evs)
	}
}

func TestUnloadFromWrongStates(t *testing.T) {
	wantErr(t, stagedPackage(t).Unload("r", pkgAt), ErrInvalidTransition)
	wantErr(t, loadedPackage(t).Unload("r", time.Time{}), ErrNoTimestamp)
}

func TestMarkShipped(t *testing.T) {
	p := loadedPackage(t)
	mustDo(t, p.MarkShipped(pkgAt))
	if p.State() != StateShipped || p.Version() != 3 {
		t.Fatalf("state = %s version = %d", p.State(), p.Version())
	}
	if evs := p.PullEvents(); len(evs) != 0 {
		t.Fatalf("MarkShipped raised %+v", evs)
	}
	wantErr(t, stagedPackage(t).MarkShipped(pkgAt), ErrInvalidTransition)
	wantErr(t, loadedPackage(t).MarkShipped(time.Time{}), ErrNoTimestamp)
}

func TestMarkShortShipped(t *testing.T) {
	for _, setup := range []struct {
		name string
		pkg  func(t *testing.T) *ShippingPackage
	}{
		{"from expected", expectedPackage},
		{"from staged", stagedPackage},
	} {
		t.Run(setup.name, func(t *testing.T) {
			p := setup.pkg(t)
			mustDo(t, p.MarkShortShipped("DIVERTED", pkgAt))
			if p.State() != StateShortShipped {
				t.Fatalf("state = %s", p.State())
			}
			if evs := p.PullEvents(); len(evs) != 0 {
				t.Fatalf("MarkShortShipped raised %+v", evs)
			}
		})
	}
	wantErr(t, expectedPackage(t).MarkShortShipped("", pkgAt), ErrShortShipReasonRequired)
	wantErr(t, loadedPackage(t).MarkShortShipped("DAMAGED", pkgAt), ErrInvalidTransition)
	wantErr(t, expectedPackage(t).MarkShortShipped("DAMAGED", time.Time{}), ErrNoTimestamp)
}
