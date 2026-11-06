package billoflading

import "errors"

// The invariant errors of the BillOfLading aggregate, named exactly as the
// domain model names them (model.yaml invariants).
var (
	// ErrBolAlreadyIssued is returned by Issue when a bill of lading
	// already exists for the load, or the load is not SHIP_CONFIRMED: the
	// model names one error for the whole "issued only for a SHIP_CONFIRMED
	// load, exactly once per load" rule. The wrapped message says which
	// half refused.
	ErrBolAlreadyIssued = errors.New("billoflading: a bill of lading is issued only for a ship-confirmed load, exactly once")

	// ErrSealMissing is returned by Issue without the seal number
	// yard-management recorded on departure.
	ErrSealMissing = errors.New("billoflading: a bill of lading is issued only with the seal number yard-management recorded")
)

// Errors the model does not name but the lifecycle implies.
var (
	// ErrBolAlreadyVoided is returned by Void on an already-voided
	// document.
	ErrBolAlreadyVoided = errors.New("billoflading: the bill of lading is already voided")

	// ErrVoidReasonRequired is returned by Void without a reason.
	ErrVoidReasonRequired = errors.New("billoflading: voiding a bill of lading needs a reason")

	// ErrRequiredField is returned by Issue or Rehydrate when an identity
	// field is missing.
	ErrRequiredField = errors.New("billoflading: bol number, load, site, carrier and trailer are required")

	// ErrNoTimestamp is returned when a command's instant is missing.
	ErrNoTimestamp = errors.New("billoflading: the command instant is required")

	// ErrInvalidState is returned by Rehydrate when the snapshot's state is
	// not ISSUED or VOIDED.
	ErrInvalidState = errors.New("billoflading: unknown bill of lading state")

	// ErrInvalidVersion is returned by Rehydrate when the snapshot's
	// version is below 1.
	ErrInvalidVersion = errors.New("billoflading: the version must be at least 1")

	// ErrInvalidPackageCount is returned by Issue with a package count
	// below 1: a document certifies at least one package.
	ErrInvalidPackageCount = errors.New("billoflading: the bill of lading certifies at least one package")

	// ErrInvalidWeight is returned by Issue with a negative total weight.
	ErrInvalidWeight = errors.New("billoflading: the total weight cannot be negative")
)
