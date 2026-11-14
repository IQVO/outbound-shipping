// Package http is the REST inbound adapter: every route of apis/openapi.yaml
// on a chi router, RFC 7807 problem documents for every error, the
// Idempotency-Key middleware of the POSTs and the liveness/readiness
// endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/outbound-shipping/internal/adapters/inbound/requestlog"
	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
)

// DefaultServiceName names the server in spans and metrics when the
// composition root passes none.
const DefaultServiceName = "outbound-shipping"

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
	// Metrics records the Tier 2 business counter of the close command
	// (ports.LoadMetrics); nil means not instrumented.
	Metrics ports.LoadMetrics
}

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml
// (.claude/rules/rest-api.md).
//
// The middleware order is the fleet's Tier 1 standard (ADR 0008):
// RequestID, otelchi (server span named after the route pattern),
// otelchimetric (http.server.request.duration and
// http.server.active_requests), RequestLogger, then the panic recoverer.
// otelchi runs before RequestLogger so every access-log record is written
// under the request's span and carries its trace_id/span_id. A nil logger
// falls back to slog.Default(); an empty serviceName to DefaultServiceName.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	metricCfg := otelchimetric.NewBaseConfig(serviceName)
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(metricCfg))
	r.Use(otelchimetric.NewServerActiveRequests(metricCfg))
	r.Use(requestlog.RequestLogger(logger))
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
