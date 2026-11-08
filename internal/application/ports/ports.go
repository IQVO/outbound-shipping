// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary
// (errors, filters, value types) lives in internal/application/repository.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// Clock is the service's notion of "now". Use cases stamp commands and events
// with it.
type Clock interface {
	Now() time.Time
}

// LoadIDs mints the aggregate ids this service creates (loads and bills of
// lading); the production adapter lives in internal/adapters/outbound/system.
type LoadIDs interface {
	// NewLoadID returns a fresh "load-" id (at most 64 characters).
	NewLoadID() shared.LoadID
	// NewBolNumber returns a fresh "BOL-" number for one operating date (at
	// most 64 characters).
	NewBolNumber(date outboundload.OperatingDate) shared.BolNumber
}

// LoadRepo persists OutboundLoad aggregates by load id. Inside a UnitOfWork
// it joins the transaction carried in ctx.
type LoadRepo interface {
	// Get returns the load, or repository.ErrLoadNotFound.
	Get(ctx context.Context, id shared.LoadID) (*outboundload.OutboundLoad, error)
	// Save persists l guarded by the version the caller loaded: loadedVersion
	// 0 inserts (a unique violation on uq_open_load_per_cutoff is
	// repository.ErrDuplicateOpenLoad); any other value updates only when the
	// stored version still equals loadedVersion (else
	// repository.ErrConcurrentModification).
	Save(ctx context.Context, l *outboundload.OutboundLoad, loadedVersion int64) error
	// FindOpenByKey returns the open load of the one-open-load key, if any.
	FindOpenByKey(ctx context.Context, key outboundload.LoadKey) (*outboundload.OutboundLoad, error)
	// OpenLoadKeys returns the cutoff keys of the site's open loads.
	OpenLoadKeys(ctx context.Context, site shared.SiteID) ([]outboundload.LoadKey, error)
	// ListOpenByCpt returns the site's open loads of one cpt, ordered by
	// cutoff (DoorFromAppointment).
	ListOpenByCpt(ctx context.Context, site shared.SiteID, cpt shared.CptID) ([]*outboundload.OutboundLoad, error)
	// ListOpenByDoor returns the site's open loads assigned to a door,
	// PLANNED first (StartOnDock).
	ListOpenByDoor(ctx context.Context, site shared.SiteID, door shared.DoorCode) ([]*outboundload.OutboundLoad, error)
	// FindLatestByTrailer returns the site's load a trailer was last attached
	// to (SHIP_CONFIRMED included), or repository.ErrLoadNotFound
	// (ConfirmOnDeparture).
	FindLatestByTrailer(ctx context.Context, site shared.SiteID, trailer shared.TrailerID) (*outboundload.OutboundLoad, error)
	// List returns up to limit loads newest first (created_at, then id,
	// descending) that come after the load `after` in that order ("" = from
	// the start), narrowed by filter.
	List(ctx context.Context, filter repository.LoadFilter, after shared.LoadID, limit int) ([]*outboundload.OutboundLoad, error)
}

// PackageRepo persists ShippingPackage aggregates by package id. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type PackageRepo interface {
	// Get returns the package, or repository.ErrPackageNotFound.
	Get(ctx context.Context, id shared.PackageID) (*shippingpackage.ShippingPackage, error)
	// Save persists p guarded by the version the caller loaded:
	// loadedVersion 0 inserts (an existing id is
	// repository.ErrConcurrentModification); any other value updates only
	// when the stored version still equals loadedVersion.
	Save(ctx context.Context, p *shippingpackage.ShippingPackage, loadedVersion int64) error
	// ListByLoad returns up to limit packages of the load ordered by package
	// id that come after `after` ("" = from the start), narrowed by filter.
	ListByLoad(ctx context.Context, load shared.LoadID, filter repository.PackageFilter, after shared.PackageID, limit int) ([]*shippingpackage.ShippingPackage, error)
	// ListSweepable returns the site's EXPECTED or STAGED packages whose
	// load's cutoff is at or before at, on still-open loads, with that
	// load's cutoff, ordered by package id (SweepMissedLoad).
	ListSweepable(ctx context.Context, site shared.SiteID, at time.Time) ([]repository.Sweepable, error)
	// ListMissed returns the site's packages that raised PackageMissedLoad,
	// newest first by flagged time (the missed-loads MCP read).
	ListMissed(ctx context.Context, site shared.SiteID, limit int) ([]*shippingpackage.ShippingPackage, error)
}

