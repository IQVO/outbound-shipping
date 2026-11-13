---
id: configuration
title: Configuration
sidebar_label: Configuration
description: "Every environment variable each outbound-shipping binary reads, with default, required flag and meaning."
---

# Configuration

Two binaries ship from this repository: `cmd/api` (REST, the six Kafka consumers and the outbox relay in one process) and `cmd/mcp` (read-only MCP server). Each reads its own environment; there is no config file and no in-memory or log-sink mode. A missing required variable is a boot error that names the variable, and `cmd/api` reports every missing variable in one message.

Source: `cmd/api/config.go` (`loadConfig`, `groupVars`), `cmd/api/main.go`, `cmd/mcp/main.go`, `internal/adapters/outbound/postgres/migrate.go` (`MigrationsURL`).

Integer variables of `cmd/api` are read with one rule: a value that is not a number, or is below the variable's minimum, is **silently replaced by the default** (no warning is logged). The minimum is 1 for `OUTBOX_INTERVAL_MS` and `OUTBOX_BATCH` (so `0` and negative values mean the default there) and 0 for `OUTBOX_MAX_ATTEMPTS`, where an explicit `0` means **unlimited** (fleet convention) and only negative or non-numeric values fall back to the default.

## cmd/api (REST API, six Kafka consumers, outbox relay)

| Variable | Default | Required | Meaning |
|---|---|---|---|
| `DATABASE_URL` | none | yes | Postgres DSN of the OLTP database. The runtime pool is capped at 10 connections with `statement_timeout = 5s` (`postgres.MaxConns`, `postgres.StatementTimeout`). |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled) DSN used only for the embedded migrations that run at boot. Set it when `DATABASE_URL` goes through a pooler: `golang-migrate` needs a session connection for its advisory lock. |
| `KAFKA_BROKERS` | none | yes | Comma-separated bootstrap brokers for the consumers, the relay and the `/readyz` broker check (a TCP dial of the **first** broker). |
| `FULFILLMENT_CONSUMER_GROUP` | none | yes | Consumer group of the fulfillment-execution consumer (`warehouse.fulfillment.events`). |
| `DOCK_CONSUMER_GROUP` | none | yes | Consumer group of the dock-scheduling consumer (`warehouse.dock-scheduling.events`). |
| `YARD_CONSUMER_GROUP` | none | yes | Consumer group of the yard-management consumer (`warehouse.yard-management.events`). |
| `CPT_CONSUMER_GROUP` | none | yes | Consumer group of the process-path-management cutoff consumer (`warehouse.process-path-management.events`). |
| `LAYOUT_CONSUMER_GROUP` | none | yes | Consumer group of the facility-layout consumer (`warehouse.facility.events`). |
| `ANALYTICS_CONSUMER_GROUP` | none | yes | Consumer group of the service's own projector consumer (`warehouse.outbound-shipping.analytics`). |
| `ADDR` | `:8080` | no | Listen address of the REST API. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`; any other value is `info`. Case-sensitive (lower case). |
| `OUTBOX_INTERVAL_MS` | `250` | no | Milliseconds the relay sleeps after a pass that did not fill a batch. |
| `OUTBOX_BATCH` | `100` | no | Outbox rows claimed per relay pass. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failed sends after which a row is excluded from future claims (see the [runbook](/docs/operations/runbook)). `0` = unlimited (the row is retried forever); negative or non-numeric = `20`. |

The consumer groups are deliberately plain environment variables with no default: they must be stable across restarts and prefixed with the service name (for example `outbound-shipping-yard`); a group id derived from the pod name would replay each topic on every restart. `internal/architecture`'s `TestKafkaConsumerGroupNeverHardcodedInline` fails the build if a group id is written as a literal in a consumer.

The code defaults of the relay package itself (`DefaultInterval` 1 s, `DefaultMaxAttempts` 20) are not what `cmd/api` runs with for the interval and batch: `cmd/api` always passes its own values (250 ms, batch 100) and takes the attempt cap from `OUTBOX_MAX_ATTEMPTS`, whose default is the relay package's `DefaultMaxAttempts` (20).

## cmd/mcp (MCP server, Streamable HTTP)

`cmd/mcp` reads Postgres only. It starts no relay, no consumer and never dials Kafka, so it needs no broker variable.

| Variable | Default | Required | Meaning |
|---|---|---|---|
| `DATABASE_URL` | none | yes | Postgres DSN. Without it the binary exits at boot with `DATABASE_URL is required`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations. `cmd/mcp` runs the same idempotent embedded migrations as `cmd/api` (golang-migrate's advisory lock makes concurrent starts safe). |
| `MCP_ADDR` | `:8090` | no | Listen address of the MCP server. |
| `LOG_LEVEL` | `info` | no | Same values as `cmd/api`. |

## In the cluster: the Helm chart and warehouse-infra

The chart `charts/outbound-shipping/` renders each variable above from a dedicated value (the mapping is in the [runbook](/docs/operations/runbook#helm-chart)), and warehouse-infra's `helm-values/outbound-shipping.yaml` sets them for the k3s cluster: the six consumer group ids at the chart defaults, the database Secret `outbound-shipping-db` (`DATABASE_URL` and `MIGRATIONS_DATABASE_URL`), one `api` replica and the MCP server **off**. The brokers come from the platform values and the image tag from the pin in `terraform/targets/k3s.images.yaml`. These values are live: the one `api` replica running on k3s since 2026-11-12 reads them, its image `857edfd-arm64` taken from that pin (see [Deployment status](/docs/operations/runbook#deployment-status-applied-to-k3s-running-v010)).

## What is not configurable

- **No auth variables.** REST, MCP and the probes are unauthenticated; access control is the cluster network boundary.
- **No OpenTelemetry variables.** Neither binary installs an OpenTelemetry SDK or exporter today, so `OTEL_*` variables have no effect (see [Observability](/docs/operations/observability)).
- **No mode toggles.** There is no `*_MODE`, no in-memory fallback (`TestRuntimeBinaryHasNoInMemoryAdapters`) and no envelope toggle: every Kafka message is a CloudEvents 1.0 structured-mode event.
- **Kafka topics are constants,** not environment: `warehouse.outbound-shipping.events` and `warehouse.outbound-shipping.analytics` for the topics this service writes, plus the five consumed topics above. Dead-letter topics are the consumed topic plus `.dlq`.
