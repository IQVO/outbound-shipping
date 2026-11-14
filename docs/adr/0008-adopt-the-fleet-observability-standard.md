# ADR 0008: Adopt the fleet observability standard (OpenTelemetry Tier 1 and one Tier 2 counter)

## Status

Accepted (2026-11-13). Applies the fleet's standard metrics convention (inventory-storage ADR 0016, wes-work-planning
ADR 0013) to this service. Supersedes nothing: until now the service had no OpenTelemetry setup at all.

## Context

`cmd/api` and `cmd/mcp` installed no tracer provider, no meter provider and no exporter, and read no `OTEL_*` variable; the
router was a plain `chi` router with no request log. The fleet pipeline (service pods, OTLP/gRPC to the Collector, then
Jaeger and a Prometheus exporter) therefore saw nothing of this service, and the generated dashboard's HTTP RED and Go
runtime rows stayed empty. The one OpenTelemetry touch point was `OutboxRepo.Insert`
(`internal/adapters/outbound/postgres/outbox_repository.go`), which already injects the W3C trace context of its `ctx` into
each outbox row's `headers` column, and `RelaySink` (`internal/adapters/outbound/kafka/relay_sink.go`) already copies the
stored headers onto the Kafka message. With no propagator installed those headers were empty.

## Decision

**Tier 1, identical to every other service.**

- `internal/adapters/outbound/telemetry` owns `Setup` (OTLP/gRPC trace and metric exporters, the W3C trace-context and
  baggage propagator, Go runtime metrics via `runtime.Start`) and `WithTraceContext`, the slog handler wrapper that stamps
  `trace_id` and `span_id` on records written with a context that carries a span. Both binaries build their logger with it.
- **An empty OTLP endpoint means telemetry is off.** `Setup` then installs nothing and returns a no-op flush. There is no
  `localhost:4317` default (inventory-storage's `Setup`, the copy source, has one): the chart renders `OTEL_EXPORTER_OTLP_ENDPOINT` only while
  `otel.enabled`, so a pod that was not given a Collector does not dial one. With an endpoint, export is non-blocking: a
  Collector that is down costs telemetry, never availability or startup.
- One `telemetry.go` per long-running binary: `cmd/api/telemetry.go` and `cmd/mcp/telemetry.go`. The repo has no other
  long-running binary on `develop` (no a2a, projector or reports process; the relay and the projector consumer run inside
  `cmd/api`).
- Middleware order of both HTTP routers: `RequestID`, `otelchi.Middleware`, `otelchimetric.NewServerRequestDuration`
  (plus `NewServerActiveRequests`), `RequestLogger`, then the panic recoverer (the REST router keeps its own `recoverPanic`,
  which writes the RFC 7807 500; the MCP router uses chi's `Recoverer`). `GET /healthz` is unchanged.
- `RequestLogger` lives in its own package, `internal/adapters/inbound/requestlog`, not in `inbound/http`: `cmd/mcp` is the
  read-only MCP binary and must be able to share the access log without importing the REST adapter (fleet convention; no
  fitness test of this repository names the `inbound/http` path, so this is kept by the package split, not by a sensor).
- `service.name` is `OTEL_SERVICE_NAME`, default `outbound-shipping` for `cmd/api` and `outbound-shipping-mcp` for
  `cmd/mcp`, so the two binaries never share a metric series. The chart renders both (the mcp one with a `-mcp` suffix).

**Tier 2: one counter, on closing a load.** The instrument is `outbound.loads.closures` (unit `{closure}`), recorded
through `ports.LoadMetrics` (`internal/application/ports/metrics.go`), implemented by
`internal/adapters/outbound/telemetry/load_metrics.go`, with attributes `outcome` (`closed` or `failed`) and, on failure,
`reason` (the RFC 7807 problem slug the client received, a bounded catalogue). A nil port is a no-op and nothing in
`internal/domain` or `internal/application` calls it.

Why this command: the context exists to prove what left on which trailer, and ship-confirm follows the physical
departure (ADR 0002). The operator command that gates it is the supervisor's close
(`POST /loads/{loadId}/closures`): ship-confirm needs a CLOSED load, its main refusal is `packages-outstanding`, and the
load-adherence report counts `loads_closed / loads_total`. The ship-confirm itself is a consumer policy (`OnDeparture`,
outcomes `applied`/`duplicate`/`ignored`), has no problem slug and is left as a follow-up. The counter is recorded in the REST
handler, not the use case, because the slug is the HTTP adapter's vocabulary (`problemFor`); an `Idempotency-Key` replay is
answered by the middleware without reaching the handler, so a replay is not counted twice.

**Trace propagation across Kafka: no change needed.** The outbox already stores `headers` and appends the trace headers where
the row is appended (the encoder has no `ctx`), so the CloudEvents encoder goldens stay byte-identical and no migration is
added. What changes is that `Setup` now installs the propagator, so the stored headers carry `traceparent` (and `baggage`)
whenever telemetry is on and a request span is active. Not evidenced in this repository: any consumer extracting the trace
context from a consumed message, so a trace started by a consumed event does not continue into this service's handlers.

**Chart.** `otel.{enabled,endpoint,serviceName}`, `environment` and `serviceVersion` (empty falls back to `image.tag`, then
the chart `appVersion`); `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION` and `ENVIRONMENT` are rendered
for every Deployment that runs a binary, and `extraEnv` / `mcp.extraEnv` refuse those names. `render_test.sh` scans the
telemetry code for the names the binaries read and compares them with the env table of `operations/configuration.md`.

## Consequences

- Every HTTP request to either binary produces `http.server.request.duration` labelled with the chi route pattern, a server
  span named after the pattern, and an access-log line carrying `trace_id`/`span_id`. Series names are listed in
  [Observability](/docs/operations/observability).
- Out of scope here, still follow-ups: consumer-lag, outbox-depth and dead-letter-rate instruments (the SQL signals in the
  observability page stand in for them), a counter on the ship-confirm policy, and trace extraction in the consumers.
  Dashboards and alert rules belong to warehouse-infra and are not touched by this change.
- The OTLP/gRPC dependencies are new direct dependencies; `golang.org/x/net` was already at v0.62.0 and `govulncheck ./...`
  reports no vulnerability reachable from this module's code.
- A metric name is a public contract with every dashboard built on it: renaming `outbound.loads.closures` or its attributes
  needs a new ADR.
