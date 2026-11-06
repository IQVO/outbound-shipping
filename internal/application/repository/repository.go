// Package repository holds the non-interface vocabulary of the application's
// ports: typed errors, list filters and the value types of the event-fed
// local copies (wave 5). It is separate from package ports because ports may
// contain interfaces only (internal/architecture's "ports package only
// contains interfaces" rule).
package repository

import (
	"errors"
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

var (
	// ErrLoadNotFound is returned when a load id is unknown.
	ErrLoadNotFound = errors.New("outbound load not found")
	// ErrPackageNotFound is returned when a package id is unknown.
	ErrPackageNotFound = errors.New("shipping package not found")
	// ErrBolNotFound is returned when a BOL number is unknown.
	ErrBolNotFound = errors.New("bill of lading not found")
	// ErrDuplicateOpenLoad is returned by LoadRepo.Save when the insert
	// would leave two open loads for one (site, cpt, operating date, ship
	// method): the partial unique index uq_open_load_per_cutoff is the race
	// backstop of the one-open-load invariant (PLAN wave 5).
	ErrDuplicateOpenLoad = errors.New("an open load already exists for this site, cpt, operating date and ship method")
	// ErrConcurrentModification is returned by a version-guarded Save when
	// the stored version is not the version the caller loaded: another
	// writer won the race.
	ErrConcurrentModification = errors.New("concurrent modification")
	// ErrCptScheduleUnknown is returned when no CPT schedule was ever seen
	// for a site (the load planner cannot resolve a cutoff instant).
	ErrCptScheduleUnknown = errors.New("no cpt schedule known for this site")
	// ErrCptNotFound is returned when the site's schedule has no cutoff
	// entry matching a manifested cutoff instant.
	ErrCptNotFound = errors.New("the site's cpt schedule has no cutoff at this time")
)

// CutoffEntry is one cutoff of a site's CPT schedule copy: the model's
// (cpt_id, local_time, days_of_week, ship_method) key a load is planned for.
type CutoffEntry struct {
	CptID      string
	LocalTime  string   // "HH:MM" in the site's timezone
	DaysOfWeek []string // empty means every day
	ShipMethod shared.ShipMethod
}

// CptSchedule is the CutoffCopy of one site: the last CPTScheduleChanged
// applied, verbatim.
type CptSchedule struct {
	SiteID   shared.SiteID
	Timezone string
	Cutoffs  []CutoffEntry
	Version  int64
	Changed  time.Time
}

// Location is a facility-layout location of the StagingLocationCopy. The site
// is its zone's site, resolved through zoneId (never by parsing the code).
type Location struct {
	Code   string
	ZoneID string
	Role   string // "" means Storage (pre-role events)
}

// Zone is a facility-layout zone of the copy: the zone-to-site mapping.
type Zone struct {
	ZoneID   string
	SiteCode shared.SiteID
	ZoneCode string
}

// Place is a location joined with its zone. Site is empty while the zone has
// not been seen yet. IsStaging reports whether the location can hold a staged
// package (ErrNotStagingLocation otherwise).
type Place struct {
	Code   string
	Site   shared.SiteID
	ZoneID string
	Role   string
}

// IsStaging reports whether the place is a Staging location.
func (p Place) IsStaging() bool { return p.Role == RoleStaging }

// RoleStaging is the facility-layout role of a staging location.
const RoleStaging = "Staging"

// Appointment is the local copy of one OUTBOUND dock appointment
// (DoorFromAppointment). The carrier and SCAC are what the bill of lading
// certifies; cpt_id empty means the appointment names no cutoff.
type Appointment struct {
	AppointmentID shared.AppointmentID
	Site          shared.SiteID
	Direction     string
	DoorCode      shared.DoorCode
	CptID         shared.CptID
	Carrier       string
	CarrierScac   string
	WindowStart   time.Time
	WindowEnd     time.Time
	Version       int64
}

// DoorWorkCompletion is the DoorWorkCompleted copy row (yard fixture
// contract): when the yard finished the work at a door. No state change in
// v1; the load-adherence read model joins it.
type DoorWorkCompletion struct {
	TrailerID     shared.TrailerID
	Site          shared.SiteID
	DoorCode      shared.DoorCode
	AppointmentID shared.AppointmentID
	CompletedAt   time.Time
}

// LoadFilter narrows LoadRepo.List. Empty values mean "no filter".
type LoadFilter struct {
	Site          shared.SiteID
	State         outboundload.State
	OperatingDate outboundload.OperatingDate
}

// PackageFilter narrows PackageRepo.ListByLoad. Empty state means "no
// filter".
type PackageFilter struct {
	State string // "" = any
}

// LoadBoardRow is one row of the LoadBoardView projection: per load, the
// counts behind GET /reports/load-adherence and the dock supervisor's board.
type LoadBoardRow struct {
	LoadID         shared.LoadID
	Site           shared.SiteID
	Cpt            shared.CptID
	OperatingDate  outboundload.OperatingDate
	ShipMethod     shared.ShipMethod
	Cutoff         time.Time
	StagedCount    int64
	LoadedCount    int64
	PackagesMissed int64
	ClosedAt       time.Time
	ClosedOnTime   bool
	ConfirmedAt    time.Time
	CancelledAt    time.Time
}

// AdherenceRow is one (site, cpt, operating date) row of the load-adherence
// report: how many loads closed before their cutoff, how many packages
// missed, and the adherence share.
type AdherenceRow struct {
	Site           shared.SiteID
	Cpt            shared.CptID
	OperatingDate  outboundload.OperatingDate
	LoadsClosed    int64
	LoadsTotal     int64
	PackagesMissed int64
	Adherence      float64
}

// Sweepable is one EXPECTED or STAGED package whose still-open load's cutoff
// has passed, with that load's cutoff (the PackageMissedLoad payload's
// cutoff).
type Sweepable struct {
	Package *shippingpackage.ShippingPackage
	Cutoff  time.Time
}

// IdempotencyRecord is the stored answer of a POST made with an
// Idempotency-Key: 2xx and 4xx only, never 5xx. Fingerprint identifies the
// request body so the same key with a different body can be told apart.
type IdempotencyRecord struct {
	Key         string
	Fingerprint string
	Status      int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
}
