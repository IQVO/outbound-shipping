package outboundload

import (
	"slices"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// State is the load lifecycle. Exactly the model's five states.
type State string

// The load states (model.yaml): PLANNED, LOADING, CLOSED, SHIP_CONFIRMED,
// CANCELLED. PLANNED and LOADING are the "open" states of the one-open-load
// rule.
const (
	StatePlanned       State = "PLANNED"
	StateLoading       State = "LOADING"
	StateClosed        State = "CLOSED"
	StateShipConfirmed State = "SHIP_CONFIRMED"
	StateCancelled     State = "CANCELLED"
)

// OperatingDate is the calendar date a load ships on, in YYYY-MM-DD (UTC).
type OperatingDate string

// Valid reports whether the date parses as a calendar date.
func (d OperatingDate) Valid() bool {
	_, err := time.Parse("2006-01-02", string(d))
	return err == nil
}

// LoadKey is the cutoff key of the one-open-load rule: one open load per
// (site, cpt, operating date, ship method).
type LoadKey struct {
	Site          shared.SiteID
	Cpt           shared.CptID
	OperatingDate OperatingDate
	ShipMethod    shared.ShipMethod
}

// Key returns the load's cutoff key.
func (l *OutboundLoad) Key() LoadKey {
	return LoadKey{Site: l.site, Cpt: l.cpt, OperatingDate: l.operatingDate, ShipMethod: l.shipMethod}
}

// OutboundLoad is the aggregate root.
//
// States: PLANNED -> LOADING -> CLOSED -> SHIP_CONFIRMED, with PLANNED ->
// CANCELLED and CLOSED -> LOADING (reopen). Version is 1 when planned and
// +1 per event-raising transition. Invariants (model.yaml): one open load
// per cutoff key (ErrDuplicateOpenLoad); loading starts only with a door and
// a docked trailer (ErrTrailerNotDocked); a load closes only when every
// package is loaded or short-shipped with a reason (ErrPackagesOutstanding);
// ship-confirm needs a CLOSED load (ErrLoadNotClosed); a SHIP_CONFIRMED load
// never changes (ErrLoadConfirmed).
type OutboundLoad struct {
	id            shared.LoadID
	site          shared.SiteID
	cpt           shared.CptID
	operatingDate OperatingDate
	shipMethod    shared.ShipMethod
	cutoff        time.Time
	state         State
	door          shared.DoorCode
	trailer       shared.TrailerID
	appointment   shared.AppointmentID
	shortShips    []shared.ShortShip
	packageCount  int
	version       int64
	events        []Event
}

// RehydrateParams is the persisted state of a load, used by repository
// adapters to rebuild it without re-raising events.
type RehydrateParams struct {
	ID            shared.LoadID
	Site          shared.SiteID
	Cpt           shared.CptID
	OperatingDate OperatingDate
	ShipMethod    shared.ShipMethod
	Cutoff        time.Time
	State         State
	Door          shared.DoorCode
	Trailer       shared.TrailerID
	Appointment   shared.AppointmentID
	ShortShips    []shared.ShortShip
	PackageCount  int
	Version       int64
}

// validStates are the states Rehydrate accepts.
var validStates = []State{StatePlanned, StateLoading, StateClosed, StateShipConfirmed, StateCancelled}

// Rehydrate rebuilds a persisted load, validating every field, so a corrupt
// row can never become a live aggregate. A rehydrated load has no pending
// events.
func Rehydrate(p RehydrateParams) (*OutboundLoad, error) {
	if p.ID == "" {
		return nil, ErrInvalidLoadID
	}
	if p.Site == "" || p.Cpt == "" || p.ShipMethod == "" || p.Cutoff.IsZero() {
		return nil, ErrRequiredField
	}
	if !p.OperatingDate.Valid() {
		return nil, ErrInvalidOperatingDate
	}
	if !slices.Contains(validStates, p.State) {
		return nil, ErrInvalidState
	}
	if p.Version < 1 {
		return nil, ErrInvalidVersion
	}
	for _, s := range p.ShortShips {
		if err := s.Valid(); err != nil {
			return nil, ErrInvalidShortShip
		}
	}
	return &OutboundLoad{
		id: p.ID, site: p.Site, cpt: p.Cpt, operatingDate: p.OperatingDate,
		shipMethod: p.ShipMethod, cutoff: p.Cutoff.UTC(), state: p.State,
		door: p.Door, trailer: p.Trailer, appointment: p.Appointment,
		shortShips: slices.Clone(p.ShortShips), packageCount: p.PackageCount,
		version: p.Version,
	}, nil
}

// PullEvents returns the events recorded since the last PullEvents (or since
// construction) and clears them, so each event is handed over exactly once.
// Rehydrated loads never have any.
func (l *OutboundLoad) PullEvents() []Event {
	out := l.events
	l.events = nil
	return out
}

func (l *OutboundLoad) raise(e Event) { l.events = append(l.events, e) }

func (l *OutboundLoad) header(at time.Time) Header {
	return Header{Load: l.id, Site: l.site, Version: l.version, At: at.UTC()}
}

// refuseIfConfirmed implements the ErrLoadConfirmed invariant for every
// mutating command.
func (l *OutboundLoad) refuseIfConfirmed() error {
	if l.state == StateShipConfirmed {
		return ErrLoadConfirmed
	}
	return nil
}

// ID returns the load id.
func (l *OutboundLoad) ID() shared.LoadID { return l.id }

// SiteID returns the site of the load.
func (l *OutboundLoad) SiteID() shared.SiteID { return l.site }

// CptID returns the cutoff id the load is planned for.
func (l *OutboundLoad) CptID() shared.CptID { return l.cpt }

// OperatingDate returns the operating date (YYYY-MM-DD).
func (l *OutboundLoad) OperatingDate() OperatingDate { return l.operatingDate }

// ShipMethod returns the ship method.
func (l *OutboundLoad) ShipMethod() shared.ShipMethod { return l.shipMethod }

// Cutoff returns the cutoff instant.
func (l *OutboundLoad) Cutoff() time.Time { return l.cutoff }

// State returns the load's current state.
func (l *OutboundLoad) State() State { return l.state }

// Door returns the assigned door ("" before an appointment names the cpt).
func (l *OutboundLoad) Door() shared.DoorCode { return l.door }

// Trailer returns the docked trailer attached to the load.
func (l *OutboundLoad) Trailer() shared.TrailerID { return l.trailer }

// Appointment returns the dock appointment that assigned the door.
func (l *OutboundLoad) Appointment() shared.AppointmentID { return l.appointment }

// ShortShips returns the recorded short-ships (a copy).
func (l *OutboundLoad) ShortShips() []shared.ShortShip { return slices.Clone(l.shortShips) }

// PackageCount returns the loaded package count recorded at close.
func (l *OutboundLoad) PackageCount() int { return l.packageCount }

// Version returns the aggregate version.
func (l *OutboundLoad) Version() int64 { return l.version }
