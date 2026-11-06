package usecases

import (
	"context"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// CloseLoad is the supervisor's close (POST /loads/{loadId}/closures):
// LOADING -> CLOSED with the short-ships of the command recorded, once every
// assigned package is loaded or short-shipped (ErrPackagesOutstanding).
// Each short-shipped package is marked SHORT_SHIPPED here (its reason is the
// operator's, DIVERTED only when the diversion policy got there first — in
// which case the package is already SHORT_SHIPPED and only the load's record
// gains the entry).
type CloseLoad struct {
	UoW   ports.UnitOfWork
	Loads ports.LoadRepo
	Pkgs  ports.PackageRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle closes the load, recording shortShips.
func (uc *CloseLoad) Handle(ctx context.Context, loadID shared.LoadID, shortShips []shared.ShortShip) (*outboundload.OutboundLoad, error) {
	var load *outboundload.OutboundLoad
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		l, err := uc.Loads.Get(ctx, loadID)
		if err != nil {
			return err
		}
		pkgs, err := uc.loadAll(ctx, loadID)
		if err != nil {
			return err
		}
		short, outstanding, loaded, err := uc.applyShortShips(ctx, pkgs, shortShips)
		if err != nil {
			return err
		}
		if err := l.CloseLoad(outboundload.CloseInput{
			OutstandingPackages: outstanding, ShortShips: short,
			LoadedCount: loaded, At: uc.Clock.Now(),
		}); err != nil {
			return err
		}
		if err := uc.Loads.Save(ctx, l, l.Version()-1); err != nil {
			return err
		}
		if err := uc.Sink.PublishLoadEvents(ctx, l.PullEvents()...); err != nil {
			return err
		}
		load = l
		return nil
	})
	if err != nil {
		return nil, err
	}
	return load, nil
}

// applyShortShips marks the commanded short-ships on their packages (a
// package already SHORT_SHIPPED by the DropDiverted policy keeps its state)
// and reports the close's inputs: the full short-ship record (commanded plus
// already-diverted), the packages still outstanding and the loaded count.
func (uc *CloseLoad) applyShortShips(ctx context.Context, pkgs []*shippingpackage.ShippingPackage, shortShips []shared.ShortShip) ([]shared.ShortShip, []shared.PackageID, int, error) {
	byID := make(map[shared.PackageID]*shippingpackage.ShippingPackage, len(pkgs))
	for _, p := range pkgs {
		byID[p.ID()] = p
	}
	short := make([]shared.ShortShip, 0, len(shortShips)+len(pkgs))
	short = append(short, shortShips...)
	var outstanding []shared.PackageID
	loaded := 0
	for _, p := range pkgs {
		switch p.State() {
		case shippingpackage.StateLoaded, shippingpackage.StateShipped:
			loaded++
		case shippingpackage.StateShortShipped:
			// Already short-shipped by the DropDiverted policy: record
			// it on the load with the same reason.
			short = append(short, shared.ShortShip{PackageID: p.ID(), Reason: "DIVERTED"})
		default:
			outstanding = append(outstanding, p.ID())
		}
	}
	for _, s := range shortShips {
		p, ok := byID[s.PackageID]
		if !ok {
			return nil, nil, 0, repository.ErrPackageNotFound
		}
		if p.State() == shippingpackage.StateShortShipped {
			continue
		}
		if err := p.MarkShortShipped(s.Reason, uc.Clock.Now()); err != nil {
			return nil, nil, 0, err
		}
		if err := uc.Pkgs.Save(ctx, p, p.Version()-1); err != nil {
			return nil, nil, 0, err
		}
	}
	return short, outstanding, loaded, nil
}

// loadAll pages through every package of the load.
func (uc *CloseLoad) loadAll(ctx context.Context, loadID shared.LoadID) ([]*shippingpackage.ShippingPackage, error) {
	var out []*shippingpackage.ShippingPackage
	after := shared.PackageID("")
	for {
		page, err := uc.Pkgs.ListByLoad(ctx, loadID, repository.PackageFilter{}, after, 200)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		out = append(out, page...)
		after = page[len(page)-1].ID()
		if len(page) < 200 {
			return out, nil
		}
	}
}

// ReopenLoad is the supervisor's reopen (POST /loads/{loadId}/reopenings):
// CLOSED -> LOADING, LoadReopened published so yard-management reopens the
// door work (wave 6.4).
type ReopenLoad struct {
	UoW   ports.UnitOfWork
	Loads ports.LoadRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle reopens the load.
func (uc *ReopenLoad) Handle(ctx context.Context, loadID shared.LoadID) (*outboundload.OutboundLoad, error) {
	var load *outboundload.OutboundLoad
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		l, err := uc.Loads.Get(ctx, loadID)
		if err != nil {
			return err
		}
		if err := l.ReopenLoad(uc.Clock.Now()); err != nil {
			return err
		}
		if err := uc.Loads.Save(ctx, l, l.Version()-1); err != nil {
			return err
		}
		if err := uc.Sink.PublishLoadEvents(ctx, l.PullEvents()...); err != nil {
			return err
		}
		load = l
		return nil
	})
	if err != nil {
		return nil, err
	}
	return load, nil
}

// CancelLoad is the supervisor's cancel (POST /loads/{loadId}/cancellations):
// PLANNED -> CANCELLED. A load with packages assigned refuses
// ErrLoadHasPackages: cancelling would strand them on a dead load (the model
// leaves re-planning unmodelled); short-ship and close instead.
type CancelLoad struct {
	UoW   ports.UnitOfWork
	Loads ports.LoadRepo
	Pkgs  ports.PackageRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle cancels the load.
func (uc *CancelLoad) Handle(ctx context.Context, loadID shared.LoadID, reason string) (*outboundload.OutboundLoad, error) {
	var load *outboundload.OutboundLoad
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		l, err := uc.Loads.Get(ctx, loadID)
		if err != nil {
			return err
		}
		pkgs, err := uc.Pkgs.ListByLoad(ctx, loadID, repository.PackageFilter{}, "", 1)
		if err != nil {
			return err
		}
		if len(pkgs) > 0 {
			return ErrLoadHasPackages
		}
		if err := l.CancelLoad(reason, uc.Clock.Now()); err != nil {
			return err
		}
		if err := uc.Loads.Save(ctx, l, l.Version()-1); err != nil {
			return err
		}
		if err := uc.Sink.PublishLoadEvents(ctx, l.PullEvents()...); err != nil {
			return err
		}
		load = l
		return nil
	})
	if err != nil {
		return nil, err
	}
	return load, nil
}

// VoidBillOfLading is the supervisor's void
// (POST /bills-of-lading/{bolNumber}/voids): ISSUED -> VOIDED with a reason.
// The shipment itself is not undone.
type VoidBillOfLading struct {
	UoW   ports.UnitOfWork
	Bols  ports.BolRepo
	Sink  ports.EventSink
	Clock ports.Clock
}

// Handle voids the document.
func (uc *VoidBillOfLading) Handle(ctx context.Context, bolNumber shared.BolNumber, reason string) (*billoflading.BillOfLading, error) {
	var bol *billoflading.BillOfLading
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		b, err := uc.Bols.Get(ctx, bolNumber)
		if err != nil {
			return err
		}
		if err := b.Void(reason, uc.Clock.Now()); err != nil {
			return err
		}
		if err := uc.Bols.Save(ctx, b, b.Version()-1); err != nil {
			return err
		}
		if err := uc.Sink.PublishBolEvents(ctx, b.PullEvents()...); err != nil {
			return err
		}
		bol = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return bol, nil
}
