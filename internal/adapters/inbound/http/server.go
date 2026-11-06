// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 7807 problem documents for every error, the
// Idempotency-Key middleware of the POSTs and the liveness/readiness
// endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
)

// Server holds the use cases the handlers call.
type Server struct {
	Stage   *usecases.StagePackage
	Load    *usecases.LoadPackage
	Unload  *usecases.UnloadPackage
	Close   *usecases.CloseLoad
	Reopen  *usecases.ReopenLoad
	Cancel  *usecases.CancelLoad
	VoidBol *usecases.VoidBillOfLading
	Sweep   *usecases.SweepMissedLoads
	Queries *usecases.Queries

	// Idempotency wraps every POST (the fleet Idempotency-Key middleware,
	// Idempotency.Middleware, built by cmd/ over the Postgres adapters); nil
	// leaves the routes unwrapped (handler unit tests only: cmd/ always
	// wires it).
	Idempotency func(http.Handler) http.Handler
	// Readiness backs GET /readyz; nil means always ready.
	Readiness *Readiness
}

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml
// (.claude/rules/rest-api.md).
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(recoverPanic)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.Get("/loads", s.handleListLoads)
	r.Get("/loads/{loadId}", s.handleGetLoad)
	r.Get("/loads/{loadId}/packages", s.handleListLoadPackages)
	r.Get("/bills-of-lading/{bolNumber}", s.handleGetBol)
	r.Get("/reports/load-adherence", s.handleLoadAdherence)

	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/packages/{packageId}/stagings", s.handleStagePackage)
		r.Post("/packages/{packageId}/loadings", s.handleLoadPackage)
		r.Post("/packages/{packageId}/unloadings", s.handleUnloadPackage)
		r.Post("/loads/{loadId}/closures", s.handleCloseLoad)
		r.Post("/loads/{loadId}/reopenings", s.handleReopenLoad)
		r.Post("/loads/{loadId}/cancellations", s.handleCancelLoad)
		r.Post("/bills-of-lading/{bolNumber}/voids", s.handleVoidBol)
		r.Post("/missed-load-sweeps", s.handleMissedLoadSweep)
	})
	return r
}
