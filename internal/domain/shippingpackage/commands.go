package shippingpackage

import (
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// ExpectInput is the PackageManifested fact the AssignPackageToLoad policy
// feeds: the package id, its site, and the correlation the manifest carried
// (ADR 0003). WeightG is the manifested weight in grams (0 when the producer
// omitted it) — the bill of lading sums it. Expect raises no event (the
// model publishes none for it).
type ExpectInput struct {
	PackageID     shared.PackageID
	Site          shared.SiteID
	SourceOrderID string
	WorkUnitRef   string
	WeightG       float64
	At            time.Time
}

// Expect records a manifested package as EXPECTED at version 1.
func Expect(in ExpectInput) (*ShippingPackage, error) {
	if in.PackageID == "" || in.Site == "" {
		return nil, ErrRequiredField
	}
	if in.At.IsZero() {
		return nil, ErrNoTimestamp
	}
	if in.WeightG < 0 {
		return nil, ErrInvalidWeight
	}
	return &ShippingPackage{
		id: in.PackageID, site: in.Site, sourceOrderID: in.SourceOrderID,
		workUnitRef: in.WorkUnitRef, weightG: in.WeightG,
		state: StateExpected, version: 1,
	}, nil
}

// AssignToLoad assigns the package to its load (AssignPackageToLoad
// policy). A package already on ANOTHER load refuses with
// ErrPackageOnAnotherLoad; re-assigning to the same load is a no-op. It
// raises no event.
func (p *ShippingPackage) AssignToLoad(loadID shared.LoadID) error {
	if loadID == "" {
		return ErrRequiredField
	}
	if p.loadID != "" && p.loadID != loadID {
		return ErrPackageOnAnotherLoad
	}
	p.loadID = loadID
	return nil
}

// StageInput names the location and what the staging-location local copy
// knows about it: whether it is a Staging location, and which site it
// belongs to.
type StageInput struct {
	Location     string
	IsStaging    bool
	LocationSite shared.SiteID
	At           time.Time
}

// Stage moves EXPECTED -> STAGED and raises PackageStaged. The location must
// be a Staging location of the package's own site
// (ErrNotStagingLocation). Staging again at the same location is a no-op.
func (p *ShippingPackage) Stage(in StageInput) error {
	if p.state == StateShipped || p.state == StateShortShipped || p.state == StateLoaded {
		return ErrInvalidTransition
	}
	if in.Location == "" || !in.IsStaging || in.LocationSite != p.site {
		return ErrNotStagingLocation
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	if p.state == StateStaged {
		if p.stagingLoc == in.Location {
			return nil
		}
		return ErrInvalidTransition
	}
	p.state = StateStaged
	p.stagingLoc = in.Location
	p.version++
	p.raise(PackageStaged{Header: p.header(in.At), LoadID: p.loadID, StagingLocation: in.Location, StagedAt: in.At.UTC()})
	return nil
}

// LoadInput names the load being scanned and the door facts: the door the
// scan went through and the door the load is assigned to.
type LoadInput struct {
	LoadID   shared.LoadID
	DoorCode shared.DoorCode
	LoadDoor shared.DoorCode
	At       time.Time
}

// Load moves EXPECTED or STAGED -> LOADED and raises PackageLoaded. The
// package loads only onto its own load (ErrWrongLoad for another load id or
// no assignment) and through that load's door (ErrWrongLoad for any other
// door).
func (p *ShippingPackage) Load(in LoadInput) error {
	if p.state == StateShipped {
		return ErrAlreadyShipped
	}
	if p.state != StateExpected && p.state != StateStaged {
		return ErrInvalidTransition
	}
	if p.loadID == "" || in.LoadID == "" {
		return ErrWrongLoad
	}
	if in.LoadID != p.loadID {
		return ErrWrongLoad
	}
	if in.DoorCode == "" || in.DoorCode != in.LoadDoor {
		return ErrWrongLoad
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	p.state = StateLoaded
	p.version++
	p.raise(PackageLoaded{Header: p.header(in.At), LoadID: p.loadID, LoadedAt: in.At.UTC()})
	return nil
}

// Unload moves LOADED -> STAGED and raises PackageUnloaded with the
// operator's reason (empty when none). Once the package has shipped it
// refuses with ErrAlreadyShipped: the truck has left.
func (p *ShippingPackage) Unload(reason string, at time.Time) error {
	if p.state == StateShipped {
		return ErrAlreadyShipped
	}
	if p.state != StateLoaded {
		return ErrInvalidTransition
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	p.state = StateStaged
	p.version++
	p.raise(PackageUnloaded{Header: p.header(at), LoadID: p.loadID, UnloadedAt: at.UTC(), Reason: reason})
	return nil
}

// MarkShipped moves LOADED -> SHIPPED (ConfirmOnDeparture policy, after the
// load confirmed). It raises no event (the model publishes none for it).
func (p *ShippingPackage) MarkShipped(at time.Time) error {
	if p.state != StateLoaded {
		return ErrInvalidTransition
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	p.state = StateShipped
	return nil
}

// MarkShortShipped moves EXPECTED or STAGED -> SHORT_SHIPPED with a reason
// (the DropDiverted policy uses reason DIVERTED). It raises no event (the
// model publishes none for it; LoadClosed carries the count).
func (p *ShippingPackage) MarkShortShipped(reason string, at time.Time) error {
	if p.state != StateExpected && p.state != StateStaged {
		return ErrInvalidTransition
	}
	if reason == "" {
		return ErrShortShipReasonRequired
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	p.state = StateShortShipped
	return nil
}

// FlagMissedLoad raises PackageMissedLoad for a package that is not loaded —
// the cutoff sweep (SweepMissedLoad) and a departure while the load is still
// LOADING (FlagDepartureWithoutClose) both call it. It changes no state and
// raises the event AT MOST ONCE per package: a re-run sweep finds
// missedFlagged set and raises nothing.
func (p *ShippingPackage) FlagMissedLoad(cutoff, at time.Time) error {
	if p.state == StateLoaded || p.state == StateShipped {
		return ErrInvalidTransition
	}
	if p.loadID == "" {
		return ErrLoadRequired
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	if p.missedFlagged {
		return nil
	}
	p.missedFlagged = true
	p.version++
	p.raise(PackageMissedLoad{
		Header:        p.header(at),
		LoadID:        p.loadID,
		SourceOrderID: p.sourceOrderID,
		Cutoff:        cutoff.UTC(),
		DetectedAt:    at.UTC(),
	})
	return nil
}
