// Package billoflading holds the BillOfLading aggregate: the shipping
// document for one confirmed load, issued once per load in v1 (ADR 0001;
// master BOL deferred, decision D4). It is pure: no I/O, no clock reads.
package billoflading

import (
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// Event is a domain event raised by the BillOfLading aggregate.
type Event interface {
	EventName() string
	BolNumber() shared.BolNumber
	SiteID() shared.SiteID
	BolVersion() int64
	OccurredAt() time.Time
}

// Header is embedded in every BillOfLading event.
type Header struct {
	Bol     shared.BolNumber
	Site    shared.SiteID
	Version int64
	At      time.Time
}

// BolNumber returns the document the event is about.
func (h Header) BolNumber() shared.BolNumber { return h.Bol }

// SiteID returns the site of the load.
func (h Header) SiteID() shared.SiteID { return h.Site }

// BolVersion returns the aggregate version after the change.
func (h Header) BolVersion() int64 { return h.Version }

// OccurredAt returns when the change happened.
func (h Header) OccurredAt() time.Time { return h.At }

// BillOfLadingIssued is raised with the ship confirmation: the document of
// one departed load, with the seal yard-management recorded.
type BillOfLadingIssued struct {
	Header
	LoadID       shared.LoadID
	Carrier      string
	CarrierScac  string
	TrailerID    shared.TrailerID
	SealNumber   string
	PackageCount int
	TotalWeightG float64
	IssuedAt     time.Time
}

// EventName returns "BillOfLadingIssued".
func (BillOfLadingIssued) EventName() string { return "BillOfLadingIssued" }

// BillOfLadingVoided is raised when a supervisor voids the document (the
// shipment itself is not undone).
type BillOfLadingVoided struct {
	Header
	LoadID   shared.LoadID
	Reason   string
	VoidedAt time.Time
}

// EventName returns "BillOfLadingVoided".
func (BillOfLadingVoided) EventName() string { return "BillOfLadingVoided" }
