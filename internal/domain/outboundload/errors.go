package outboundload

import "errors"

// The invariant errors of the OutboundLoad aggregate, named exactly as the
// domain model names them (model.yaml invariants).
var (
	// ErrDuplicateOpenLoad is returned by Plan when an open load already
	// exists for the same (site, cpt, operating date, ship method). The
	// rule spans loads, so the use case passes the open loads' keys and a
	// partial unique index backs it (PLAN wave 5).
	ErrDuplicateOpenLoad = errors.New("outboundload: an open load already exists for this site, cutoff, date and ship method")

	// ErrTrailerNotDocked is returned by StartLoading when the load has no
	// assigned door or no docked trailer attached: loading starts only
	// against the physical dock fact.
	ErrTrailerNotDocked = errors.New("outboundload: loading starts only when the load has a door and a docked trailer")

	// ErrPackagesOutstanding is returned by CloseLoad when any assigned
	// package is neither loaded nor short-shipped with a reason.
	ErrPackagesOutstanding = errors.New("outboundload: a load closes only when every package is loaded or short-shipped")

	// ErrLoadNotClosed is returned by ConfirmShipment when the load is not
	// CLOSED — including a departure that arrives while still LOADING
	// (ADR 0002: the load is never confirmed then).
	ErrLoadNotClosed = errors.New("outboundload: ship-confirm needs a CLOSED load")

	// ErrLoadConfirmed is returned by every command on a SHIP_CONFIRMED
	// load: it never changes.
	ErrLoadConfirmed = errors.New("outboundload: a SHIP_CONFIRMED load never changes")
)

// Errors the model does not name but the transitions table implies.
var (
	// ErrInvalidTransition is returned when the command is not allowed from
	// the load's current state (the model's transitions table).
	ErrInvalidTransition = errors.New("outboundload: this command is not allowed from the load's current state")

	// ErrRequiredField is returned by Plan when a cutoff-key field is
	// missing.
	ErrRequiredField = errors.New("outboundload: site, cpt, operating date, ship method and cutoff are required")

	// ErrNoTimestamp is returned when a command's instant is missing.
	ErrNoTimestamp = errors.New("outboundload: the command instant is required")

	// ErrWrongDoor is returned by AttachTrailer when the trailer docked at
	// a door that is not the load's assigned door.
	ErrWrongDoor = errors.New("outboundload: the trailer docked at another door than the load's")

	// ErrCancelReasonRequired is returned by CancelLoad without a reason.
	ErrCancelReasonRequired = errors.New("outboundload: cancelling a load needs a reason")

	// ErrInvalidShortShip is returned by CloseLoad when a short-ship entry
	// is incomplete.
	ErrInvalidShortShip = errors.New("outboundload: every short-ship entry needs a package and a reason")

	// ErrInvalidOperatingDate is returned when an operating date is not a
	// YYYY-MM-DD calendar date.
	ErrInvalidOperatingDate = errors.New("outboundload: the operating date must be a YYYY-MM-DD date")

	// ErrInvalidLoadID is returned by Rehydrate when the snapshot names no
	// load.
	ErrInvalidLoadID = errors.New("outboundload: the snapshot needs a load id")

	// ErrInvalidState is returned by Rehydrate when the snapshot's state is
	// not one of the model's five states.
	ErrInvalidState = errors.New("outboundload: unknown load state")

	// ErrInvalidVersion is returned by Rehydrate when the snapshot's
	// version is below 1.
	ErrInvalidVersion = errors.New("outboundload: the version must be at least 1")
)
