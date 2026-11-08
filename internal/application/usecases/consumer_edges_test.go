package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/outbox"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// The consumer policies share one intake shape: a deterministic payload
// problem refuses with ErrInvalidEvent (dead-letter, never retry), an event
// that is not for this service right now is OutcomeIgnored, and a
// re-delivered CloudEvents id is OutcomeDuplicate. These tables pin those
// edges for every policy the unit prologues do not already reach.

func TestApplyCptScheduleEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.applyCpt.Handle(ctx, "ev-cpt-bad", CptChange{SiteID: ""}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("cpt without a site: %v, want ErrInvalidEvent", err)
	}
	r.seedCpt(ctx, t)
	if out, err := r.applyCpt.Handle(ctx, "ev-cpt-1", CptChange{SiteID: "SITE-1", Timezone: "UTC",
		Cutoffs: []repository.CutoffEntry{{CptID: "cpt-1600", LocalTime: "16:00", ShipMethod: "STDOUT-2DAY"}},
		Version: 1, Changed: tDock}); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered cpt: %v %v", out, err)
	}
}

func TestApplyLayoutEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.applyLayout.HandleZone(ctx, "ev-zone-bad", ZoneChange{ZoneID: ""}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("zone without an id: %v, want ErrInvalidEvent", err)
	}
	if _, err := r.applyLayout.HandleLocation(ctx, "ev-loc-bad", LocationChange{Code: ""}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("location without a code: %v, want ErrInvalidEvent", err)
	}
	r.seedLayout(ctx, t)
	if out, err := r.applyLayout.HandleZone(ctx, "ev-zone-1", ZoneChange{ZoneID: "zone-1", SiteCode: "SITE-1", ZoneCode: "DOCK-D"}); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered zone: %v %v", out, err)
	}
}

func TestBookAppointmentEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	appt := Appointment{
		AppointmentID: "appt-9100", Site: "SITE-1", Direction: "OUTBOUND",
		DoorCode: "D09", CptID: "cpt-1600", Carrier: "STDOUT", CarrierScac: "STDOUT",
		WindowStart: tDock, WindowEnd: tCutoff, Version: 1, BookedAt: tDock,
	}
	bad := appt
	bad.DoorCode = ""
	if _, err := r.book.Handle(ctx, "ev-book-bad", bad); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("appointment without a door: %v, want ErrInvalidEvent", err)
	}
	// An INBOUND appointment only updates the copy.
	inbound := appt
	inbound.Direction = "INBOUND"
	if out, err := r.book.Handle(ctx, "ev-book-in", inbound); err != nil || out != OutcomeIgnored {
		t.Fatalf("inbound appointment: %v %v", out, err)
	}
	// OUTBOUND naming a cpt no load of exists yet: copy saved, ignored.
	if out, err := r.book.Handle(ctx, "ev-book-none", appt); err != nil || out != OutcomeIgnored {
		t.Fatalf("appointment before any load: %v %v", out, err)
	}
	if out, err := r.book.Handle(ctx, "ev-book-none", appt); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered appointment: %v %v", out, err)
	}
}

func TestDockTrailerEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.dock.Handle(ctx, "ev-dock-bad", Docking{TrailerID: "trl-91", Site: "SITE-1"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("docking without a door: %v, want ErrInvalidEvent", err)
	}
	// A docking at a door no open load uses is ignored.
	if out, err := r.dock.Handle(ctx, "ev-dock-none", Docking{
		TrailerID: "trl-40", Site: "SITE-1", DoorCode: "D40", DockedAt: tDock,
	}); err != nil || out != OutcomeIgnored {
		t.Fatalf("docking at an unused door: %v %v", out, err)
	}
	// A re-dock at the already-LOADING load applies (the trailer re-attaches).
	r.openLoad(ctx, t)
	if out, err := r.dock.Handle(ctx, "ev-dock-2", Docking{
		TrailerID: "trl-91", Site: "SITE-1", DoorCode: "D04", DockedAt: tCutoff,
	}); err != nil || out != OutcomeApplied {
		t.Fatalf("re-dock: %v %v", out, err)
	}
	if out, err := r.dock.Handle(ctx, "ev-dock-1", Docking{
		TrailerID: "trl-91", Site: "SITE-1", DoorCode: "D04", DockedAt: tDock,
	}); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered docking: %v %v", out, err)
	}
}

func TestDivertPackageEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.divert.Handle(ctx, "ev-div-bad", "", tDepart); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("diversion without a package: %v, want ErrInvalidEvent", err)
	}
	// A package this service never expected is not ours to drop.
	if out, err := r.divert.Handle(ctx, "ev-div-ghost", "pkg-ghost", tDepart); err != nil || out != OutcomeIgnored {
		t.Fatalf("unknown package diverted: %v %v", out, err)
	}
	// A LOADED package cannot be dropped: ignored.
	loadID := loadedLoad(t, r)
	if out, err := r.divert.Handle(ctx, "ev-div-loaded", "pkg-1029", tDepart); err != nil || out != OutcomeIgnored {
		t.Fatalf("loaded package diverted: %v %v", out, err)
	}
	// A STAGED package drops to SHORT_SHIPPED; the re-delivery is a
	// duplicate.
	r.manifest(ctx, t, "ev-man-div", "pkg-3077", "order-88425")
	stagedAt(t, r, "pkg-3077")
	if out, err := r.divert.Handle(ctx, "ev-div-2", "pkg-3077", tDepart); err != nil || out != OutcomeApplied {
		t.Fatalf("staged package diverted: %v %v", out, err)
	}
	p, err := r.pkgs.Get(ctx, "pkg-3077")
	if err != nil || p.State() != shippingpackage.StateShortShipped {
		t.Fatalf("diverted package state: %v %v", p, err)
	}
	if out, err := r.divert.Handle(ctx, "ev-div-2", "pkg-3077", tDepart); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered diversion: %v %v", out, err)
	}
	_ = loadID
}

func TestExpectPackageEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	// No site and no cutoff instant: no cutoff key, nothing to plan.
	if out, err := r.expect.Handle(ctx, "ev-man-nosite", Manifest{
		PackageID: "pkg-n1", OrderRef: "o-w1", ManifestedAt: tDock,
	}); err != nil || out != OutcomeIgnored {
		t.Fatalf("manifest without site/cutoff: %v %v", out, err)
	}
	// A site whose schedule copy is unknown is ignored the same way.
	if out, err := r.expect.Handle(ctx, "ev-man-noschedule", Manifest{
		PackageID: "pkg-n2", SiteID: "SITE-9", Cutoff: tCutoff, ManifestedAt: tDock,
	}); err != nil || out != OutcomeIgnored {
		t.Fatalf("manifest of an unknown site: %v %v", out, err)
	}
	// A cutoff the site does not run is ignored too.
	if out, err := r.expect.Handle(ctx, "ev-man-nocpt", Manifest{
		PackageID: "pkg-n3", SiteID: "SITE-1", Cutoff: tCutoff.Add(3 * time.Hour), ManifestedAt: tDock,
	}); err != nil || out != OutcomeIgnored {
		t.Fatalf("manifest at an unrun cutoff: %v %v", out, err)
	}
	// A manifest without a package id is a dead letter.
	if _, err := r.expect.Handle(ctx, "ev-man-bad", Manifest{
		SiteID: "SITE-1", Cutoff: tCutoff, ManifestedAt: tDock,
	}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("manifest without a package: %v, want ErrInvalidEvent", err)
	}
	// The same event id re-delivered is a duplicate.
	r.manifest(ctx, t, "ev-man-dup", "pkg-dup", "order-dup")
	if out, err := r.expect.Handle(ctx, "ev-man-dup", Manifest{
		PackageID: "pkg-dup", SiteID: "SITE-1", SourceOrderID: "order-dup",
		Cutoff: tCutoff, ManifestedAt: tDock,
	}); err != nil || out != OutcomeDuplicate {
		t.Fatalf("re-delivered manifest: %v %v", out, err)
	}
}

func TestOnDepartureEdges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	// A trailer this service never attached is not ours to confirm.
	if out, err := r.depart.Handle(ctx, "ev-dep-unknown", Departure{
		TrailerID: "trl-77", Site: "SITE-1", SealNumber: "SL-1", DepartedAt: tDepart,
	}); err != nil || out != OutcomeIgnored {
		t.Fatalf("unknown trailer departed: %v %v", out, err)
	}
	// A departure without a seal on a CLOSED load confirms nothing: the
	// document must certify the seal the yard recorded.
	loadID := loadedLoad(t, r)
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := r.depart.Handle(ctx, "ev-dep-noseal", Departure{
		TrailerID: "trl-91", Site: "SITE-1", DepartedAt: tDepart,
	}); err != nil || out != OutcomeIgnored {
		t.Fatalf("departure without a seal: %v %v", out, err)
	}
	if l, _ := r.loads.Get(ctx, loadID); l.State() != "CLOSED" {
		t.Fatalf("state after an unsealed departure: %s, want CLOSED", l.State())
	}
}

// TestOutboxSendFailureError pins the wording the runbook greps for.
func TestOutboxSendFailureError(t *testing.T) {
	cause := errors.New("broker unreachable")
	f := &outbox.SendFailureError{RowID: 42, Message: outbox.Message{EventType: "t"}, Attempts: 3, Err: cause}
	want := "outbox row 42 (t): broker unreachable"
	if f.Error() != want {
		t.Fatalf("Error() = %q, want %q", f.Error(), want)
	}
	if !errors.Is(f, cause) {
		t.Fatal("Unwrap does not expose the cause")
	}
}

