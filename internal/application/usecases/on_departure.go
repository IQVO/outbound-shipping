package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// OnDeparture applies TrailerDeparted (ADR 0002: ship-confirm follows the
// physical departure), splitting on the load's state:
//
//   - CLOSED: ConfirmOnDeparture — ConfirmShipment (ShipmentConfirmed with
//     the departure's seal and the distinct source orders of the shipped
//     packages), mark every LOADED package SHIPPED, and IssueBillOfLading.
//   - LOADING (or PLANNED with the trailer attached — a departure that
//     arrived BEFORE LoadClosed, the out-of-order case): FlagDepartureWithoutClose
//     — the load is NEVER confirmed; every package not loaded raises
//     PackageMissedLoad once and the load is left for a supervisor.
//   - SHIP_CONFIRMED: a re-delivered departure confirms nothing twice (the
//     inbox claim catches same-id redeliveries; the state is the backstop
//     for distinct ids).
//
// Idempotency is three layers deep: the inbox claim (same CloudEvents id),
// the SHIP_CONFIRMED refusal (any later departure), and the BOL's
// exactly-once-per-load invariant.
type OnDeparture struct {
	Intake
	Loads    ports.LoadRepo
	Packages ports.PackageRepo
	Bols     ports.BolRepo
	Copies   ports.AppointmentCopy
	Sink     ports.EventSink
	IDs      ports.LoadIDs
	Clock    ports.Clock
}

// Departure is one TrailerDeparted (yard fixture contract), decoded from the
// producer's payload. SealNumber empty means the yard recorded none.
type Departure struct {
	TrailerID  shared.TrailerID
	Site       shared.SiteID
	SealNumber string
	DepartedAt time.Time
}

// Handle applies one departure.
func (uc *OnDeparture) Handle(ctx context.Context, eventID string, d Departure) (Outcome, error) {
	return uc.apply(ctx, YardConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if d.TrailerID == "" || d.Site == "" {
			return "", invalidf("departure needs trailer id and site")
		}
		at := atOr(d.DepartedAt, uc.Clock.Now())
		load, err := uc.Loads.FindLatestByTrailer(ctx, d.Site, d.TrailerID)
		if err != nil {
			if errors.Is(err, repository.ErrLoadNotFound) {
				// A trailer this service never attached: not ours to confirm.
				return OutcomeIgnored, nil
			}
			return "", err
		}
		switch load.State() {
		case outboundload.StateShipConfirmed:
			return OutcomeIgnored, nil
		case outboundload.StateClosed:
			return uc.confirm(ctx, load, d, at)
		case outboundload.StateLoading, outboundload.StatePlanned:
			return uc.flagWithoutClose(ctx, load, at)
		default: // CANCELLED
			return OutcomeIgnored, nil
		}
	})
}

