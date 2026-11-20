package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// problemBaseURI is the namespace of this service's RFC 7807 `type` URIs.
const problemBaseURI = "https://errors.outbound-shipping.warehouse-systems.dev/"

// replayClass is what the Idempotency-Key middleware does with the answer of a
// retried request (ADR 0012; fleet rule .claude/rules/fleet/idempotency-and-outbox.md).
// Every problem type declares exactly one; the middleware reads it and keeps no
// list of slugs.
//
//   - replayFinal: the answer is a function of the request alone (or of a state
//     that only moves one way for it). Stored under the key and replayed.
//   - replayOutcome: the command COMMITTED state and then answered an error that
//     must be remembered. Stored and replayed. No problem type of this service is
//     one: a 4xx rolls the whole unit of work back (ADR 0012), so there is no
//     committed state to remember, and a test pins that none is declared.
//   - replayRetryable: nothing committed and the answer depends on state that can
//     change (a lost optimistic lock, a fact an event has not delivered yet, a
//     state the aggregate has not reached, a dependency that is not ready). NOT
//     stored: the key is released and a retry executes again.
//
// When unsure between FINAL and RETRYABLE the class is RETRYABLE. A 5xx is never
// stored whatever its class says, and a refusal that reaches the middleware with
// no class is released like RETRYABLE.
type replayClass string

const (
	replayFinal     replayClass = "FINAL"
	replayOutcome   replayClass = "OUTCOME"
	replayRetryable replayClass = "RETRYABLE"
)

// problem is the fixed (status, slug, title, replay class) of one error
// category. detail, when set, replaces the error's own message (used for domain
// errors, whose text carries a package prefix and is never sent to a client as
// is). Build one with newProblem only (TestProblemsAreBuiltByTheConstructorOnly):
// a problem cannot exist without a class, and every problem is in the registry
// the tests read.
type problem struct {
	status int
	slug   string
	title  string
	detail string
	class  replayClass
}

// registry holds every problem built by newProblem, in declaration order.
var registry []problem

func newProblem(status int, slug, title string, class replayClass) problem {
	p := problem{status: status, slug: slug, title: title, class: class}
	registry = append(registry, p)
	return p
}

// withDetail is p with a fixed detail; it is the same problem type (same slug,
// status and class), so it is not registered again.
func (p problem) withDetail(detail string) problem {
	p.detail = detail
	return p
}

