package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// problemBaseURI is the namespace of this service's RFC 7807 `type` URIs.
const problemBaseURI = "https://errors.outbound-shipping.warehouse-systems.dev/"

// problem is the fixed (status, slug, title) of one error category. detail,
// when set, replaces the error's own message (used for domain errors, whose
// text carries a package prefix and is never sent to a client as is).
type problem struct {
	status int
	slug   string
	title  string
	detail string
}

var internalProblem = problem{status: http.StatusInternalServerError, slug: "internal-error", title: "Internal server error"}

// Errors the adapter itself raises, before or besides the use cases.
var (
	// errMalformedRequest marks a body the adapter rejects (bad JSON, wrong
	// type, trailing data, empty required body).
	errMalformedRequest = errors.New("request body is not valid JSON for this operation")
	// errUnknownQueryParameter marks a query parameter a route does not declare.
	errUnknownQueryParameter = errors.New("unknown query parameter")
	// errInvalidParameter marks a path or query value outside the OpenAPI
	// bounds (blank, wrong enum, wrong date).
	errInvalidParameter = errors.New("parameter is invalid")
)

// Problems the idempotency middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{status: http.StatusBadRequest, slug: "idempotency-key-required", title: "Idempotency-Key header is required"}
	idempotencyKeyReused   = problem{status: http.StatusConflict, slug: "idempotency-key-reused", title: "Idempotency-Key was already used with a different request"}
)

// problemCatalogue maps every typed error to its problem, in match order.
// Slugs are the catalogue of apis/openapi.yaml (and .claude/rules/rest-api.md).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{status: http.StatusBadRequest, slug: "bad-request", title: "Malformed request"}},
	{errInvalidParameter, problem{status: http.StatusBadRequest, slug: "invalid-parameter", title: "Parameter is invalid"}},
	{errUnknownQueryParameter, problem{status: http.StatusBadRequest, slug: "unknown-query-parameter", title: "Unknown query parameter"}},
	{usecases.ErrInvalidListQuery, problem{status: http.StatusBadRequest, slug: "invalid-parameter", title: "Parameter is invalid"}},

	// Package refusals.
	{shippingpackage.ErrNotStagingLocation, problem{status: http.StatusUnprocessableEntity, slug: "not-staging-location", title: "The location is not a Staging location of the package's site"}},
	{shippingpackage.ErrWrongLoad, problem{status: http.StatusConflict, slug: "wrong-load", title: "The package loads only onto its own load, through that load's door"}},
	{shippingpackage.ErrPackageOnAnotherLoad, problem{status: http.StatusConflict, slug: "package-on-another-load", title: "The package belongs to one load at a time"}},
	{shippingpackage.ErrAlreadyShipped, problem{status: http.StatusConflict, slug: "already-shipped", title: "The package has shipped; the truck has left"}},
	{shippingpackage.ErrInvalidTransition, problem{status: http.StatusConflict, slug: "package-state", title: "The command is not allowed from the package's current state"}},

	// Load refusals.
	{outboundload.ErrPackagesOutstanding, problem{status: http.StatusConflict, slug: "packages-outstanding", title: "A load closes only when every package is loaded or short-shipped with a reason"}},
	{outboundload.ErrTrailerNotDocked, problem{status: http.StatusConflict, slug: "trailer-not-docked", title: "Loading starts only when the load has a door and a docked trailer"}},
	{outboundload.ErrLoadNotClosed, problem{status: http.StatusConflict, slug: "load-not-closed", title: "Ship-confirm needs a CLOSED load"}},
	{outboundload.ErrLoadConfirmed, problem{status: http.StatusConflict, slug: "load-confirmed", title: "A SHIP_CONFIRMED load never changes"}},
	{outboundload.ErrDuplicateOpenLoad, problem{status: http.StatusConflict, slug: "duplicate-open-load", title: "An open load already exists for this site, cpt, operating date and ship method"}},
	{outboundload.ErrInvalidTransition, problem{status: http.StatusConflict, slug: "load-state", title: "The command is not allowed from the load's current state"}},
	{outboundload.ErrCancelReasonRequired, problem{status: http.StatusBadRequest, slug: "bad-request", title: "Malformed request", detail: "a cancellation needs a reason"}},
	{outboundload.ErrWrongDoor, problem{status: http.StatusConflict, slug: "trailer-not-docked", title: "The trailer must dock at the load's assigned door"}},
	{outboundload.ErrRequiredField, problem{status: http.StatusBadRequest, slug: "bad-request", title: "Malformed request"}},
	{outboundload.ErrNoTimestamp, problem{status: http.StatusBadRequest, slug: "bad-request", title: "Malformed request"}},
	{usecases.ErrLoadHasPackages, problem{status: http.StatusConflict, slug: "load-has-packages", title: "The load already has packages assigned", detail: "short-ship them and close instead of cancelling"}},

	// Bill of lading refusals.
	{billoflading.ErrBolAlreadyIssued, problem{status: http.StatusConflict, slug: "bol-already-issued", title: "A bill of lading exists for this load already"}},
	{billoflading.ErrBolAlreadyVoided, problem{status: http.StatusConflict, slug: "bol-already-voided", title: "The bill of lading is voided already"}},
	{billoflading.ErrSealMissing, problem{status: http.StatusConflict, slug: "seal-missing", title: "The bill of lading is issued only with the seal yard-management recorded"}},
	{billoflading.ErrVoidReasonRequired, problem{status: http.StatusBadRequest, slug: "bad-request", title: "Malformed request"}},

	// Not-found errors.
	{repository.ErrLoadNotFound, problem{status: http.StatusNotFound, slug: "load-not-found", title: "Outbound load not found"}},
	{repository.ErrPackageNotFound, problem{status: http.StatusNotFound, slug: "package-not-found", title: "Shipping package not found"}},
	{repository.ErrBolNotFound, problem{status: http.StatusNotFound, slug: "bol-not-found", title: "Bill of lading not found"}},

	// Concurrency.
	{repository.ErrConcurrentModification, problem{status: http.StatusConflict, slug: "concurrent-modification", title: "The resource was modified by another request; re-fetch the latest version and retry"}},
}

// problemFor returns the problem of the first catalogue entry err matches,
// or the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
