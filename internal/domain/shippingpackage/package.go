package shippingpackage

import (
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// State is the package lifecycle. Exactly the model's five states.
type State string

// The package states (model.yaml): EXPECTED, STAGED, LOADED, SHIPPED,
// SHORT_SHIPPED.
const (
	StateExpected     State = "EXPECTED"
	StateStaged       State = "STAGED"
	StateLoaded       State = "LOADED"
	StateShipped      State = "SHIPPED"
	StateShortShipped State = "SHORT_SHIPPED"
)

// validStates are the states Rehydrate accepts.
var validStates = []State{StateExpected, StateStaged, StateLoaded, StateShipped, StateShortShipped}

// ShippingPackage is the aggregate root: one manifested package and where it
// is on its way to the truck.
//
// States: EXPECTED -> STAGED -> LOADED -> SHIPPED, with EXPECTED ->
// LOADED (loaded straight off the line), LOADED -> STAGED (unload) and
// EXPECTED|STAGED -> SHORT_SHIPPED. Version is 1 when expected and +1 per
// event-raising change. Invariants (model.yaml): stages only at a Staging
// location of its site (ErrNotStagingLocation); loads only onto its own load
// through that load's door (ErrWrongLoad); belongs to one load at a time
// (ErrPackageOnAnotherLoad); unloads only before ship-confirm
// (ErrAlreadyShipped).
type ShippingPackage struct {
	id            shared.PackageID
	site          shared.SiteID
	sourceOrderID string
	workUnitRef   string
	loadID        shared.LoadID
	stagingLoc    string
	state         State
	missedFlagged bool
	version       int64
	events        []Event
}

// RehydrateParams is the persisted state of a package, used by repository
// adapters to rebuild it without re-raising events.
type RehydrateParams struct {
	ID            shared.PackageID
	Site          shared.SiteID
	SourceOrderID string
	WorkUnitRef   string
	LoadID        shared.LoadID
	StagingLoc    string
	State         State
	MissedFlagged bool
	Version       int64
}

// Rehydrate rebuilds a persisted package, validating every field, so a
// corrupt row can never become a live aggregate. A rehydrated package has no
// pending events.
func Rehydrate(p RehydrateParams) (*ShippingPackage, error) {
	if p.ID == "" || p.Site == "" {
		return nil, ErrRequiredField
	}
	if !containsState(p.State) {
		return nil, ErrInvalidState
	}
	if p.Version < 1 {
		return nil, ErrInvalidVersion
	}
	return &ShippingPackage{
		id: p.ID, site: p.Site, sourceOrderID: p.SourceOrderID,
		workUnitRef: p.WorkUnitRef, loadID: p.LoadID, stagingLoc: p.StagingLoc,
		state: p.State, missedFlagged: p.MissedFlagged, version: p.Version,
	}, nil
}

func containsState(s State) bool {
	for _, v := range validStates {
		if v == s {
			return true
		}
	}
	return false
}

// PullEvents returns the events recorded since the last PullEvents (or since
// construction) and clears them, so each event is handed over exactly once.
// Rehydrated packages never have any.
func (p *ShippingPackage) PullEvents() []Event {
	out := p.events
	p.events = nil
	return out
}

func (p *ShippingPackage) raise(e Event) { p.events = append(p.events, e) }

func (p *ShippingPackage) header(at time.Time) Header {
	return Header{Package: p.id, Site: p.site, Version: p.version, At: at.UTC()}
}

// ID returns the package id.
func (p *ShippingPackage) ID() shared.PackageID { return p.id }

// SiteID returns the site of the package.
func (p *ShippingPackage) SiteID() shared.SiteID { return p.site }

// SourceOrderID returns the order-management OrderId the manifest carried
// ("" when the producer did not know it, ADR 0003).
func (p *ShippingPackage) SourceOrderID() string { return p.sourceOrderID }

// WorkUnitRef returns the manifested order_ref, stored verbatim.
func (p *ShippingPackage) WorkUnitRef() string { return p.workUnitRef }

// LoadID returns the load the package is assigned to ("" before assignment).
func (p *ShippingPackage) LoadID() shared.LoadID { return p.loadID }

// StagingLocation returns the staging location ("" while EXPECTED).
func (p *ShippingPackage) StagingLocation() string { return p.stagingLoc }

// State returns the package's current state.
func (p *ShippingPackage) State() State { return p.state }

// MissedFlagged reports whether PackageMissedLoad was already raised (the
// sweep's once-guard).
func (p *ShippingPackage) MissedFlagged() bool { return p.missedFlagged }

// Version returns the aggregate version.
func (p *ShippingPackage) Version() int64 { return p.version }
