package usecases

import (
	"context"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// BoardStoreWriter is the projector's write side over the LoadBoardView
// tables. It is a separate port from the reader (ports.BoardStore) so the
// projector binary wires only what it needs.
type BoardStoreWriter interface {
	// ApplyLoadPlanned upserts the load's board row (LoadPlanned).
	ApplyLoadPlanned(ctx context.Context, load shared.LoadID, site shared.SiteID, cpt shared.CptID, date, method string, cutoff time.Time) error
	// ApplyCount bumps the load's staged or loaded package count
	// (PackageStaged / PackageLoaded).
	ApplyCount(ctx context.Context, load shared.LoadID, staged, loaded int64) error
	// ApplyMissed bumps the load's missed-package count (PackageMissedLoad).
	ApplyMissed(ctx context.Context, load shared.LoadID, miss int64) error
	// ApplyClosed stamps the close and its on-time verdict (LoadClosed).
	ApplyClosed(ctx context.Context, load shared.LoadID, closedAt, cutoff time.Time) error
	// ApplyConfirmed stamps the ship confirmation (ShipmentConfirmed).
	ApplyConfirmed(ctx context.Context, load shared.LoadID, at time.Time) error
	// ApplyCancelled stamps the cancellation (LoadCancelled).
	ApplyCancelled(ctx context.Context, load shared.LoadID, at time.Time) error
}

// ProjectBoard folds THIS service's own events (the analytics twin of
// warehouse.outbound-shipping.events) into the LoadBoardView projection:
// one row per load with the counts behind GET /reports/load-adherence and
// the board's expected/staged/loaded funnel.
type ProjectBoard struct {
	Intake
	Board BoardStoreWriter
}

// Handle applies one projected event by name. Unknown names are ignored
// (the projector consumes every type of the stream).
func (uc *ProjectBoard) Handle(ctx context.Context, eventID string, fold BoardFold) (Outcome, error) {
	return uc.apply(ctx, ProjectorConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		switch fold.Name {
		case "LoadPlanned":
			// The board's cutoff is the LOAD's cutoff (the LoadPlanned
			// payload carries it; loads_at_risk and the on-time verdict
			// read it), never the event's occurrence time — found by
			// cmd/mcp's integration test.
			return appliedOrIgnore(uc.Board.ApplyLoadPlanned(ctx, fold.Load, fold.Site, fold.Cpt, fold.Date, fold.Method, fold.Cutoff))
		case "PackageStaged":
			return appliedOrIgnore(uc.Board.ApplyCount(ctx, fold.Load, 1, 0))
		case "PackageLoaded":
			return appliedOrIgnore(uc.Board.ApplyCount(ctx, fold.Load, 0, 1))
		case "PackageMissedLoad":
			return appliedOrIgnore(uc.Board.ApplyMissed(ctx, fold.Load, 1))
		case "LoadClosed":
			return appliedOrIgnore(uc.Board.ApplyClosed(ctx, fold.Load, fold.At, fold.Cutoff))
		case "ShipmentConfirmed":
			return appliedOrIgnore(uc.Board.ApplyConfirmed(ctx, fold.Load, fold.At))
		case "LoadCancelled":
			return appliedOrIgnore(uc.Board.ApplyCancelled(ctx, fold.Load, fold.At))
		default:
			return OutcomeIgnored, nil
		}
	})
}

// BoardFold is one decoded own-event occurrence, projected: the event name,
// the load it is about, and the facts the fold needs.
type BoardFold struct {
	Name   string
	Load   shared.LoadID
	Site   shared.SiteID
	Cpt    shared.CptID
	Date   string
	Method string
	Cutoff time.Time
	At     time.Time
}

// appliedOrIgnore maps a nil store error to OutcomeApplied.
func appliedOrIgnore(err error) (Outcome, error) {
	if err != nil {
		return "", err
	}
	return OutcomeApplied, nil
}
