package usecases

import (
	"context"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

func departed(t *testing.T, r *rig, eventID, seal string) Outcome {
	t.Helper()
	out, err := r.depart.Handle(context.Background(), eventID, Departure{
		TrailerID: "trl-91", Site: "SITE-1", SealNumber: seal, DepartedAt: tDepart,
	})
	if err != nil {
		t.Fatalf("depart %s: %v", eventID, err)
	}
	return out
}

func TestOnDepartureConfirmsClosedLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}

	if out := departed(t, r, "ev-dep-1", "SL-4471"); out != OutcomeApplied {
		t.Fatalf("out=%v want applied", out)
	}
	if got := r.published("ShipmentConfirmed"); got != 1 {
		t.Fatalf("ShipmentConfirmed x%d, want 1", got)
	}
	if got := r.published("BillOfLadingIssued"); got != 1 {
		t.Fatalf("BillOfLadingIssued x%d, want 1", got)
	}
	p, _ := r.pkgs.Get(ctx, "pkg-1029")
	if p.State() != shippingpackage.StateShipped {
		t.Fatalf("package=%s want SHIPPED", p.State())
	}
	l, _ := r.loads.Get(ctx, loadID)
	if l.State() != "SHIP_CONFIRMED" {
		t.Fatalf("load=%s want SHIP_CONFIRMED", l.State())
	}
	if _, err := r.bols.GetByLoad(ctx, loadID); err != nil {
		t.Fatalf("bol by load: %v", err)
	}
}

func TestOnDepartureWithoutSealConfirmsNothing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if out := departed(t, r, "ev-dep-noseal", ""); out != OutcomeIgnored {
		t.Fatalf("out=%v want ignored", out)
	}
	if got := r.published("ShipmentConfirmed"); got != 0 {
		t.Fatalf("ShipmentConfirmed x%d, want 0", got)
	}
	l, _ := r.loads.Get(ctx, loadID)
	if l.State() != "CLOSED" {
		t.Fatalf("load=%s want CLOSED (waiting for a sealed departure)", l.State())
	}
}

func TestOnDepartureWhileLoadingFlagsAndNeverConfirms(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadedLoad(t, r)
	// One more package staged but not loaded.
	r.manifest(ctx, t, "ev-man-2", "pkg-2055", "order-88423")
	stagedAt(t, r, "pkg-2055")

	if out := departed(t, r, "ev-dep-1", "SL-4471"); out != OutcomeApplied {
		t.Fatalf("out=%v want applied (the flag ran)", out)
	}
	if got := r.published("ShipmentConfirmed"); got != 0 {
		t.Fatalf("ShipmentConfirmed x%d, want 0", got)
	}
	if got := r.published("BillOfLadingIssued"); got != 0 {
		t.Fatalf("BillOfLadingIssued x%d, want 0", got)
	}
	if got := r.published("PackageMissedLoad"); got != 1 {
		t.Fatalf("PackageMissedLoad x%d, want 1 (pkg-2055)", got)
	}
	// The loaded package made it: never flagged.
	p, _ := r.pkgs.Get(ctx, "pkg-1029")
	if p.MissedFlagged() {
		t.Fatal("the loaded package was flagged")
	}
	// A second departure (the yard re-records) changes nothing: the
	// once-guard holds and the load stays LOADING for the supervisor.
	departed(t, r, "ev-dep-2", "SL-4471")
	if got := r.published("PackageMissedLoad"); got != 1 {
		t.Fatalf("PackageMissedLoad x%d after re-delivery, want 1", got)
	}
}

func TestOnDepartureAfterConfirmIsIgnored(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	departed(t, r, "ev-dep-1", "SL-4471")
	// Same id (the inbox claim answers duplicate) and a distinct id (the
	// SHIP_CONFIRMED state is the backstop).
	if out := departed(t, r, "ev-dep-1", "SL-4471"); out != OutcomeDuplicate {
		t.Fatalf("same id: out=%v want duplicate", out)
	}
	if out := departed(t, r, "ev-dep-other", "SL-9999"); out != OutcomeIgnored {
		t.Fatalf("distinct id: out=%v want ignored", out)
	}
	if got := r.published("ShipmentConfirmed"); got != 1 {
		t.Fatalf("ShipmentConfirmed x%d, want 1", got)
	}
	if got := r.published("BillOfLadingIssued"); got != 1 {
		t.Fatalf("BillOfLadingIssued x%d, want 1", got)
	}
}

func TestOnDepartureUnknownTrailerIgnored(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)
	out, err := r.depart.Handle(ctx, "ev-dep-x", Departure{TrailerID: "trl-nope", Site: "SITE-1", SealNumber: "SL-1", DepartedAt: tDepart})
	if err != nil || out != OutcomeIgnored {
		t.Fatalf("out=%v err=%v want ignored", out, err)
	}
	// Invalid payload: dead-letter material, not ignored.
	if _, err := r.depart.Handle(ctx, "ev-dep-bad", Departure{Site: "SITE-1"}); err == nil {
		t.Fatal("want an invalid-event error")
	}
}

func TestOnDepartureConfirmNeedsTheAppointmentCarrier(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	// Break the carrier copy: the appointment the load names is unknown to
	// the copy store (simulating a copy lag): the BOL refuses an empty
	// carrier and the confirm surfaces the refusal.
	r.appts = memoryAppointmentsEmpty()
	r.depart.Copies = r.appts
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := departedErr(r, "ev-dep-1", "SL-4471"); err == nil {
		t.Fatal("want the BOL's carrier refusal to surface")
	}
	if got := r.published("ShipmentConfirmed"); got != 0 {
		t.Fatalf("ShipmentConfirmed x%d, want 0 (rolled back)", got)
	}
}

// memoryAppointmentsEmpty is a fresh, empty appointment copy store.
func memoryAppointmentsEmpty() *memory.Appointments { return memory.NewAppointments() }

// departedErr departs trl-91 and returns the raw error.
func departedErr(r *rig, eventID, seal string) (Outcome, error) {
	return r.depart.Handle(context.Background(), eventID, Departure{
		TrailerID: "trl-91", Site: "SITE-1", SealNumber: seal, DepartedAt: tDepart,
	})
}
