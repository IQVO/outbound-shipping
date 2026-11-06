// Package usecases holds the application's use cases and policies
// (model.yaml): the REST commands, the consumer policies (PlanLoadFromCutoff,
// AssignPackageToLoad, DoorFromAppointment, StartOnDock, ConfirmOnDeparture,
// FlagDepartureWithoutClose, SweepMissedLoad, DropDiverted) and the queries.
// Every use case runs its effect inside ONE ports.UnitOfWork, so a command's
// aggregate writes, its outbox rows and its inbox claim commit or roll back
// together.
package usecases

import "errors"

// Consumer names namespace the claims of the inbound consumers in
// ports.ProcessedEvents. They are not Kafka consumer group ids.
const (
	FulfillmentConsumer = "outbound-shipping-fulfillment"
	CptConsumer         = "outbound-shipping-cpt"
	LayoutConsumer      = "outbound-shipping-layout"
	DockConsumer        = "outbound-shipping-dock"
	YardConsumer        = "outbound-shipping-yard"
	ProjectorConsumer   = "outbound-shipping-projector"
)

// ErrInvalidEvent marks a consumed event that can never be applied: a
// deterministic problem the consumer dead-letters, never retries.
var ErrInvalidEvent = errors.New("usecases: event cannot be applied")

// ErrLoadHasPackages is returned by CancelLoad when packages are already
// assigned to the load: cancelling would strand them on a dead load with no
// path to any other (the model leaves re-planning unmodelled).
var ErrLoadHasPackages = errors.New("usecases: the load already has packages assigned; short-ship them and close instead of cancelling")

// ErrInvalidListQuery marks an invalid list parameter (limit, cursor, filter
// value).
var ErrInvalidListQuery = errors.New("usecases: invalid list query")

// Outcome reports what a consumer use case did.
type Outcome string

// The outcomes.
const (
	// OutcomeApplied: the effect ran (a load planned, a package expected...).
	OutcomeApplied Outcome = "applied"
	// OutcomeDuplicate: this CloudEvents id was already processed.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeIgnored: the event is not for this service right now (a
	// manifest without a site, an appointment naming no cpt, a docking at a
	// door no load uses); nothing changed. The consumer logs it and moves on.
	OutcomeIgnored Outcome = "ignored"
)
