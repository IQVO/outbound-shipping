package usecases

import (
	"context"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// SweepMissedLoads is the cron sweep (POST /missed-load-sweeps,
// SweepMissedLoad policy): every EXPECTED or STAGED package of the site
// whose still-open load's cutoff has passed raises PackageMissedLoad
// exactly once — the flagged fact is remembered on the package, so a re-run
// sweep flags nothing (the once-guard of the BDD scenario).
type SweepMissedLoads struct {
	UoW   ports.UnitOfWork
	Pkgs  ports.PackageRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle sweeps the site at (or, by default, now) and returns how many
// packages THIS run newly flagged.
func (uc *SweepMissedLoads) Handle(ctx context.Context, site shared.SiteID, cutoff time.Time) (int, time.Time, error) {
	at := uc.Clock.Now()
	if !cutoff.IsZero() {
		at = cutoff
	}
	flagged := 0
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		due, err := uc.Pkgs.ListSweepable(ctx, site, at)
		if err != nil {
			return err
		}
		for _, s := range due {
			p := s.Package
			already := p.MissedFlagged()
			loaded := p.Version() // a re-flag bumps nothing (the once-guard)
			if err := p.FlagMissedLoad(s.Cutoff, at); err != nil {
				// LOADED/SHIPPED are not sweepable; a package with no load
				// cannot miss one. Defensive skip.
				continue
			}
			if already {
				// The once-guard: a re-run sweep publishes nothing new.
				continue
			}
			if err := uc.Pkgs.Save(ctx, p, loaded); err != nil {
				return err
			}
			if err := uc.Sink.PublishPackageEvents(ctx, p.PullEvents()...); err != nil {
				return err
			}
			flagged++
		}
		return nil
	})
	if err != nil {
		return 0, at, err
	}
	return flagged, at, nil
}

// RecordDoorWork is the DoorWorkCompleted effect (yard fixture contract):
// the completion is copied for the load-adherence read model; no state
// changes in v1 (a CLOSED load's door work completes on LoadClosed, wave
// 6.4).
type RecordDoorWork struct {
	Intake
	Copies ports.DoorWorkCopy
}

// Handle records one completion.
func (uc *RecordDoorWork) Handle(ctx context.Context, eventID string, c repository.DoorWorkCompletion) (Outcome, error) {
	return uc.apply(ctx, YardConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if c.TrailerID == "" || c.Site == "" || c.DoorCode == "" || c.CompletedAt.IsZero() {
			return "", invalidf("door work completion needs trailer, site, door and time")
		}
		if err := uc.Copies.Record(ctx, c); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}