// confirm runs ConfirmOnDeparture for a CLOSED load: shipment confirmed,
// packages SHIPPED, bill of lading issued with the departure's seal. A
// departure without a seal confirms NOTHING (ErrSealMissing is the domain's
// own refusal): the load stays CLOSED until a departure carrying a seal
// arrives — the document must certify the seal the yard recorded.
func (uc *OnDeparture) confirm(ctx context.Context, load *outboundload.OutboundLoad, d Departure, at time.Time) (Outcome, error) {
	if d.SealNumber == "" {
		return OutcomeIgnored, nil
	}
	pkgs, err := uc.loadPackages(ctx, load.ID())
	if err != nil {
		return "", err
	}
	orders := make([]string, 0, len(pkgs))
	weight := 0.0
	shipped := 0
	for _, p := range pkgs {
		switch p.State() {
		case shippingpackage.StateLoaded:
			if err := p.MarkShipped(at); err != nil {
				return "", err
			}
			if err := uc.Packages.Save(ctx, p, p.Version()-1); err != nil {
				return "", err
			}
			shipped++
			weight += p.WeightG()
			if p.SourceOrderID() != "" {
				orders = append(orders, p.SourceOrderID())
			}
		case shippingpackage.StateShipped:
			// A partial crash between the package saves: count it.
			shipped++
			weight += p.WeightG()
			if p.SourceOrderID() != "" {
				orders = append(orders, p.SourceOrderID())
			}
		}
	}
	carrier, scac := uc.carrierOf(ctx, load)
	if err := load.ConfirmShipment(outboundload.ConfirmInput{
		Departure: outboundload.Departure{
			TrailerID: d.TrailerID, SealNumber: d.SealNumber, DepartedAt: at,
		},
		Carrier: carrier, Orders: orders, At: at,
	}); err != nil {
		return "", err
	}
	if err := uc.Loads.Save(ctx, load, load.Version()-1); err != nil {
		return "", err
	}
	if err := uc.Sink.PublishLoadEvents(ctx, load.PullEvents()...); err != nil {
		return "", err
	}
	bol, events, err := billoflading.Issue(billoflading.IssueInput{
		BolNumber: uc.IDs.NewBolNumber(load.OperatingDate()), LoadID: load.ID(),
		Site: load.SiteID(), LoadConfirmed: true, Carrier: carrier,
		CarrierScac: scac, TrailerID: d.TrailerID, SealNumber: d.SealNumber,
		PackageCount: shipped, TotalWeightG: weight, At: at,
	})
	if err != nil {
		return "", err
	}
	if err := uc.Bols.Save(ctx, bol, 0); err != nil {
		return "", err
	}
	if err := uc.Sink.PublishBolEvents(ctx, events...); err != nil {
		return "", err
	}
	return OutcomeApplied, nil
}

// flagWithoutClose runs FlagDepartureWithoutClose (the out-of-order case
// included): no confirmation, no BOL; every package not loaded raises
// PackageMissedLoad once and the load stays for a supervisor (ADR 0002's
// note records the ordering).
func (uc *OnDeparture) flagWithoutClose(ctx context.Context, load *outboundload.OutboundLoad, at time.Time) (Outcome, error) {
	if load.Trailer() == "" {
		// The trailer was never attached to this load: the departure is
		// about another door's work.
		return OutcomeIgnored, nil
	}
	pkgs, err := uc.loadPackages(ctx, load.ID())
	if err != nil {
		return "", err
	}
	flagged := 0
	for _, p := range pkgs {
		before := p.MissedFlagged()
		if err := p.FlagMissedLoad(load.Cutoff(), at); err != nil {
			// LOADED or SHIPPED packages are exactly what did make it.
			continue
		}
		if err := uc.Packages.Save(ctx, p, p.Version()-1); err != nil {
			return "", err
		}
		if !before {
			flagged++
		}
		if err := uc.Sink.PublishPackageEvents(ctx, p.PullEvents()...); err != nil {
			return "", err
		}
	}
	return OutcomeApplied, nil
}

// loadPackages pages through every package of the load.
func (uc *OnDeparture) loadPackages(ctx context.Context, load shared.LoadID) ([]*shippingpackage.ShippingPackage, error) {
	var out []*shippingpackage.ShippingPackage
	after := shared.PackageID("")
	for {
		page, err := uc.Packages.ListByLoad(ctx, load, repository.PackageFilter{}, after, 200)
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

// carrierOf reads the carrier the load's appointment named from the local
// copy ("" when the appointment is unknown: the BOL then carries an empty
// carrier, which the domain refuses — the confirm surfaces that refusal).
func (uc *OnDeparture) carrierOf(ctx context.Context, load *outboundload.OutboundLoad) (carrier, scac string) {
	if load.Appointment() == "" {
		return "", ""
	}
	a, ok, err := uc.Copies.Get(ctx, load.Appointment())
	if err != nil || !ok {
		return "", ""
	}
	return a.Carrier, a.CarrierScac
}
