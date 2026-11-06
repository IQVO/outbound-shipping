package shippingpackage

import "errors"

// The invariant errors of the ShippingPackage aggregate, named exactly as
// the domain model names them (model.yaml invariants).
var (
	// ErrNotStagingLocation is returned by Stage when the location is not a
	// facility-layout Staging location of the package's site.
	ErrNotStagingLocation = errors.New("shippingpackage: a package stages only at a Staging location of its site")

	// ErrWrongLoad is returned by Load when the package loads onto another
	// load than its own, or through a door that is not its load's door.
	ErrWrongLoad = errors.New("shippingpackage: a package loads only onto its own load, through that load's door")

	// ErrPackageOnAnotherLoad is returned by AssignToLoad when the package
	// already belongs to a different load: one load at a time.
	ErrPackageOnAnotherLoad = errors.New("shippingpackage: a package belongs to one load at a time")

	// ErrAlreadyShipped is returned by Unload (and Load) once the package
	// has shipped: the truck has left.
	ErrAlreadyShipped = errors.New("shippingpackage: a package unloads only before its load is ship-confirmed")
)

// Errors the model does not name but the transitions table implies.
var (
	// ErrInvalidTransition is returned when the command is not allowed from
	// the package's current state (the model's transitions table).
	ErrInvalidTransition = errors.New("shippingpackage: this command is not allowed from the package's current state")

	// ErrRequiredField is returned when an identity or input field is
	// missing.
	ErrRequiredField = errors.New("shippingpackage: package id and site are required")

	// ErrNoTimestamp is returned when a command's instant is missing.
	ErrNoTimestamp = errors.New("shippingpackage: the command instant is required")

	// ErrShortShipReasonRequired is returned by MarkShortShipped without a
	// reason: a package is never silently left off a load.
	ErrShortShipReasonRequired = errors.New("shippingpackage: short-shipping a package needs a reason")

	// ErrInvalidState is returned by Rehydrate when the snapshot's state is
	// not one of the model's five states.
	ErrInvalidState = errors.New("shippingpackage: unknown package state")

	// ErrInvalidVersion is returned by Rehydrate when the snapshot's
	// version is below 1.
	ErrInvalidVersion = errors.New("shippingpackage: the version must be at least 1")

	// ErrLoadRequired is returned by Load, Unload and FlagMissedLoad when
	// the package has no load: those commands need the assignment first.
	ErrLoadRequired = errors.New("shippingpackage: the package has no load")
)
