package usecases

import (
	"context"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// stagedAt runs the prologue then stages pkg at the staging lane.
func stagedAt(t *testing.T, r *rig, pkg string) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.stage.Handle(ctx, shared.PackageID(pkg), "STG-D04-A"); err != nil {
		t.Fatalf("stage %s: %v", pkg, err)
	}
}

func TestStagePackage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)

	stagedAt(t, r, "pkg-1029")
	p, err := r.pkgs.Get(ctx, "pkg-1029")
	if err != nil {
		t.Fatal(err)
	}
	if p.State() != shippingpackage.StateStaged {
		t.Fatalf("state=%s want STAGED", p.State())
	}
	if got := r.published("PackageStaged"); got != 1 {
		t.Fatalf("PackageStaged x%d, want 1", got)
	}
	// Re-staging at the same lane is idempotent: no second event.
	stagedAt(t, r, "pkg-1029")
	if got := r.published("PackageStaged"); got != 1 {
		t.Fatalf("PackageStaged x%d after re-stage, want 1", got)
	}
}

func TestStagePackageRefusals(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)

	for name, loc := range map[string]string{
		"unknown location":  "STG-NOWHERE",
		"non-staging place": "BIN-999",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.stage.Handle(ctx, "pkg-1029", loc); err == nil {
				t.Fatal("want a refusal")
			}
		})
	}
	// The non-staging location: seed a Storage-role lane first.
	if out, err := r.applyLayout.HandleLocation(ctx, "ev-loc-2", LocationChange{Code: "BIN-999", ZoneID: "zone-1", Role: ""}); err != nil || out != OutcomeApplied {
		t.Fatalf("seed storage lane: %v %v", out, err)
	}
	if _, err := r.stage.Handle(ctx, "pkg-1029", "BIN-999"); err == nil {
		t.Fatal("storage lane must refuse a staging scan")
	}
	// Unknown package.
	if _, err := r.stage.Handle(ctx, "pkg-nope", "STG-D04-A"); err == nil {
		t.Fatal("unknown package must refuse")
	}
}

func TestLoadPackage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)
	stagedAt(t, r, "pkg-1029")
	loadID := r.loadID(ctx, t)

	if _, err := r.load.Handle(ctx, "pkg-1029", loadID, "D04"); err != nil {
		t.Fatal(err)
	}
	p, _ := r.pkgs.Get(ctx, "pkg-1029")
	if p.State() != shippingpackage.StateLoaded {
		t.Fatalf("state=%s want LOADED", p.State())
	}
	if got := r.published("PackageLoaded"); got != 1 {
		t.Fatalf("PackageLoaded x%d, want 1", got)
	}
}

func TestLoadPackageWrongDoorOrLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)
	stagedAt(t, r, "pkg-1029")
	loadID := r.loadID(ctx, t)

	if _, err := r.load.Handle(ctx, "pkg-1029", loadID, "D09"); !isErr(err, shippingpackage.ErrWrongLoad) {
		t.Fatalf("wrong door: %v", err)
	}
	// A package assigned to no load hits the domain's ErrWrongLoad too.
	r.manifest(ctx, t, "ev-man-x", "pkg-free", "order-x")
	// pkg-free joined the SAME open load; unassign by loading through
	// another load's id instead.
	if _, err := r.load.Handle(ctx, "pkg-1029", "load-other", "D04"); !isErr(err, shippingpackage.ErrWrongLoad) {
		t.Fatalf("other load: %v", err)
	}
	if _, err := r.load.Handle(ctx, "pkg-nope", loadID, "D04"); err == nil {
		t.Fatal("unknown package must refuse")
	}
}

func TestUnloadPackage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)
	stagedAt(t, r, "pkg-1029")
	loadID := r.loadID(ctx, t)

	if _, err := r.load.Handle(ctx, "pkg-1029", loadID, "D04"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.unload.Handle(ctx, "pkg-1029", "operator asked"); err != nil {
		t.Fatal(err)
	}
	p, _ := r.pkgs.Get(ctx, "pkg-1029")
	if p.State() != shippingpackage.StateStaged {
		t.Fatalf("state=%s want STAGED", p.State())
	}
	if got := r.published("PackageUnloaded"); got != 1 {
		t.Fatalf("PackageUnloaded x%d, want 1", got)
	}
	// Unloading a STAGED package (never loaded) refuses.
	if _, err := r.unload.Handle(ctx, "pkg-1029", "again"); err == nil {
		t.Fatal("want ErrInvalidTransition")
	}
	if _, err := r.unload.Handle(ctx, "pkg-nope", "x"); err == nil {
		t.Fatal("unknown package must refuse")
	}
}

// isErr reports whether errors.Is(target, want).
func isErr(target error, want error) bool { return target != nil && errorIs(target, want) }