var internalProblem = newProblem(http.StatusInternalServerError, "internal-error", "Internal server error", replayRetryable)

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
// It answers them without touching its store, so nothing is ever stored for
// them; FINAL records that each is a function of the request (and of the stored
// fingerprint) alone.
var (
	idempotencyKeyRequired = newProblem(http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", replayFinal)
	idempotencyKeyReused   = newProblem(http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", replayFinal)
)

// The problems the handlers write directly (a path or body they cannot read);
// each is one problem type whatever the detail. Both are a function of the
// request alone: FINAL.
var (
	badRequest        = newProblem(http.StatusBadRequest, "bad-request", "Malformed request", replayFinal)
	invalidParameter  = newProblem(http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", replayFinal)
	unknownQueryParam = newProblem(http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter", replayFinal)
)

// problemCatalogue maps every typed error to its problem, in match order.
// Slugs are the catalogue of apis/openapi.yaml (and .claude/rules/rest-api.md).
// The replay class of each is declared on its problem, with the reason in ADR
// 0012 (a test holds the ADR table equal to this catalogue). Entries that share a
// slug share one class.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, badRequest},
	{errInvalidParameter, invalidParameter},
	{errUnknownQueryParameter, unknownQueryParam},
	{usecases.ErrInvalidListQuery, invalidParameter},

	// Package refusals.
	{shippingpackage.ErrNotStagingLocation, newProblem(http.StatusUnprocessableEntity, "not-staging-location", "The location is not a Staging location of the package's site", replayRetryable)},
	{shippingpackage.ErrWrongLoad, newProblem(http.StatusConflict, "wrong-load", "The package loads only onto its own load, through that load's door", replayRetryable)},
	{shippingpackage.ErrPackageOnAnotherLoad, newProblem(http.StatusConflict, "package-on-another-load", "The package belongs to one load at a time", replayRetryable)},
	{shippingpackage.ErrAlreadyShipped, newProblem(http.StatusConflict, "already-shipped", "The package has shipped; the truck has left", replayFinal)},
	{shippingpackage.ErrInvalidTransition, newProblem(http.StatusConflict, "package-state", "The command is not allowed from the package's current state", replayRetryable)},

	// Load refusals.
	{outboundload.ErrPackagesOutstanding, newProblem(http.StatusConflict, "packages-outstanding", "A load closes only when every package is loaded or short-shipped with a reason", replayRetryable)},
	{outboundload.ErrTrailerNotDocked, newProblem(http.StatusConflict, "trailer-not-docked", "Loading starts only when the load has a door and a docked trailer", replayRetryable)},
	{outboundload.ErrLoadNotClosed, newProblem(http.StatusConflict, "load-not-closed", "Ship-confirm needs a CLOSED load", replayRetryable)},
	{outboundload.ErrLoadConfirmed, newProblem(http.StatusConflict, "load-confirmed", "A SHIP_CONFIRMED load never changes", replayFinal)},
	{outboundload.ErrDuplicateOpenLoad, newProblem(http.StatusConflict, "duplicate-open-load", "An open load already exists for this site, cpt, operating date and ship method", replayRetryable)},
	{outboundload.ErrInvalidTransition, newProblem(http.StatusConflict, "load-state", "The command is not allowed from the load's current state", replayRetryable)},
	{outboundload.ErrCancelReasonRequired, badRequest.withDetail("a cancellation needs a reason")},
	{outboundload.ErrWrongDoor, newProblem(http.StatusConflict, "trailer-not-docked", "The trailer must dock at the load's assigned door", replayRetryable)},
	// A short-ship entry without a package or a reason is a bad body, not a
	// server fault (openapi ShortShip: package_id and reason, minLength 1).
	{shared.ErrShortShipReasonRequired, badRequest.withDetail("every short-ship entry needs a package_id and a reason")},
	{shared.ErrShortShipPackageRequired, badRequest.withDetail("every short-ship entry needs a package_id and a reason")},
	{shippingpackage.ErrShortShipReasonRequired, badRequest.withDetail("every short-ship entry needs a package_id and a reason")},
	{outboundload.ErrInvalidShortShip, badRequest.withDetail("every short-ship entry needs a package_id and a reason")},
	{outboundload.ErrRequiredField, badRequest},
	{outboundload.ErrNoTimestamp, badRequest},
	{usecases.ErrLoadHasPackages, newProblem(http.StatusConflict, "load-has-packages", "The load already has packages assigned", replayRetryable).withDetail("short-ship them and close instead of cancelling")},

	// Bill of lading refusals.
	{billoflading.ErrBolAlreadyIssued, newProblem(http.StatusConflict, "bol-already-issued", "A bill of lading exists for this load already", replayRetryable)},
	{billoflading.ErrBolAlreadyVoided, newProblem(http.StatusConflict, "bol-already-voided", "The bill of lading is voided already", replayFinal)},
	{billoflading.ErrSealMissing, newProblem(http.StatusConflict, "seal-missing", "The bill of lading is issued only with the seal yard-management recorded", replayRetryable)},
	{billoflading.ErrVoidReasonRequired, badRequest},

	// Not-found errors: the ids are minted by events from other contexts, not by
	// a request, so the fact can arrive later (ADR 0012, J1).
	{repository.ErrLoadNotFound, newProblem(http.StatusNotFound, "load-not-found", "Outbound load not found", replayRetryable)},
	{repository.ErrPackageNotFound, newProblem(http.StatusNotFound, "package-not-found", "Shipping package not found", replayRetryable)},
	{repository.ErrBolNotFound, newProblem(http.StatusNotFound, "bol-not-found", "Bill of lading not found", replayRetryable)},

	// Concurrency.
	{repository.ErrConcurrentModification, newProblem(http.StatusConflict, "concurrent-modification", "The resource was modified by another request; re-fetch the latest version and retry", replayRetryable)},
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
