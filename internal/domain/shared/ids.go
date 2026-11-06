// Package shared holds the value objects every outbound-shipping aggregate
// uses: the typed identifiers from the domain model, the short-ship entry a
// load records, and nothing else. It performs no I/O and imports nothing
// from this module.
package shared

// SiteID is facility-layout's siteCode. A location maps to a site via its
// zoneId, never by parsing a code (the model's site_key convention).
type SiteID string

// CptID identifies one cutoff in the local CPT schedule copy.
type CptID string

// LoadID is the OutboundLoad aggregate root id.
type LoadID string

// PackageID is a manifested package id (fulfillment-execution's own id).
type PackageID string

// BolNumber is the BillOfLading aggregate root id.
type BolNumber string

// TrailerID is yard-management's trailer id.
type TrailerID string

// DoorCode is a dock door code (dock-scheduling's door_code).
type DoorCode string

// AppointmentID is a dock appointment id.
type AppointmentID string

// ShipMethod is the carrier ship method a load is planned for.
type ShipMethod string
