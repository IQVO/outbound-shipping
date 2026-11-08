package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// timeHour keeps the fixture instants readable above.
const timeHour = time.Hour

// loadedLoad runs the prologue with one staged+loaded package: a LOADING
// load at D04 with trl-91 attached and pkg-1029 LOADED.
func loadedLoad(t *testing.T, r *rig) shared.LoadID {
	t.Helper()
	ctx := context.Background()
	r.openLoad(ctx, t)
	stagedAt(t, r, "pkg-1029")
	loadID := r.loadID(ctx, t)
	if _, err := r.load.Handle(ctx, "pkg-1029", loadID, "D04"); err != nil {
		t.Fatalf("load package: %v", err)
	}
	return loadID
}

func errorIs(target, want error) bool { return errors.Is(target, want) }

func TestCloseLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)

	l, err := r.close.Handle(ctx, loadID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.State() != "CLOSED" {
		t.Fatalf("state=%s want CLOSED", l.State())
	}
	if got := r.published("LoadClosed"); got != 1 {
		t.Fatalf("LoadClosed x%d, want 1", got)
	}
}

func TestCloseLoadOutstandingRefuses(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)                                // pkg-1029 LOADED
	r.manifest(ctx, t, "ev-man-2", "pkg-2055", "order-88423") // EXPECTED

	if _, err := r.close.Handle(ctx, loadID, nil); err == nil {
		t.Fatal("outstanding package must refuse the close")
	}
	if got := r.published("LoadClosed"); got != 0 {
		t.Fatalf("LoadClosed x%d, want 0", got)
	}
	// Regression (in-command short-ship): the command marks pkg-2055
	// SHORT_SHIPPED, so closing WITH the short-ship in the same command
	// must succeed -- outstanding is collected after the commanded
	// short-ships are applied, not before.
	l, err := r.close.Handle(ctx, loadID, []shared.ShortShip{{PackageID: "pkg-2055", Reason: "NO-STOCK"}})
	if err != nil {
		t.Fatal(err)
	}
	if l.State() != "CLOSED" {
		t.Fatalf("state=%s want CLOSED", l.State())
	}
	p, _ := r.pkgs.Get(ctx, "pkg-2055")
	if p.State() != shippingpackage.StateShortShipped {
		t.Fatalf("short-shipped=%s", p.State())
	}
	// A short-ship naming an unknown package refuses.
	r2 := newRig(t)
	r2.openLoad(ctx, t)
	lid := r2.loadID(ctx, t)
	if _, err := r2.close.Handle(ctx, lid, []shared.ShortShip{{PackageID: "pkg-ghost", Reason: "X"}}); err == nil {
		t.Fatal("unknown short-ship package must refuse")
	}
}

func TestReopenLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	l, err := r.reopen.Handle(ctx, loadID)
	if err != nil {
		t.Fatal(err)
	}
	if l.State() != "LOADING" {
		t.Fatalf("state=%s want LOADING", l.State())
	}
	if got := r.published("LoadReopened"); got != 1 {
		t.Fatalf("LoadReopened x%d, want 1", got)
	}
	if _, err := r.reopen.Handle(ctx, loadID); err == nil {
		t.Fatal("reopening a LOADING load must refuse")
	}
	if _, err := r.reopen.Handle(ctx, "load-nope"); err == nil {
		t.Fatal("unknown load must refuse")
	}
}

func TestCancelLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421")
	loadID := r.loadID(ctx, t)

	if _, err := r.cancel.Handle(ctx, loadID, "no carrier"); err == nil {
		t.Fatal("a load with packages assigned must refuse the cancel")
	}
	if got := r.published("LoadCancelled"); got != 0 {
		t.Fatalf("LoadCancelled x%d, want 0", got)
	}

	// A fresh load with no packages cancels.
	empty, evs, err := outboundload.Plan(outboundload.PlanInput{
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
	_ = evs
	l, err := r.cancel.Handle(ctx, empty.ID(), "no carrier")
	if err != nil {
		t.Fatal(err)
	}
	if l.State() != "CANCELLED" {
		t.Fatalf("state=%s want CANCELLED", l.State())
	}
	if got := r.published("LoadCancelled"); got != 1 {
		t.Fatalf("LoadCancelled x%d, want 1", got)
	}
	if _, err := r.cancel.Handle(ctx, "load-nope", "x"); err == nil {
		t.Fatal("unknown load must refuse")
	}
}

func TestVoidBillOfLading(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := r.depart.Handle(ctx, "ev-dep-1", Departure{TrailerID: "trl-91", Site: "SITE-1", SealNumber: "SL-4471", DepartedAt: tDepart}); err != nil || out != OutcomeApplied {
		t.Fatalf("depart: %v %v", out, err)
	}
	bol, err := r.bols.GetByLoad(ctx, loadID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.voidBol.Handle(ctx, bol.BolNumber(), "mis-load")
	if err != nil {
		t.Fatal(err)
	}
	if b.State() != "VOIDED" {
		t.Fatalf("bol state=%s want VOIDED", b.State())
	}
	if _, err := r.voidBol.Handle(ctx, bol.BolNumber(), "again"); err == nil {
		t.Fatal("double void must refuse")
	}
	if _, err := r.voidBol.Handle(ctx, shared.BolNumber("BOL-nope"), "x"); err == nil {
		t.Fatal("unknown bol must refuse")
	}
}
