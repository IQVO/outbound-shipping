// Package shippingpackage holds the ShippingPackage aggregate: one
// manifested package and where it is on its way to the truck (ADR 0001). v1
// ships packages, not pallets (decision D4). It is pure: no I/O, no clock
// reads; the facts it raises are plain domain events.
package shippingpackage

import (
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// Event is a domain event raised by the ShippingPackage aggregate. Every
// event carries the package id, the site, the aggregate version AFTER the
// change and when it happened.
type Event interface {
	EventName() string
	PackageID() shared.PackageID
	SiteID() shared.SiteID
	PackageVersion() int64
	OccurredAt() time.Time
}

// Header is embedded in every ShippingPackage event.
type Header struct {
	Package shared.PackageID
	Site    shared.SiteID
	Version int64
	At      time.Time
}

// PackageID returns the package the event is about.
func (h Header) PackageID() shared.PackageID { return h.Package }

// SiteID returns the site the package belongs to.
func (h Header) SiteID() shared.SiteID { return h.Site }

// PackageVersion returns the aggregate version after the change.
func (h Header) PackageVersion() int64 { return h.Version }

// OccurredAt returns when the change happened.
func (h Header) OccurredAt() time.Time { return h.At }

// PackageStaged is raised when a package stages at a Staging location.
type PackageStaged struct {
	Header
	LoadID          shared.LoadID
	StagingLocation string
	StagedAt        time.Time
}

// EventName returns "PackageStaged".
func (PackageStaged) EventName() string { return "PackageStaged" }

// PackageLoaded is raised when a package loads through its load's door.
type PackageLoaded struct {
	Header
	LoadID   shared.LoadID
	LoadedAt time.Time
}

// EventName returns "PackageLoaded".
func (PackageLoaded) EventName() string { return "PackageLoaded" }

// PackageUnloaded is raised when a loaded package comes back off the load.
// Reason is empty when the operator gave none.
type PackageUnloaded struct {
	Header
	LoadID     shared.LoadID
	UnloadedAt time.Time
	Reason     string
}

// EventName returns "PackageUnloaded".
func (PackageUnloaded) EventName() string { return "PackageUnloaded" }

// PackageMissedLoad is raised at most once per package and load: the package
// was not loaded by its cutoff (sweep) or by the trailer's departure. It
// changes no state; it is the exception-management and re-promise fact.
type PackageMissedLoad struct {
	Header
	LoadID        shared.LoadID
	SourceOrderID string
	Cutoff        time.Time
	DetectedAt    time.Time
}

// EventName returns "PackageMissedLoad".
func (PackageMissedLoad) EventName() string { return "PackageMissedLoad" }
