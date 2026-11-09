---
id: observability
title: Observability
sidebar_label: Observability
description: "What outbound-shipping emits today: structured logs, trace-context propagation into the outbox, probes, and what does not exist yet (metrics, spans, dashboards, alerts)."
---

# Observability

Source: `cmd/api/main.go` (`newLogger`), `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `internal/adapters/inbound/http/*.go`, `go.mod`; warehouse-infra `origin/develop` (checked 2026-11-09).

This service is built on `develop` and not deployed, and its observability is thin. Read this page as the inventory of what exists, so that nobody assumes more.

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

-- parked rows: the relay gave up on these
SELECT count(*) FROM outbox_events WHERE published_at IS NULL AND attempts >= 10;

-- inbox volume per consumer, last hour
SELECT consumer, count(*) FROM processed_events
WHERE processed_at > now() - interval '1 hour' GROUP BY consumer;

-- projector freshness: the board is fed from the analytics topic
SELECT max(updated_at) FROM load_board;
```

## Dashboards and alerts

None exist. warehouse-infra `origin/develop` has no `outbound-shipping` entry in `terraform/apps.yaml` and no dashboard or alert rule that mentions the service. Fleet dashboards are generated per registered application, so the service gets none until it is registered (see [Before the first release](/docs/operations/before-first-release), row 13). The fleet's Kong 5xx panels would only cover it once a Kong route exists.

Minimum alerts to define when it is deployed: the two ERROR messages marked above, `/readyz` failing for more than a few minutes, consumer-group lag on the six groups, and the `outbox_events` backlog age.
