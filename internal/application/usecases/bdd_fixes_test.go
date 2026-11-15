package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// TestCloseLoadRefusesAShortShipWithoutReasonOrPackage pins the validation of
// the short-ship entries: a bad entry is refused with the shared error (the
// REST adapter maps it to 400 bad-request) before any package is touched,
// whatever state the named package is in.
func TestCloseLoadRefusesAShortShipWithoutReasonOrPackage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)                                // pkg-1029 LOADED
	r.manifest(ctx, t, "ev-man-2", "pkg-2055", "order-88423") // EXPECTED

	_, err := r.close.Handle(ctx, loadID, []shared.ShortShip{{PackageID: "pkg-2055", Reason: ""}})
	if !errors.Is(err, shared.ErrShortShipReasonRequired) {
		t.Fatalf("empty reason: err = %v, want ErrShortShipReasonRequired", err)
	}
	_, err = r.close.Handle(ctx, loadID, []shared.ShortShip{{PackageID: "", Reason: "NO-STOCK"}})
	if !errors.Is(err, shared.ErrShortShipPackageRequired) {
		t.Fatalf("empty package: err = %v, want ErrShortShipPackageRequired", err)
	}
	// A valid entry after an invalid one: the whole command is refused and
	// the valid entry's package is left alone.
	_, err = r.close.Handle(ctx, loadID, []shared.ShortShip{
		{PackageID: "pkg-2055", Reason: "NO-STOCK"}, {PackageID: "pkg-1029", Reason: ""},
	})
	if !errors.Is(err, shared.ErrShortShipReasonRequired) {
		t.Fatalf("mixed entries: err = %v, want ErrShortShipReasonRequired", err)
	}
	p, perr := r.pkgs.Get(ctx, "pkg-2055")
	if perr != nil || p.State() != shippingpackage.StateExpected {
		t.Fatalf("pkg-2055 = %v %v, want untouched EXPECTED", p, perr)
	}
	if got := r.published("LoadClosed"); got != 0 {
		t.Fatalf("LoadClosed x%d, want 0", got)
	}
}

// TestCancelLoadNamesTheStateRefusalBeforeThePackages pins the order of the
// cancel refusals (apis/openapi.yaml cancelLoad): the load's state first
// (load-confirmed, load-state), the packages only for a PLANNED load.
func TestCancelLoadNamesTheStateRefusalBeforeThePackages(t *testing.T) {
	ctx := context.Background()

	t.Run("PLANNED with a package: load-has-packages", func(t *testing.T) {
		r := newRig(t)
		r.seedCpt(ctx, t)
		r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421")
		_, err := r.cancel.Handle(ctx, r.loadID(ctx, t), "no carrier")
		if !errors.Is(err, ErrLoadHasPackages) {
			t.Fatalf("err = %v, want ErrLoadHasPackages", err)
		}
	})
	t.Run("LOADING: load-state, not load-has-packages", func(t *testing.T) {
		r := newRig(t)
		loadID := loadedLoad(t, r)
		_, err := r.cancel.Handle(ctx, loadID, "no carrier")
		if !errors.Is(err, outboundload.ErrInvalidTransition) || errors.Is(err, ErrLoadHasPackages) {
			t.Fatalf("err = %v, want ErrInvalidTransition", err)
		}
	})
	t.Run("CLOSED: load-state", func(t *testing.T) {
		r := newRig(t)
		loadID := loadedLoad(t, r)
		if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
			t.Fatal(err)
		}
		_, err := r.cancel.Handle(ctx, loadID, "no carrier")
		if !errors.Is(err, outboundload.ErrInvalidTransition) {
			t.Fatalf("err = %v, want ErrInvalidTransition", err)
		}
	})
	t.Run("SHIP_CONFIRMED: load-confirmed", func(t *testing.T) {
		r := newRig(t)
		loadID := loadedLoad(t, r)
		if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
			t.Fatal(err)
		}
		if out, err := r.depart.Handle(ctx, "ev-dep-1", Departure{TrailerID: "trl-91", Site: "SITE-1", SealNumber: "SL-4471", DepartedAt: tDepart}); err != nil || out != OutcomeApplied {
			t.Fatalf("depart: %v %v", out, err)
		}
		_, err := r.cancel.Handle(ctx, loadID, "no carrier")
		if !errors.Is(err, outboundload.ErrLoadConfirmed) {
			t.Fatalf("err = %v, want ErrLoadConfirmed", err)
		}
		if got := r.published("LoadCancelled"); got != 0 {
			t.Fatalf("LoadCancelled x%d, want 0", got)
		}
	})
}

