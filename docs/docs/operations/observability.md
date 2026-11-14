---
id: observability
title: Observability
sidebar_label: Observability
description: "What outbound-shipping emits today: OpenTelemetry metrics and spans, trace-correlated structured logs, trace-context propagation into the outbox, probes, and what is not instrumented yet (consumer lag, outbox depth, dead-letter rate)."
---

# Observability

Source: `cmd/api/main.go` (`newLogger`), `cmd/api/telemetry.go`, `cmd/mcp/telemetry.go`, `internal/adapters/outbound/telemetry/*.go`, `internal/adapters/inbound/requestlog/requestlog.go`, `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `internal/adapters/inbound/http/*.go`, `go.mod`; warehouse-infra `origin/develop` (`terraform/apps.yaml`, `scripts/gen-context-dashboards.py`, `terraform/alerting/warehouse-alert-rules.json`, `terraform/dashboards/contexts/outbound-shipping.json`, `helm-values/outbound-shipping.yaml`; checked 2026-11-11).

This service is released as `v0.1.0`, registered in warehouse-infra and applied to the k3s cluster (one `api` pod running `857edfd` since 2026-11-12). That image predates the OpenTelemetry setup described below: nothing here has been observed on the running pod, so read the signal list as what the code of `develop` emits once an image carrying it runs with telemetry on, and the dashboard section as the state of 2026-11-13.

## OpenTelemetry signals

Both binaries bring OpenTelemetry up at boot (`cmd/api/telemetry.go`, `cmd/mcp/telemetry.go`, over `internal/adapters/outbound/telemetry`; [ADR 0008](/docs/adr/0008-adopt-the-fleet-observability-standard)). Traces and metrics are pushed over OTLP/gRPC; there is no `/metrics` endpoint to scrape. Export is non-blocking: an unreachable Collector drops telemetry and costs nothing else.

**Switch.** `OTEL_EXPORTER_OTLP_ENDPOINT` (`host:port` or a URL) turns it on; **empty means off**: no provider is installed and nothing is exported (the chart renders it only while `otel.enabled`, default `true`, endpoint `otel-collector.observability.svc.cluster.local:4317`). `OTEL_SERVICE_NAME` (`outbound-shipping` for `cmd/api`, `outbound-shipping-mcp` for `cmd/mcp`), `SERVICE_VERSION` (default `dev`) and `ENVIRONMENT` (default `local`) become `service.name`, `service.version` and `deployment.environment.name`. All variables are in [Configuration](/docs/operations/configuration).

| Signal | Name | Kind and unit | Attributes | Source |
|---|---|---|---|---|
| HTTP request duration | `http.server.request.duration` | histogram, `s` | `http.method`, `http.scheme`, `http.route` (the chi pattern, never the raw path; absent when no route matched). No status-code attribute: this instrument cannot split errors from successes, the server spans can | `otelchimetric` on the REST router and the MCP router |
| HTTP in flight | `http.server.active_requests` | up-down counter, `{request}` | `http.method`, `http.scheme` (the attributes are taken before routing, so no `http.route`) | same |
| Load closures (Tier 2) | `outbound.loads.closures` | counter, `{closure}` | `outcome` = `closed` or `failed`; on `failed`, `reason` = the RFC 7807 problem slug | `POST /loads/{loadId}/closures` |
| Go runtime | the instruments of `go.opentelemetry.io/contrib/instrumentation/runtime` | as that package defines them | | `runtime.Start` in `Setup` |
| Server spans | one per request, named after the route pattern | span kind server | | `otelchi` |

Expected Prometheus series, from the OTel to Prometheus naming rules (the Collector's exporter configuration is warehouse-infra's and was not read for this page): `http_server_request_duration_seconds_bucket`, `_sum` and `_count`; `http_server_active_requests`; `outbound_loads_closures_total`.

`outbound.loads.closures` counts every attempt of the supervisor's close, including a malformed body. Idempotency-Key replays are answered by the middleware and are not counted twice. `reason` values are the slugs of the problem catalogue in `internal/adapters/inbound/http/errors.go` (for example `packages-outstanding`, `load-state`, `load-not-found`, `bad-request`, `internal-error`).

**Trace context across Kafka.** `OutboxRepo.Insert` injects the W3C trace context of the request into each outbox row's `headers`, and the relay copies the stored headers onto the Kafka message. With telemetry on, those headers carry `traceparent` (and `baggage`); with it off the propagator is the global no-op and the list stays empty. Not evidenced: any consumer in this repository extracting the trace context from a consumed message.

**Not instrumented yet** (follow-up work, not part of ADR 0008): consumer lag, outbox backlog depth, dead-letter rate, and a counter on the ship-confirm policy. Operate those from the logs and the SQL signals below.

## Logs

JSON records on stdout through `log/slog` (`slog.NewJSONHandler`), level from `LOG_LEVEL`. Records written with a context that carries a span (the `*Context` logger variants, as the access log does) also carry `trace_id` and `span_id` (`telemetry.WithTraceContext`); that needs the span, so it applies only while telemetry is on. The message text is the stable handle; the useful attributes are:

| Message (prefix) | Level | Attributes | Meaning |
|---|---|---|---|
| `api listening` | info | `addr` | HTTP listener started. |
| `mcp server listening (Streamable HTTP)` | info | `addr` | MCP server started. |
| `outbox relay running` | info | `interval`, `batch`, `max_attempts` | Relay started with its effective settings. |
| `event processed` | info | `type`, `event_id`, `subject`, `outcome` | One consumed event handled. `outcome` is `applied`, `duplicate` or `ignored`. |
| `kafka reader error; retrying` | error | `consumer`, `topic`, `detail` | kafka-go surfaced a reader problem (unreachable broker, missing topic, group join failure). |
| `<consumer> handling failed; retrying the same message` | error | `error`, `partition`, `offset`, `attempt`, `max_attempts`, `retry_in` | Transient failure; the same message is retried. |
| `<consumer> handling failed after the maximum attempts; dead-lettering the message` | error | `error`, `partition`, `offset`, `attempts` | Fifth attempt failed; message goes to `<topic>.dlq`. |
| `<consumer> message can never be applied; dead-lettering it without retry` | error | `error`, `partition`, `offset` | Poison message. |
| `dead-lettering an event that can never be applied` | warn | `consumer`, `type`, `event_id`, `error` | The reason a poison CloudEvent was refused. |
| `skipping a message that is not a valid CloudEvent` | warn | `consumer`, `topic`, `error` | Skipped, offset committed. |
| `<consumer> dead-letter publish failed; retrying the same message` | error | `partition`, `offset`, `attempt`, `retry_in` | The DLQ write itself failed; the partition is blocked until it succeeds. |
| `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | error | `id`, `topic`, `type`, `key`, `attempts`, `max_attempts`, `error` | A parked outbox row. **Alert on this.** |
| `outbox relay pass failed` | error | `error` | A drain pass failed (database or broker). |
| `kafka consumer stopped` | error | `consumer`, `error` | A consumer loop exited on its own; the process keeps running and that topic is no longer consumed. **Alert on this.** |
| `outbox relay stopped` | error | `error` | The relay loop exited. |
| `readiness: dependency is not ready` | warn | `dependency`, `error` | A `/readyz` check failed (`postgres` or `kafka`). |
| `request failed` | error | `error`, `method`, `path` | An unmapped error became a 500. |
| `handler panicked` | error | `panic`, `method`, `path` | A panic was recovered into a 500. |
| `http request` | info (error for a 5xx) | `method`, `path`, `status`, `duration_ms`, `bytes`, `request_id` | One access-log line per request on both binaries (`RequestLogger`). |
| `telemetry configured` / `telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set` | info | `service_name`, `otlp_endpoint` (configured only) | Written once at boot by each binary. |
| `telemetry flush failed on shutdown` | warn | `error` | The final export at shutdown did not complete within 5 s. |

Request-derived values (`method`, `path`) pass through `logSafe` (or, in the access log, the equivalent in `internal/adapters/inbound/requestlog`), which strips CR and LF. Messages are JSON on stdout only; the container root filesystem is meant to be read-only. There is no log of the `Idempotency-Key`.

## Probes

| Binary | Route | Use |
|---|---|---|
| `cmd/api` | `GET /healthz` | Liveness (always 200 while the process serves). |
| `cmd/api` | `GET /readyz` | Readiness: Postgres ping and a TCP dial of the first broker; 503 while draining. Details in the [runbook](/docs/operations/runbook). |
| `cmd/mcp` | `GET /healthz` | Liveness and readiness (no dependency check). |

## SQL signals

These queries stand in for the instruments that do not exist yet (consumer lag, outbox depth, dead-letter rate; run them against the OLTP database):

```sql
-- relay backlog and age of the oldest unpublished row
SELECT count(*) AS backlog, now() - min(created_at) AS oldest
FROM outbox_events WHERE published_at IS NULL;

-- parked rows: the relay gave up on these (20 = default OUTBOX_MAX_ATTEMPTS; none exist when it is 0)
SELECT count(*) FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;

-- inbox volume per consumer, last hour
SELECT consumer, count(*) FROM processed_events
WHERE processed_at > now() - interval '1 hour' GROUP BY consumer;

-- projector freshness: the board is fed from the analytics topic
SELECT max(updated_at) FROM load_board;
```

## Dashboards and alerts

**Loaded onto the cluster by the platform apply of 2026-11-13** (the live Prometheus alerting config and the Grafana dashboards ConfigMap both carry the context; re-read 2026-11-13). warehouse-infra generates one dashboard (`Warehouse - Outbound Shipping`, uid `warehouse-outbound-shipping`) and six alert rules for the context from `scripts/gen-context-dashboards.py`; the context entry has `service_regex: outbound-shipping.*`, `loki_app: outbound-shipping` and **empty `business_metrics`**. They go live only through warehouse-infra's platform apply, and a read-only check on 2026-11-13 shows the app apply of 2026-11-12 did not carry them: the `prometheus-server` ConfigMap (`alerting_rules.yml`) in `observability` holds no outbound-shipping rule and `grafana-dashboards-warehouse` holds no outbound-shipping dashboard. The pod and the Kong route exist now, so the rows below that read them are live inputs.

The image running on the cluster (`857edfd`) predates the OpenTelemetry setup and registers no OTel instrument, so most of the generated dashboard is empty today. The code of `develop` registers the instruments listed in the first section (still no `/metrics` endpoint: metrics are pushed over OTLP). The context entry's `business_metrics` is empty, so `outbound.loads.closures` has no dashboard panel until warehouse-infra's generator is filled (not part of this repository). What is meaningful:

| Dashboard row or alert | Source | Meaningful? |
|---|---|---|
| Kong rows: request rate, 5xx rate, latency p95 | Kong's own `kong_http_requests_total` and `kong_upstream_latency_ms` for the route, from `kongPlugins.prometheus` in the helm values | Yes: the route `/api/outbound-shipping` exists and answered 200 (read 2026-11-13); the rows fill as traffic arrives. |
| Alerts `WarehouseKong5xxRatioHigh`, `WarehouseApiAvailabilityBurnFast` and `...Slow`, `WarehouseApiLatencyBurnFast` and `...Slow` | the same Kong series, for the services `outbound-shipping`, `outbound-shipping-mcp` and `outbound-shipping-reports` routes | The rules are not loaded in the cluster yet (ConfigMap check, 2026-11-13); their input series are live, and the burn alerts need at least 30 requests in their window. They say nothing about the six consumers or the relay. |
| Alert `WarehousePodRestarting` | `kube_pod_container_status_restarts_total` for pods named `outbound-shipping-*` | Yes: the pod has run since 2026-11-12 (0 restarts at read time). It would catch boot-retry exhaustion, a failed migration, a consumer that exits on purpose and an OOM kill. |
| Logs rows: live stream, volume by level, errors and warnings | Loki, label `app=outbound-shipping`, fed by the JSON logs above | Yes: the pod emits the JSON logs above (`api listening`, `outbox relay running` at read time, no ERROR). |
| "Service HTTP RED" rows (request rate by route, request duration p95 by process) and "Go runtime" rows (goroutines, memory) | `http.server.request.duration` and the Go runtime instruments, which the running image does not emit | **Empty today.** The generator's own comment says these stay empty until the service instruments itself; the instrumentation now exists in the code, and the rows should fill once an image carrying it runs with telemetry on. Not verified: whether the generator's queries match these series names. |

The Kong rows and alerts are fed only through the API route; the MCP route, when the MCP server is switched on, is `outbound-shipping-mcp`. The helm values enable the `cors`, `prometheus` and `opentelemetry` Kong plugins and leave the MCP server off. The chart now renders `ENVIRONMENT`, `SERVICE_VERSION` and, while `otel.enabled` (default `true`), `OTEL_EXPORTER_OTLP_ENDPOINT` and `OTEL_SERVICE_NAME` for both Deployments; a rollout of an image with this change is what turns the signals on.

Not covered by any rule in warehouse-infra, still to define and not yet instrumented in the service: the two ERROR messages marked above (`outbox relay: row reached the attempt cap...` and `kafka consumer stopped`), `/readyz` failing for more than a few minutes, consumer-group lag on the six groups, and the `outbox_events` backlog age. None of those was verified live yet (lag and backlog not measured); the SQL signals above stand in for them.
