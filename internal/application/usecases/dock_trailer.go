package usecases

import (
	"context"

	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// DockTrailer is the StartOnDock policy (model.yaml): on TrailerDocked at
// the load's door, attach the trailer to the load — and, when the load is
// still PLANNED, START LOADING (the model's only StartLoading trigger: no
// REST route exists for it). A docking at a door no open load uses, or a
// trailer docking again at the same load, is a no-op for the domain state
// (AttachTrailer tolerates LOADING; StartLoading's refusal on an
// already-LOADING load is swallowed).
type DockTrailer struct {
	Intake
	Loads ports.LoadRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Docking is one TrailerDocked (yard fixture contract), decoded from the
// producer's payload.
type Docking struct {
	TrailerID shared.TrailerID
	Site      shared.SiteID
	DoorCode  shared.DoorCode
	DockedAt  time.Time
}

// Handle applies one docking.
func (uc *DockTrailer) Handle(ctx context.Context, eventID string, d Docking) (Outcome, error) {
	return uc.apply(ctx, YardConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if d.TrailerID == "" || d.Site == "" || d.DoorCode == "" {
			return "", invalidf("docking needs trailer id, site and door")
		}
		at := atOr(d.DockedAt, uc.Clock.Now())
		loads, err := uc.Loads.ListOpenByDoor(ctx, d.Site, d.DoorCode)
		if err != nil {
			return "", err
		}
		if len(loads) == 0 {
			// A docking at a door no open load uses: nothing to attach to.
			return OutcomeIgnored, nil
		}
		load := loads[0] // PLANNED first (repo order)
		if err := load.AttachTrailer(outboundload.TrailerDocked{
			TrailerID: d.TrailerID, DoorCode: d.DoorCode, At: at,
		}); err != nil {
			return "", err
		}
		started := false
		if load.State() == outboundload.StatePlanned {
			if err := load.StartLoading(at); err != nil {
				return "", err
			}
			started = true
		}
		if err := uc.Loads.Save(ctx, load, load.Version()-1); err != nil {
			return "", err
		}
		if started {
			if err := uc.Sink.PublishLoadEvents(ctx, load.PullEvents()...); err != nil {
				return "", err
			}
		}
		return OutcomeApplied, nil
	})
}