// BolRepo persists BillOfLading aggregates by BOL number. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type BolRepo interface {
	// Get returns the document, or repository.ErrBolNotFound.
	Get(ctx context.Context, id shared.BolNumber) (*billoflading.BillOfLading, error)
	// GetByLoad returns the load's document, if any (the
	// ErrBolAlreadyIssued pre-check).
	GetByLoad(ctx context.Context, load shared.LoadID) (*billoflading.BillOfLading, error)
	// Save persists b guarded by the version the caller loaded
	// (loadedVersion 0 inserts).
	Save(ctx context.Context, b *billoflading.BillOfLading, loadedVersion int64) error
}

// CptCopy is the local copy of process-path-management's CPTScheduleChanged
// (the model's CutoffCopy read model), one row per site, last writer wins.
// Inside a UnitOfWork it joins the transaction carried in ctx.
type CptCopy interface {
	// Get returns the site's schedule, or repository.ErrCptScheduleUnknown.
	Get(ctx context.Context, site shared.SiteID) (repository.CptSchedule, error)
	// Save replaces the site's schedule wholesale.
	Save(ctx context.Context, s repository.CptSchedule) error
}

// LayoutCopy is the local copy of facility-layout's zones and locations (the
// model's StagingLocationCopy read model). Inside a UnitOfWork it joins the
// transaction carried in ctx.
type LayoutCopy interface {
	// SaveZone upserts a zone.
	SaveZone(ctx context.Context, z repository.Zone) error
	// SaveLocation upserts a location.
	SaveLocation(ctx context.Context, l repository.Location) error
	// Place returns the location joined with its zone; false when the
	// location was never seen.
	Place(ctx context.Context, code string) (repository.Place, bool, error)
}

// AppointmentCopy is the local copy of the OUTBOUND dock appointments acted
// on (DoorFromAppointment): the carrier and SCAC the bill of lading
// certifies, read back at ship-confirm by appointment id. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type AppointmentCopy interface {
	// Save upserts the appointment.
	Save(ctx context.Context, a repository.Appointment) error
	// Get returns the appointment, or repository.ErrBolNotFound-free false.
	Get(ctx context.Context, id shared.AppointmentID) (repository.Appointment, bool, error)
}

// DoorWorkCopy records yard DoorWorkCompleted facts (fixture contract). No
// state change in v1; the load-adherence read model joins them. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type DoorWorkCopy interface {
	// Record stores one completion (idempotent per (trailer, completed_at)).
	Record(ctx context.Context, c repository.DoorWorkCompletion) error
}

// BoardStore is the projector-fed LoadBoardView read model (the analytics
// projection): per load the counts behind GET /reports/load-adherence.
type BoardStore interface {
	// Report returns the adherence rows of the scope, ordered by site,
	// operating date, cpt. Empty filters mean "everything".
	Report(ctx context.Context, site shared.SiteID, date outboundload.OperatingDate) ([]repository.AdherenceRow, error)
	// RowsAtRisk returns the still-open loads of the scope whose cutoff is
	// within horizon of now, ordered by cutoff, with their outstanding
	// package counts (the loads-at-risk MCP read).
	RowsAtRisk(ctx context.Context, site shared.SiteID, now time.Time, horizon time.Duration, limit int) ([]repository.LoadBoardRow, error)
}

// EventSink is the write side of the transactional outbox, in domain terms.
// Called with the ctx handed to UnitOfWork.Do it joins the SAME transaction
// as the aggregate's Save, so the state change and its events commit or roll
// back together. The outbox adapter maps the facts to CloudEvents (ADR 0005).
type EventSink interface {
	// PublishLoadEvents enqueues the facts an OutboundLoad raised.
	PublishLoadEvents(ctx context.Context, events ...outboundload.Event) error
	// PublishPackageEvents enqueues the facts a ShippingPackage raised.
	PublishPackageEvents(ctx context.Context, events ...shippingpackage.Event) error
	// PublishBolEvents enqueues the facts a BillOfLading raised.
	PublishBolEvents(ctx context.Context, events ...billoflading.Event) error
}

// ProcessedEvents is the inbound-consumer idempotency guard (the inbox).
// Claim records (consumer, CloudEvents id) INSIDE the same UnitOfWork as the
// event's effect and reports false when an earlier committed handling already
// claimed it (the caller then skips).
type ProcessedEvents interface {
	Claim(ctx context.Context, consumer, eventID string) (claimed bool, err error)
}

// IdempotencyStore keeps the answers of POSTs made with an Idempotency-Key.
// The HTTP adapter stores 2xx and 4xx answers only, never 5xx.
type IdempotencyStore interface {
	// Lookup returns the stored answer for the key.
	Lookup(ctx context.Context, key string) (repository.IdempotencyRecord, bool, error)
	// Save stores the answer; the first writer of a key wins and a later Save
	// of the same key changes nothing.
	Save(ctx context.Context, rec repository.IdempotencyRecord) error
}
