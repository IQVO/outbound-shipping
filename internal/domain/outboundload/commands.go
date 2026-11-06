package outboundload

import (
	"slices"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// PlanInput is everything Plan needs: the cutoff key from the local CPT
// copy plus the id, and the cutoff keys of the loads that are already open
// for the site (PLANNED or LOADING), so the one-open-load rule can be
// enforced in the domain while the use case owns the query.
type PlanInput struct {
	LoadID        shared.LoadID
	Site          shared.SiteID
	Cpt           shared.CptID
	OperatingDate OperatingDate
	ShipMethod    shared.ShipMethod
	Cutoff        time.Time
	At            time.Time
	// OpenLoadKeys are the cutoff keys of the site's open loads at Plan
	// time (the partial unique index is the race backstop, PLAN wave 5).
	OpenLoadKeys []LoadKey
}

// Plan creates a PLANNED load at version 1 and raises LoadPlanned. It
// enforces the one-open-load invariant (ErrDuplicateOpenLoad).
func Plan(in PlanInput) (*OutboundLoad, []Event, error) {
	key := LoadKey{Site: in.Site, Cpt: in.Cpt, OperatingDate: in.OperatingDate, ShipMethod: in.ShipMethod}
	if err := validatePlan(in, key); err != nil {
		return nil, nil, err
	}
	l := &OutboundLoad{
		id: in.LoadID, site: in.Site, cpt: in.Cpt, operatingDate: in.OperatingDate,
		shipMethod: in.ShipMethod, cutoff: in.Cutoff.UTC(), state: StatePlanned,
		version: 1,
	}
	ev := LoadPlanned{
		Header:        l.header(in.At),
		CptID:         l.cpt,
		OperatingDate: l.operatingDate,
		ShipMethod:    l.shipMethod,
		Cutoff:        l.cutoff,
	}
	l.raise(ev)
	return l, l.PullEvents(), nil
}

func validatePlan(in PlanInput, key LoadKey) error {
	if in.LoadID == "" {
		return ErrInvalidLoadID
	}
	if in.Site == "" || in.Cpt == "" || in.ShipMethod == "" || in.Cutoff.IsZero() {
		return ErrRequiredField
	}
	if !in.OperatingDate.Valid() {
		return ErrInvalidOperatingDate
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	for _, open := range in.OpenLoadKeys {
		if open == key {
			return ErrDuplicateOpenLoad
		}
	}
	return nil
}

// AssignDoorInput names the door and the outbound dock appointment that
// assigned it (DoorFromAppointment policy).
type AssignDoorInput struct {
	DoorCode      shared.DoorCode
	AppointmentID shared.AppointmentID
	At            time.Time
}

// AssignDoor assigns or re-assigns the load's door and raises
// LoadDoorAssigned. Allowed while PLANNED or LOADING (a later appointment
// for the same cpt re-assigns).
func (l *OutboundLoad) AssignDoor(in AssignDoorInput) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StatePlanned && l.state != StateLoading {
		return ErrInvalidTransition
	}
	if in.DoorCode == "" || in.AppointmentID == "" {
		return ErrRequiredField
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	l.door = in.DoorCode
	l.appointment = in.AppointmentID
	l.version++
	l.raise(LoadDoorAssigned{Header: l.header(in.At), DoorCode: l.door, AppointmentID: l.appointment})
	return nil
}

// TrailerDocked is the yard-management dock fact the StartOnDock policy
// feeds to AttachTrailer: the trailer, the door it docked at, and when.
type TrailerDocked struct {
	TrailerID shared.TrailerID
	DoorCode  shared.DoorCode
	At        time.Time
}

// AttachTrailer records the docked trailer against the load. The attachment
// IS the dock fact: StartLoading refuses without one (ErrTrailerNotDocked).
// It raises no event (the model publishes no attach event); the trailer
// appears on LoadingStarted.
func (l *OutboundLoad) AttachTrailer(d TrailerDocked) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StatePlanned && l.state != StateLoading {
		return ErrInvalidTransition
	}
	if l.door == "" {
		return ErrTrailerNotDocked
	}
	if d.TrailerID == "" || d.At.IsZero() {
		return ErrRequiredField
	}
	if d.DoorCode != l.door {
		return ErrWrongDoor
	}
	l.trailer = d.TrailerID
	return nil
}

// StartLoading moves PLANNED -> LOADING and raises LoadingStarted. Loading
// starts only when the load has a door and a docked trailer
// (ErrTrailerNotDocked).
func (l *OutboundLoad) StartLoading(at time.Time) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StatePlanned {
		return ErrInvalidTransition
	}
	if l.door == "" || l.trailer == "" {
		return ErrTrailerNotDocked
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	l.state = StateLoading
	l.version++
	l.raise(LoadingStarted{Header: l.header(at), DoorCode: l.door, TrailerID: l.trailer, StartedAt: at.UTC()})
	return nil
}

// CloseInput carries what the use case knows at close: the packages still
// outstanding (neither loaded nor short-shipped — must be empty), the
// short-ships to record, and the loaded package count.
type CloseInput struct {
	OutstandingPackages []shared.PackageID
	ShortShips          []shared.ShortShip
	LoadedCount         int
	At                  time.Time
}

// CloseLoad moves LOADING -> CLOSED and raises LoadClosed. Any outstanding
// package refuses the close (ErrPackagesOutstanding); every short-ship
// needs a package and a reason.
func (l *OutboundLoad) CloseLoad(in CloseInput) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StateLoading {
		return ErrInvalidTransition
	}
	if len(in.OutstandingPackages) > 0 {
		return ErrPackagesOutstanding
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	if in.LoadedCount < 0 {
		return ErrInvalidShortShip
	}
	for _, s := range in.ShortShips {
		if err := s.Valid(); err != nil {
			return ErrInvalidShortShip
		}
	}
	l.state = StateClosed
	l.shortShips = slices.Clone(in.ShortShips)
	l.packageCount = in.LoadedCount
	l.version++
	l.raise(LoadClosed{
		Header:            l.header(in.At),
		DoorCode:          l.door,
		TrailerID:         l.trailer,
		PackageCount:      l.packageCount,
		ShortShippedCount: len(l.shortShips),
		ClosedAt:          in.At.UTC(),
	})
	return nil
}

// ReopenLoad moves CLOSED -> LOADING and raises LoadReopened. A
// SHIP_CONFIRMED load is never reopened (ErrLoadConfirmed).
func (l *OutboundLoad) ReopenLoad(at time.Time) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StateClosed {
		return ErrInvalidTransition
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	l.state = StateLoading
	l.version++
	l.raise(LoadReopened{Header: l.header(at), ReopenedAt: at.UTC()})
	return nil
}

// Departure is the TrailerDeparted fact ConfirmShipment consumes (ADR 0002):
// which trailer left, carrying which seal, when. The domain never invents
// these values.
type Departure struct {
	TrailerID  shared.TrailerID
	SealNumber string
	DepartedAt time.Time
}

// ConfirmInput is everything ConfirmShipment needs: the departure facts
// from yard-management, the carrier of the ship method's appointment, and
// the distinct order ids of the shipped packages (from source_order_id,
// ADR 0003).
type ConfirmInput struct {
	Departure Departure
	Carrier   string
	Orders    []string
	At        time.Time
}

// ConfirmShipment moves CLOSED -> SHIP_CONFIRMED and raises
// ShipmentConfirmed. Only a CLOSED load confirms (ErrLoadNotClosed — a
// departure while LOADING never confirms, ADR 0002); a confirmed load never
// changes (ErrLoadConfirmed). The orders are deduplicated and sorted so the
// payload is deterministic.
func (l *OutboundLoad) ConfirmShipment(in ConfirmInput) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StateClosed {
		return ErrLoadNotClosed
	}
	if in.Departure.TrailerID == "" || in.Departure.DepartedAt.IsZero() {
		return ErrRequiredField
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	l.state = StateShipConfirmed
	l.version++
	l.raise(ShipmentConfirmed{
		Header:     l.header(in.At),
		CptID:      l.cpt,
		Carrier:    in.Carrier,
		TrailerID:  in.Departure.TrailerID,
		SealNumber: in.Departure.SealNumber,
		DepartedAt: in.Departure.DepartedAt.UTC(),
		Orders:     distinctSorted(in.Orders),
	})
	return nil
}

func distinctSorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// CancelLoad moves PLANNED -> CANCELLED and raises LoadCancelled. Only a
// PLANNED load can be cancelled: a LOADING load must be resolved (short-ship
// and close) instead.
func (l *OutboundLoad) CancelLoad(reason string, at time.Time) error {
	if err := l.refuseIfConfirmed(); err != nil {
		return err
	}
	if l.state != StatePlanned {
		return ErrInvalidTransition
	}
	if reason == "" {
		return ErrCancelReasonRequired
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	l.state = StateCancelled
	l.version++
	l.raise(LoadCancelled{Header: l.header(at), CancelledAt: at.UTC()})
	return nil
}