// TestApplyCptScheduleBadEntry pins the per-cutoff-entry validation.
func TestApplyCptScheduleBadEntry(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if _, err := r.applyCpt.Handle(ctx, "ev-cpt-badent", CptChange{
		SiteID: "SITE-1", Timezone: "UTC", Version: 1, Changed: tDock,
		Cutoffs: []repository.CutoffEntry{{CptID: "cpt-x", LocalTime: "16:00"}},
	}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("cpt entry without a ship method: %v, want ErrInvalidEvent", err)
	}
	if _, err := r.applyCpt.Handle(ctx, "ev-cpt-badver", CptChange{SiteID: "SITE-1", Timezone: "UTC"}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("cpt change without a version: %v, want ErrInvalidEvent", err)
	}
}

// TestExpectPackageRedeliveryStands: a package the service already expects,
// re-delivered under a NEW CloudEvents id (the inbox did not catch it), keeps
// its one assignment.
func TestExpectPackageRedeliveryStands(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.openLoad(ctx, t)
	loadID := r.loadID(ctx, t)
	if out, err := r.expect.Handle(ctx, "ev-man-re", Manifest{
		PackageID: "pkg-1029", SiteID: "SITE-1", SourceOrderID: "order-88421",
		Cutoff: tCutoff, ManifestedAt: tDock,
	}); err != nil || out != OutcomeApplied {
		t.Fatalf("redelivered manifest: %v %v", out, err)
	}
	p, err := r.pkgs.Get(ctx, "pkg-1029")
	if err != nil || p.LoadID() != loadID {
		t.Fatalf("re-manifested package: %v %v", p, err)
	}
}

// TestDepartureCountsAnAlreadyShippedPackage is the crash-recovery shape:
// a package marked SHIPPED before the load confirmed (a partial confirm) is
// counted, not re-marked.
func TestDepartureCountsAnAlreadyShippedPackage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	p, err := r.pkgs.Get(ctx, "pkg-1029")
	if err != nil {
		t.Fatal(err)
	}
	version := p.Version()
	if err := p.MarkShipped(tDepart); err != nil {
		t.Fatal(err)
	}
	if err := r.pkgs.Save(ctx, p, version); err != nil {
		t.Fatal(err)
	}
	if _, err := r.close.Handle(ctx, loadID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.depart.Handle(ctx, "ev-dep-crash", Departure{
		TrailerID: "trl-91", Site: "SITE-1", SealNumber: "SL-9001", DepartedAt: tDepart,
	}); err != nil {
		t.Fatal(err)
	}
	l, _ := r.loads.Get(ctx, loadID)
	if l.State() != "SHIP_CONFIRMED" {
		t.Fatalf("state = %s, want SHIP_CONFIRMED", l.State())
	}
	if got := r.published("ShipmentConfirmed"); got != 1 {
		t.Fatalf("ShipmentConfirmed x%d, want 1", got)
	}
}

// TestStagePullsTheHeaderSite pins the event Header accessor the projector
// family uses.
func TestStagePullsTheHeaderSite(t *testing.T) {
	p, err := shippingpackage.Expect(shippingpackage.ExpectInput{
		PackageID: "pkg-hdr", Site: "SITE-1", At: tDock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AssignToLoad("load-hdr"); err != nil {
		t.Fatal(err)
	}
	if err := p.Stage(shippingpackage.StageInput{
		Location: "STG-D04-A", IsStaging: true, LocationSite: "SITE-1", At: tDock,
	}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range p.PullEvents() {
		if ev.SiteID() != "SITE-1" {
			t.Fatalf("%s site = %q, want SITE-1", ev.EventName(), ev.SiteID())
		}
	}
}

// TestListLoadPackagesPagination pins the default limit and the full-page
// cursor of the package listing.
func TestListLoadPackagesPagination(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	loadID := loadedLoad(t, r)
	// limit 0 falls back to the default; a full page (1 of 1) yields the
	// cursor, and the page after it is empty.
	page, next, err := r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, "", 1)
	if err != nil || len(page) != 1 || next != "pkg-1029" {
		t.Fatalf("packages page 1: %d %q %v", len(page), next, err)
	}
	page2, next2, err := r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, next, 1)
	if err != nil || len(page2) != 0 || next2 != "" {
		t.Fatalf("packages page 2: %d %q %v", len(page2), next2, err)
	}
	if page, _, err := r.queries.ListLoadPackages(ctx, loadID, repository.PackageFilter{}, "", 0); err != nil || len(page) != 1 {
		t.Fatalf("packages default limit: %d %v", len(page), err)
	}
}
