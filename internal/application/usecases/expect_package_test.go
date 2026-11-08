package usecases

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

func TestExpectPackagePlansLoadAndAssigns(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)

	if out := r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421"); out != OutcomeApplied {
		t.Fatalf("out=%v want applied", out)
	}
	if got := r.published("LoadPlanned"); got != 1 {
		t.Fatalf("LoadPlanned x%d, want 1", got)
	}
	p, err := r.pkgs.Get(ctx, "pkg-1029")
	if err != nil {
		t.Fatal(err)
	}
	if p.LoadID() == "" {
		t.Fatal("package assigned to no load")
	}
	l, err := r.loads.Get(ctx, p.LoadID())
	if err != nil {
		t.Fatal(err)
	}
	if l.State() != outboundload.StatePlanned || l.CptID() != "cpt-1600" || l.ShipMethod() != "STDOUT-2DAY" {
		t.Fatalf("planned load = %s %s %s", l.State(), l.CptID(), l.ShipMethod())
	}
}

func TestExpectPackageSecondManifestJoinsSameLoad(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421")
	r.manifest(ctx, t, "ev-man-2", "pkg-2055", "order-88423")
	if got := r.published("LoadPlanned"); got != 1 {
		t.Fatalf("LoadPlanned x%d, want 1 (one open load per cutoff)", got)
	}
	a, _ := r.pkgs.Get(ctx, "pkg-1029")
	b, _ := r.pkgs.Get(ctx, "pkg-2055")
	if a.LoadID() != b.LoadID() {
		t.Fatalf("packages on different loads: %s vs %s", a.LoadID(), b.LoadID())
	}
}

func TestExpectPackageRedelivery(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421")

	// Same CloudEvents id: the inbox claim answers duplicate.
	if out := r.manifest(ctx, t, "ev-man-1", "pkg-1029", "order-88421"); out != OutcomeDuplicate {
		t.Fatalf("same id: out=%v want duplicate", out)
	}
	// A redelivery the inbox missed (new id): the assignment stands.
	if out := r.manifest(ctx, t, "ev-man-1b", "pkg-1029", "order-88421"); out != OutcomeApplied {
		t.Fatalf("known package: out=%v want applied", out)
	}
	if got := r.published("LoadPlanned"); got != 1 {
		t.Fatalf("LoadPlanned x%d, want 1", got)
	}
}

func TestExpectPackageIgnoredWithoutCorrelation(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	cases := []struct {
		name string
		m    Manifest
	}{
		{"no site", Manifest{PackageID: "pkg-1", ManifestedAt: tDock, Cutoff: tCutoff}},
		{"no cutoff", Manifest{PackageID: "pkg-1", ManifestedAt: tDock, SiteID: "SITE-1"}},
		{"unknown schedule", Manifest{PackageID: "pkg-1", ManifestedAt: tDock, SiteID: "SITE-2", Cutoff: tCutoff}},
		{"cutoff the site does not run", Manifest{PackageID: "pkg-1", ManifestedAt: tDock, SiteID: "SITE-1", Cutoff: tCutoff.Add(time.Hour)}},
		{"cutoff on an unlisted weekday", Manifest{PackageID: "pkg-1", ManifestedAt: tDock, SiteID: "SITE-1", Cutoff: tCutoff}},
	}
	// The weekday row needs its own schedule: Fridays only, fixture is one.
	if out, err := r.applyCpt.Handle(ctx, "ev-cpt-fri", CptChange{
		SiteID: "SITE-FRI", Timezone: "UTC",
		Cutoffs: []repository.CutoffEntry{{CptID: "cpt-1600", LocalTime: "16:00", DaysOfWeek: []string{"Mon"}, ShipMethod: "STDOUT-2DAY"}},
		Version: 1, Changed: tDock,
	}); err != nil || out != OutcomeApplied {
		t.Fatalf("seed friday schedule: %v %v", out, err)
	}
	cases[4].m.SiteID = "SITE-FRI"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := r.expect.Handle(ctx, "ev-"+tc.name, tc.m)
			if err != nil {
				t.Fatalf("err=%v want ignored", err)
			}
			if out != OutcomeIgnored {
				t.Fatalf("out=%v want ignored", out)
			}
		})
	}
	if got := r.published("LoadPlanned"); got != 0 {
		t.Fatalf("LoadPlanned x%d, want 0", got)
	}
}

func TestExpectPackageInvalid(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.seedCpt(ctx, t)
	for name, m := range map[string]Manifest{
		"no package id":  {SiteID: "SITE-1", Cutoff: tCutoff, ManifestedAt: tDock},
		"no manifested":  {PackageID: "pkg-1", SiteID: "SITE-1", Cutoff: tCutoff},
		"no event id":    {PackageID: "pkg-1", SiteID: "SITE-1", Cutoff: tCutoff, ManifestedAt: tDock},
		"bad weight pkg": {PackageID: "pkg-bad", SiteID: "SITE-1", Cutoff: tCutoff, ManifestedAt: tDock, WeightG: -1},
	} {
		t.Run(name, func(t *testing.T) {
			eventID := "ev-" + name
			if name == "no event id" {
				eventID = ""
			}
			_, err := r.expect.Handle(ctx, eventID, m)
			if err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestResolveCutoffTimezoneAndDay(t *testing.T) {
	s := repository.CptSchedule{Timezone: "UTC", Cutoffs: []repository.CutoffEntry{
		{CptID: "cpt-1600", LocalTime: "16:00", ShipMethod: "STDOUT-2DAY"},
		{CptID: "cpt-1230", LocalTime: "12:30", DaysOfWeek: []string{"Sat", "Sun"}, ShipMethod: "STDOUT-1DAY"},
	}}
	cpt, date, method, err := ResolveCutoff(s, tCutoff) // a Friday
	if err != nil || cpt != "cpt-1600" || date != "2026-11-06" || method != "STDOUT-2DAY" {
		t.Fatalf("got %s %s %s %v", cpt, date, method, err)
	}
	// Sunday 12:30 local.
	sun := time.Date(2026, 11, 8, 12, 30, 0, 0, time.UTC)
	cpt, _, _, err = ResolveCutoff(s, sun)
	if err != nil || cpt != "cpt-1230" {
		t.Fatalf("sunday: %s %v", cpt, err)
	}
	// Saturday 16:00: the daily entry matches, the weekend one does not.
	sat := time.Date(2026, 11, 7, 16, 0, 0, 0, time.UTC)
	if _, _, _, err = ResolveCutoff(s, sat); err != nil {
		t.Fatalf("saturday daily: %v", err)
	}
	// Sunday 16:00 runs too (daily entry).
	if _, _, _, err = ResolveCutoff(s, sun.Add(4*time.Hour-30*time.Minute)); err != nil {
		t.Fatalf("sunday daily: %v", err)
	}
	// No match.
	_, _, _, err = ResolveCutoff(s, tCutoff.Add(30*time.Minute))
	if !errors.Is(err, repository.ErrCptNotFound) {
		t.Fatalf("want ErrCptNotFound, got %v", err)
	}
	// Bad timezone.
	if _, _, _, err = ResolveCutoff(repository.CptSchedule{Timezone: "No/Such"}, tCutoff); err == nil {
		t.Fatal("want timezone error")
	}
	// Zero instant.
	if _, _, _, err = ResolveCutoff(s, time.Time{}); err == nil {
		t.Fatal("want zero-instant error")
	}
	_ = shared.CptID("")
}
