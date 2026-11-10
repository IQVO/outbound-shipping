package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// ExpectPackage is the PackageManifested policy pair (model.yaml):
// PlanLoadFromCutoff — with no open load for the manifest's
// (site, cpt, operating date, ship method), plan one from the local CPT copy
// — and AssignPackageToLoad — record the package as EXPECTED on that load.
//
// The one-open-load race (two packages of one cutoff manifesting
// simultaneously) has TWO places the loser can learn it lost, and both are
// the same race signal (lostPlanRace):
//
//   - the rival committed between findOrPlan's two READ COMMITTED reads
//     (FindOpenByKey missed, OpenLoadKeys sees the rival), so the domain's
//     Plan refuses with outboundload.ErrDuplicateOpenLoad;
//   - the rival committed before the INSERT, so the partial unique index
//     uq_open_load_per_cutoff refuses with repository.ErrDuplicateOpenLoad.
//
// Either way the whole unit of work — claim included — rolls back and Handle
// retries the ENTIRE application once on a fresh transaction, which
// re-claims the id and finds the winner's committed load. Both packages land
// on the ONE load (proven by the concurrency integration test and, for each
// interleaving deterministically, by expect_package_race_test.go).
type ExpectPackage struct {
	Intake
	Loads    ports.LoadRepo
	Packages ports.PackageRepo
	Copies   ports.CptCopy
	Sink     ports.EventSink
	IDs      ports.LoadIDs
	Clock    ports.Clock
}

// Manifest is one PackageManifested, decoded from the producer's payload.
// SiteID, SourceOrderID, Cutoff and WeightG are the additive fields of ADR
// 0003: the producer omits each when unknown, and the zero values here mean
// "unknown". OrderRef is stored verbatim and never parsed.
type Manifest struct {
	PackageID     shared.PackageID
	SiteID        shared.SiteID
	SourceOrderID string
	OrderRef      string
	Cutoff        time.Time // the manifested cpt instant; zero when unknown
	WeightG       float64
	ManifestedAt  time.Time
}

// Handle applies one manifestation, retrying once when it lost the
// one-open-load race.
func (uc *ExpectPackage) Handle(ctx context.Context, eventID string, m Manifest) (Outcome, error) {
	outcome, err := uc.apply(ctx, FulfillmentConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		return uc.expect(ctx, m)
	})
	if lostPlanRace(err) {
		return uc.apply(ctx, FulfillmentConsumer, eventID, func(ctx context.Context) (Outcome, error) {
			return uc.expect(ctx, m)
		})
	}
	return outcome, err
}

// lostPlanRace reports whether err means a rival committed an open load of
// the same cutoff key while this attempt was planning one: refused by the
// domain (the rival was visible to OpenLoadKeys but not yet to
// FindOpenByKey) or by the partial unique index on insert.
func lostPlanRace(err error) bool {
	return errors.Is(err, outboundload.ErrDuplicateOpenLoad) ||
		errors.Is(err, repository.ErrDuplicateOpenLoad)
}

func (uc *ExpectPackage) expect(ctx context.Context, m Manifest) (Outcome, error) {
	if m.PackageID == "" || m.ManifestedAt.IsZero() {
		return "", invalidf("manifestation needs package id and time")
	}
	// An already-known package is a redelivery the inbox did not catch: its
	// assignment stands (one load at a time, the domain's own rule).
	if _, err := uc.Packages.Get(ctx, m.PackageID); err == nil {
		return OutcomeApplied, nil
	} else if !errors.Is(err, repository.ErrPackageNotFound) {
		return "", err
	}
	key, ok, err := uc.cutoffKey(ctx, m)
	if err != nil {
		return "", err
	}
	if !ok {
		return OutcomeIgnored, nil
	}

	pkg, err := shippingpackage.Expect(shippingpackage.ExpectInput{
		PackageID: m.PackageID, Site: m.SiteID, SourceOrderID: m.SourceOrderID,
		WorkUnitRef: m.OrderRef, WeightG: m.WeightG, At: uc.Clock.Now(),
	})
	if err != nil {
		return "", invalid(err)
	}

	load, planned, err := uc.findOrPlan(ctx, key, m.Cutoff)
	if err != nil {
		return "", err
	}
	if err := pkg.AssignToLoad(load.ID()); err != nil {
		return "", err
	}
	if err := uc.Packages.Save(ctx, pkg, 0); err != nil {
		return "", err
	}
	if planned {
		if err := uc.Sink.PublishLoadEvents(ctx, load.PullEvents()...); err != nil {
			return "", err
		}
	}
	return OutcomeApplied, nil
}

// cutoffKey resolves the manifest's correlation to the load-planning key.
// ok false means the manifest is Ignored: the producer omits site_id today
// (spike Q3) and the cpt instant until its wave-2 change lands -- without
// both there is no cutoff key, no staging rule and no door, so no load can
// be planned (omit-when-unknown, ADR 0003); a site whose schedule copy is
// unknown or whose cutoff the site does not run is ignored the same way.
func (uc *ExpectPackage) cutoffKey(ctx context.Context, m Manifest) (outboundload.LoadKey, bool, error) {
	if m.SiteID == "" || m.Cutoff.IsZero() {
		return outboundload.LoadKey{}, false, nil
	}
	schedule, err := uc.Copies.Get(ctx, m.SiteID)
	if err != nil {
		if errors.Is(err, repository.ErrCptScheduleUnknown) {
			return outboundload.LoadKey{}, false, nil
		}
		return outboundload.LoadKey{}, false, err
	}
	cpt, date, method, err := ResolveCutoff(schedule, m.Cutoff)
	if err != nil {
		if errors.Is(err, repository.ErrCptNotFound) {
			return outboundload.LoadKey{}, false, nil
		}
		return outboundload.LoadKey{}, false, err
	}
	return outboundload.LoadKey{Site: m.SiteID, Cpt: cpt, OperatingDate: date, ShipMethod: method}, true, nil
}

// findOrPlan returns the open load of key, planning it (LoadPlanned) when
// there is none. planned reports whether this call planned it. A raced plan
// fails either in the domain (outboundload.ErrDuplicateOpenLoad: the rival
// committed between the two reads) or on uq_open_load_per_cutoff
// (repository.ErrDuplicateOpenLoad); Handle retries both on a fresh
// transaction (lostPlanRace).
func (uc *ExpectPackage) findOrPlan(ctx context.Context, key outboundload.LoadKey, cutoff time.Time) (load *outboundload.OutboundLoad, planned bool, err error) {
	load, err = uc.Loads.FindOpenByKey(ctx, key)
	if err != nil {
		if !errors.Is(err, repository.ErrLoadNotFound) {
			return nil, false, err
		}
	} else {
		return load, false, nil
	}
	open, err := uc.Loads.OpenLoadKeys(ctx, key.Site)
	if err != nil {
		return nil, false, err
	}
	fresh, events, err := outboundload.Plan(outboundload.PlanInput{
		LoadID: uc.IDs.NewLoadID(), Site: key.Site, Cpt: key.Cpt,
		OperatingDate: key.OperatingDate, ShipMethod: key.ShipMethod,
		Cutoff: cutoff, At: uc.Clock.Now(), OpenLoadKeys: open,
	})
	if err != nil {
		return nil, false, err
	}
	if err := uc.Loads.Save(ctx, fresh, 0); err != nil {
		return nil, false, err
	}
	if err := uc.Sink.PublishLoadEvents(ctx, events...); err != nil {
		return nil, false, err
	}
	return fresh, true, nil
}
