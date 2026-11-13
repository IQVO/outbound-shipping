---
id: observability
title: Observability
sidebar_label: Observability
description: "What outbound-shipping emits today: structured logs, trace-context propagation into the outbox, probes, and what does not exist yet (metrics, spans, dashboards, alerts)."
---

# Observability

Source: `cmd/api/main.go` (`newLogger`), `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `internal/adapters/inbound/http/*.go`, `go.mod`; warehouse-infra `origin/develop` (`terraform/apps.yaml`, `scripts/gen-context-dashboards.py`, `terraform/alerting/warehouse-alert-rules.json`, `terraform/dashboards/contexts/outbound-shipping.json`, `helm-values/outbound-shipping.yaml`; checked 2026-11-11).

This service is released as `v0.1.0`, registered in warehouse-infra and applied to the k3s cluster (one `api` pod running `857edfd` since 2026-11-12), and its observability is thin. Read this page as the inventory of what exists, so that nobody assumes more.

## OpenTelemetry instruments and spans: none

- **No metric instruments.** No file in `cmd/` or `internal/` creates an OpenTelemetry meter, counter, histogram or gauge. `go.opentelemetry.io/otel/metric` is only an indirect dependency.
- **No spans and no HTTP instrumentation.** The router is a plain `chi` router; no `otelhttp`/`otelchi` middleware and no tracer are installed.
- **No SDK or exporter bootstrap.** Neither `cmd/api` nor `cmd/mcp` configures a tracer provider, a meter provider or an exporter, and neither reads any `OTEL_*` variable. Setting `OTEL_EXPORTER_OTLP_ENDPOINT` has no effect.
- **One OTel touch point:** `OutboxRepo.Insert` calls `otel.GetTextMapPropagator().Inject` and stores the result as W3C trace-context headers on each outbox row, so a trace could continue across the relay. Because no propagator is ever installed (the global one is a no-op) and no span is ever started, the headers list is empty today. It becomes useful the day a composition root installs an SDK.

Adding instruments is feature work for the service, not a docs change. Until then, operate it from logs, probes and SQL (below).

## Logs

JSON records on stdout through `log/slog` (`slog.NewJSONHandler`), level from `LOG_LEVEL`. The message text is the stable handle; the useful attributes are:

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

Request-derived values (`method`, `path`) pass through `logSafe`, which strips CR and LF. There is no request-id or trace-id attribute, no access log line per request and no log of the `Idempotency-Key`. Messages are JSON on stdout only; the container root filesystem is meant to be read-only.

## Probes

| Binary | Route | Use |
|---|---|---|
| `cmd/api` | `GET /healthz` | Liveness (always 200 while the process serves). |
| `cmd/api` | `GET /readyz` | Readiness: Postgres ping and a TCP dial of the first broker; 503 while draining. Details in the [runbook](/docs/operations/runbook). |
| `cmd/mcp` | `GET /healthz` | Liveness and readiness (no dependency check). |

## SQL signals

These queries stand in for the metrics that do not exist (run them against the OLTP database):

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

The service registers **no OTel instrument and no `/metrics` endpoint** (see the first section: no meter, no span, no `otelhttp`, no SDK, no Prometheus handler), so most of the generated dashboard stays empty. What is meaningful:

| Dashboard row or alert | Source | Meaningful? |
|---|---|---|
| Kong rows: request rate, 5xx rate, latency p95 | Kong's own `kong_http_requests_total` and `kong_upstream_latency_ms` for the route, from `kongPlugins.prometheus` in the helm values | Yes: the route `/api/outbound-shipping` exists and answered 200 (read 2026-11-13); the rows fill as traffic arrives. |
| Alerts `WarehouseKong5xxRatioHigh`, `WarehouseApiAvailabilityBurnFast` and `...Slow`, `WarehouseApiLatencyBurnFast` and `...Slow` | the same Kong series, for the services `outbound-shipping`, `outbound-shipping-mcp` and `outbound-shipping-reports` routes | The rules are not loaded in the cluster yet (ConfigMap check, 2026-11-13); their input series are live, and the burn alerts need at least 30 requests in their window. They say nothing about the six consumers or the relay. |
| Alert `WarehousePodRestarting` | `kube_pod_container_status_restarts_total` for pods named `outbound-shipping-*` | Yes: the pod has run since 2026-11-12 (0 restarts at read time). It would catch boot-retry exhaustion, a failed migration, a consumer that exits on purpose and an OOM kill. |
| Logs rows: live stream, volume by level, errors and warnings | Loki, label `app=outbound-shipping`, fed by the JSON logs above | Yes: the pod emits the JSON logs above (`api listening`, `outbox relay running` at read time, no ERROR). |
| "Service HTTP RED" rows (request rate by route, request duration p95 by process) and "Go runtime" rows (goroutines, memory) | metrics the service does not emit | **No: empty.** The generator's own comment says these stay empty until the service instruments itself. |

The Kong rows and alerts are fed only through the API route; the MCP route, when the MCP server is switched on, is `outbound-shipping-mcp`. The helm values enable the `cors`, `prometheus` and `opentelemetry` Kong plugins and leave the MCP server off. The chart sets no `OTEL_*`, `ENVIRONMENT` or `SERVICE_VERSION` variable, because the binaries read none.

Not covered by any rule in warehouse-infra, and still to define now that the service runs: the two ERROR messages marked above (`outbox relay: row reached the attempt cap...` and `kafka consumer stopped`), `/readyz` failing for more than a few minutes, consumer-group lag on the six groups, and the `outbox_events` backlog age. None of those was verified live yet (lag and backlog not measured); the SQL signals above stand in for them.
