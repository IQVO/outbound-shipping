// Package outboundload holds the OutboundLoad aggregate: the packages
// planned to leave together for one (site, cpt, operating date, ship
// method), planned automatically from the first manifested package (ADR
// 0001). It is pure: no I/O, no clock reads; the facts it raises are plain
// domain events the application layer hands to the transactional outbox.
package outboundload

import (
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// Event is a domain event raised by the OutboundLoad aggregate. Every event
// carries the load id, the site, the aggregate version AFTER the change and
// when it happened (from the caller's clock).
type Event interface {
	EventName() string
	LoadID() shared.LoadID
	SiteID() shared.SiteID
	LoadVersion() int64
	OccurredAt() time.Time
}

// Header is embedded in every OutboundLoad event.
type Header struct {
	Load    shared.LoadID
	Site    shared.SiteID
	Version int64
	At      time.Time
}

// LoadID returns the load the event is about.
func (h Header) LoadID() shared.LoadID { return h.Load }

// SiteID returns the site the load belongs to.
func (h Header) SiteID() shared.SiteID { return h.Site }

// LoadVersion returns the aggregate version after the change.
func (h Header) LoadVersion() int64 { return h.Version }

// OccurredAt returns when the change happened.
func (h Header) OccurredAt() time.Time { return h.At }

// LoadPlanned is raised when the first package of a cutoff key manifests and
// the load is planned (PlanLoadFromCutoff policy).
type LoadPlanned struct {
	Header
	CptID         shared.CptID
	OperatingDate OperatingDate
	ShipMethod    shared.ShipMethod
	Cutoff        time.Time
}

// EventName returns "LoadPlanned".
func (LoadPlanned) EventName() string { return "LoadPlanned" }

// LoadDoorAssigned is raised when an outbound dock appointment names the
// load's cpt and the door is assigned (DoorFromAppointment policy).
type LoadDoorAssigned struct {
	Header
	DoorCode      shared.DoorCode
	AppointmentID shared.AppointmentID
}

// EventName returns "LoadDoorAssigned".
func (LoadDoorAssigned) EventName() string { return "LoadDoorAssigned" }

// LoadingStarted is raised when loading begins at the assigned door with the
// docked trailer attached.
type LoadingStarted struct {
	Header
	DoorCode  shared.DoorCode
	TrailerID shared.TrailerID
	StartedAt time.Time
}

// EventName returns "LoadingStarted".
func (LoadingStarted) EventName() string { return "LoadingStarted" }

// LoadClosed is raised when every package is loaded or short-shipped.
type LoadClosed struct {
	Header
	DoorCode          shared.DoorCode
	TrailerID         shared.TrailerID
	PackageCount      int
	ShortShippedCount int
	ClosedAt          time.Time
}

// EventName returns "LoadClosed".
func (LoadClosed) EventName() string { return "LoadClosed" }

// LoadReopened is raised when a supervisor reopens a closed load.
type LoadReopened struct {
	Header
	ReopenedAt time.Time
}

// EventName returns "LoadReopened".
func (LoadReopened) EventName() string { return "LoadReopened" }

// ShipmentConfirmed is raised when TrailerDeparted arrives for a CLOSED
// load: the goods physically left (ADR 0002). Orders lists every distinct
// source_order_id of the shipped packages.
type ShipmentConfirmed struct {
	Header
	CptID      shared.CptID
	Carrier    string
	TrailerID  shared.TrailerID
	SealNumber string
	DepartedAt time.Time
	Orders     []string
}

// EventName returns "ShipmentConfirmed".
func (ShipmentConfirmed) EventName() string { return "ShipmentConfirmed" }

// LoadCancelled is raised when a planned load is cancelled before loading.
type LoadCancelled struct {
	Header
	CancelledAt time.Time
}

// EventName returns "LoadCancelled".
func (LoadCancelled) EventName() string { return "LoadCancelled" }