// TestListPackagesEmitsACursorOnlyWhenAnotherPageFollows pins openapi LoadPage
// / LoadPackagePage ("null after the last"): an exactly-full last page has no
// cursor, so following a cursor never lands on an empty page.
func TestListPackagesEmitsACursorOnlyWhenAnotherPageFollows(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)                                // pkg-1029 LOADED
	r.manifest(ctx, t, "ev-man-2", "pkg-2055", "order-88423") // second package, same load

	page, next, err := r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, "", 2)
	if err != nil || len(page) != 2 || next != "" {
		t.Fatalf("limit 2 of 2: %d %q %v, want 2 items and no cursor", len(page), next, err)
	}
	page, next, err = r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, "", 1)
	if err != nil || len(page) != 1 || next != "pkg-1029" {
		t.Fatalf("limit 1 of 2: %d %q %v, want 1 item and cursor pkg-1029", len(page), next, err)
	}
	page, next, err = r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, next, 1)
	if err != nil || len(page) != 1 || next != "" {
		t.Fatalf("second page: %d %q %v, want 1 item and no cursor", len(page), next, err)
	}
}

// TestListLoadsEmitsACursorOnlyWhenAnotherPageFollows is the same rule for
// the load listing.
func TestListLoadsEmitsACursorOnlyWhenAnotherPageFollows(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadedLoad(t, r)
	// The loaded one plus an empty planned one.
	empty, _, err := outboundload.Plan(outboundload.PlanInput{
		LoadID: "load-empty", Site: "SITE-1", Cpt: "cpt-1800",
		OperatingDate: "2026-11-06", ShipMethod: "STDOUT-2DAY",
		Cutoff: tCutoff.Add(2 * timeHour), At: tDock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.loads.Save(ctx, empty, 0); err != nil {
		t.Fatal(err)
	}
	page, next, err := r.queries.ListLoads(ctx, repository.LoadFilter{}, "", 2)
	if err != nil || len(page) != 2 || next != "" {
		t.Fatalf("limit 2 of 2: %d %q %v, want 2 items and no cursor", len(page), next, err)
	}
	page, next, err = r.queries.ListLoads(ctx, repository.LoadFilter{}, "", 1)
	if err != nil || len(page) != 1 || next == "" {
		t.Fatalf("limit 1 of 2: %d %q %v, want 1 item and a cursor", len(page), next, err)
	}
	page, next, err = r.queries.ListLoads(ctx, repository.LoadFilter{}, next, 1)
	if err != nil || len(page) != 1 || next != "" {
		t.Fatalf("second page: %d %q %v, want 1 item and no cursor", len(page), next, err)
	}
}

// TestCloseLoadRecordsADivertedPackageOnce pins the diverted-package ruling:
// a package the diversion policy short-shipped is recorded once on the close.
// When the command names it, the operator's reason is the one recorded; when
// the command does not, the record says DIVERTED.
func TestCloseLoadRecordsADivertedPackageOnce(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*rig, shared.LoadID) {
		r := newRig(t)
		loadID := loadedLoad(t, r)
		r.manifest(ctx, t, "ev-man-2", "pkg-2055", "order-88423")
		if out, err := r.divert.Handle(ctx, "ev-div-1", "pkg-2055", tDepart); err != nil || out != OutcomeApplied {
			t.Fatalf("divert: %v %v", out, err)
		}
		return r, loadID
	}

	t.Run("not named: DIVERTED", func(t *testing.T) {
		r, loadID := setup(t)
		l, err := r.close.Handle(ctx, loadID, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := l.ShortShips()
		if len(got) != 1 || got[0].PackageID != "pkg-2055" || got[0].Reason != "DIVERTED" {
			t.Fatalf("short-ships = %v, want pkg-2055 DIVERTED once", got)
		}
	})
	t.Run("named: the operator's reason, still once", func(t *testing.T) {
		r, loadID := setup(t)
		l, err := r.close.Handle(ctx, loadID, []shared.ShortShip{{PackageID: "pkg-2055", Reason: "DAMAGED"}})
		if err != nil {
			t.Fatal(err)
		}
		got := l.ShortShips()
		if len(got) != 1 || got[0].PackageID != "pkg-2055" || got[0].Reason != "DAMAGED" {
			t.Fatalf("short-ships = %v, want pkg-2055 DAMAGED once", got)
		}
	})
}
