---
id: runbook
title: Runbook
sidebar_label: Runbook
description: "Deployment status, probes, migrations, Kafka topics and consumer groups, the outbox relay, dead letters and the routine procedures for outbound-shipping."
---

# Runbook

Source: `cmd/api/*.go`, `cmd/mcp/*.go`, `internal/adapters/inbound/http/readiness.go`, `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `migrations/`, `Dockerfile`.

## Deployment status: built on develop, not deployed

- **There is no Helm chart.** `charts/` does not exist in this repository, so the `chart-tests` CI job has nothing to run (it only warns when no chart tests exist) and the `release` job has no chart to package. [Before the first release](/docs/operations/before-first-release) lists the work that remains (rows 5 to 9 are the chart).
- **It is not registered in warehouse-infra.** `terraform/apps.yaml` on `origin/develop` of warehouse-infra has no `outbound-shipping` entry, and no other file of that repository mentions the service (checked 2026-11-09). There is no Kong route, no Argo application, no dashboard and no alert rule for it.
- **The image builds.** The `Dockerfile` compiles every `cmd/*` directory into `/app/<name>` (`/app/api`, `/app/mcp`), runs as UID 1000 on Alpine, `EXPOSE 8080` and `ENTRYPOINT ["./api"]`. The MCP server would run with `command: ["/app/mcp"]` and port 8090. `docker-publish` pushes to `ghcr.io/iqvo` only on a push to `main`.
- **Consequence for operators:** nothing here runs in the k3s cluster yet. Everything below describes how the binaries behave, so that the chart can be written to match it.

## Processes

| Binary | Port | Does |
|---|---|---|
| `cmd/api` | `ADDR`, default `:8080` | REST, six Kafka consumers, the outbox relay, boot migrations. One process, one Postgres pool (10 connections). |
| `cmd/mcp` | `MCP_ADDR`, default `:8090` | Read-only MCP over Streamable HTTP at `/` and `/mcp`, `GET /healthz`. Boot migrations too. No Kafka. |

There is **no separate relay, projector or sweep binary**. The relay and the projector's consumer run inside `cmd/api`; the cutoff sweep is a REST route (`POST /missed-load-sweeps`) that an external scheduler must call (nothing in this repository schedules it).

Run **one replica** of `cmd/api` unless you accept the trade-offs: every replica starts all six consumers and the relay. Consumers of one group split partitions, and the relay claims rows with `FOR UPDATE SKIP LOCKED`, so two replicas do not double-send a row, but ordering across rows of one aggregate then depends on the claim order.

## Probes

`cmd/api`:

| Probe | Route | Behaviour |
|---|---|---|
| Liveness | `GET /healthz` | 200 `{"status":"ok"}`. Never flipped by shutdown. |
| Readiness | `GET /readyz` | 200 `{"status":"ready"}` only while (a) the drain gate is open and (b) both dependency checks pass: a Postgres `Ping` and a TCP dial of the **first** broker in `KAFKA_BROKERS` (2 s timeout, verdict cached 3 s). Otherwise 503: `{"status":"not_ready"}` while draining, a `dependency-unavailable` problem naming `postgres` and/or `kafka` when a dependency fails. |

Readiness does **not** wait for the consumers to have joined their groups or for the relay to have drained anything: the consumers dial Kafka lazily on their first fetch. A broker that accepts TCP but rejects group joins looks ready; see [Troubleshooting](/docs/operations/troubleshooting).

`cmd/mcp` exposes only `GET /healthz` (200 `{"status":"ok"}`), which does not check Postgres after boot.

## Migrations

- One migration pair: `migrations/0001_outbound_shipping_schema.{up,down}.sql`, embedded in the binary (`go:embed`), applied by `golang-migrate` at the start of **both** `cmd/api` and `cmd/mcp`. A re-run is a no-op (`ErrNoChange`); concurrent starts are serialised by the advisory lock.
- The migration URL is `MIGRATIONS_DATABASE_URL` when set, else `DATABASE_URL`. Behind PgBouncer in transaction mode, give the migrations a direct DSN.
- A failed migration aborts boot with `run migrations: ...`. There is no automatic down-migration.
- The schema is listed on [Entity relationship](/docs/ddd/entity-relationship).

## Kafka

One broker platform-wide. The service never creates topics it consumes; it relies on the broker's auto-creation for topics it writes (`AllowAutoTopicCreation` on the writers).

| Direction | Topic | Group variable | Types |
|---|---|---|---|
| consume | `warehouse.fulfillment.events` | `FULFILLMENT_CONSUMER_GROUP` | `PackageManifested`, `PackageDiverted` |
| consume | `warehouse.dock-scheduling.events` | `DOCK_CONSUMER_GROUP` | `DockAppointmentBooked` |
| consume | `warehouse.yard-management.events` | `YARD_CONSUMER_GROUP` | `TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` |
| consume | `warehouse.process-path-management.events` | `CPT_CONSUMER_GROUP` | `CPTScheduleChanged` |
| consume | `warehouse.facility.events` | `LAYOUT_CONSUMER_GROUP` | `ZoneRegistered`, `LocationSlotRegistered` |
| consume | `warehouse.outbound-shipping.analytics` | `ANALYTICS_CONSUMER_GROUP` | its own 13 types (feeds the `load_board` projection) |
| publish | `warehouse.outbound-shipping.events` | none | the 13 integration types |
| publish | `warehouse.outbound-shipping.analytics` | none | the same 13 types, same CloudEvents id |

Consumer semantics (`internal/adapters/inbound/kafka`): one message at a time; handled inside one database transaction that also claims the CloudEvents id in `processed_events`; the offset is committed only after success; no `CommitInterval` (synchronous commit). A transient failure retries the same message with backoff 200 ms doubling to 5 s, at most **5 attempts**, and is then dead-lettered. A fresh group starts at the first offset.

Group ids must be stable and chosen by the deployer; there is no default. Changing a group id replays the whole topic: that is safe (the inbox dedupes) but slow, and a replayed `TrailerDeparted` for a load that already confirmed is ignored.

## Outbox relay

Every event is written to `outbox_events` in the same transaction as the aggregate (two rows per event: integration and analytics topic). The relay in `cmd/api` drains them:

- passes every `OUTBOX_INTERVAL_MS` (250) when a pass did not fill a batch, `OUTBOX_BATCH` (100) rows per pass, ordered by `id`, claimed `FOR UPDATE SKIP LOCKED`;
- the Kafka writer uses `RequireAll` acks and a hash balancer on the key (the aggregate id), so all events of one load, package or bill of lading stay ordered on one partition;
- a successful send sets `published_at`; a failed one increments `attempts` and stores `last_error`;
- after `OUTBOX_MAX_ATTEMPTS` (10) failures a row is excluded from future claims, one ERROR is logged (`outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause`, with `id`, `topic`, `type`, `key`, `attempts`), and later rows keep flowing.

Delivery is at-least-once: a crash between a successful send and the `UPDATE` re-sends the row with the same CloudEvents `id`, so consumers must dedupe on `id`.

Inspect the backlog and the parked rows:

```sql
-- unpublished rows and how old the oldest is
SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL;

-- parked rows (cap reached)
SELECT id, topic, event_type, subject, attempts, last_error
FROM outbox_events
WHERE published_at IS NULL AND attempts >= 10
ORDER BY id;
```

Re-arm a parked row after fixing the cause (broker down, topic misconfigured, message too large):

```sql
UPDATE outbox_events SET attempts = 0 WHERE id = <id> AND published_at IS NULL;
```

`published_at` rows are never deleted by the service: there is no housekeeping job, so the table grows. Plan retention before this runs in production.

## Dead letters

A message that cannot be applied goes to `<consumed topic>.dlq` (for example `warehouse.yard-management.events.dlq`) with the original key, value and headers plus `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at`.

- **Poison at once, no retry:** a CloudEvent of a handled type whose payload cannot be decoded or that a use case refuses as `ErrInvalidEvent` (missing id, site, door...).
- **After 5 attempts:** transient failures that never cleared (database errors, `ErrConcurrentModification`).
- **Skipped, never dead-lettered:** a message that is not a valid CloudEvent, and a type the consumer does not handle.
- **Shutdown is never a dead letter:** a handler interrupted by `SIGTERM` leaves the offset uncommitted and is redelivered after restart.
- The DLQ publish itself retries forever (and up to 40 x 250 ms while the auto-created DLQ topic has no leader), so a broker outage blocks the partition rather than losing a message.

There is no replay tool. To replay a dead-lettered message after fixing the cause, republish its value to the original topic with the original key (the inbox dedupes on the CloudEvents `id`, and a message that was rolled back is not claimed, so it is applied).

## Shutdown

On `SIGTERM`/`SIGINT` `cmd/api`: flips `/readyz` to not ready first, stops the six consumers, then the relay, waits for each loop and closes the readers, drains HTTP for up to 10 s, then closes the relay writer and the Postgres pool. There is no drain delay between the readiness flip and the listener close, so a chart should set a `preStop` sleep and a `terminationGracePeriodSeconds` above 10 s plus the longest consumer handler.

A consumer loop that dies on its own (a reader error) is logged as `kafka consumer stopped` and does **not** stop the process: `/healthz` and `/readyz` keep answering 200 and that topic stops being consumed until the pod restarts. Alert on that log line and on consumer lag.

## Routine procedures

| Task | How |
|---|---|
| Run the missed-load sweep | `POST /missed-load-sweeps` with `{"site_id":"SITE-1"}` and an `Idempotency-Key`; response `{"flagged":N,"swept_at":...}`. Re-running flags nothing new (the once-guard on each package). Schedule it from outside the service around each cutoff. |
| Void a bill of lading | `POST /bills-of-lading/{bolNumber}/voids` with `{"reason":"..."}`. The shipment is not undone. |
| Reopen a closed load before departure | `POST /loads/{loadId}/reopenings` (only a `CLOSED` load). |
| Resolve a load that left without closing | The departure never confirms a `LOADING` load (ADR 0002). Short-ship the packages that missed with a reason and close it; it stays `CLOSED` and unconfirmed: the departure that already arrived is not replayed, and only a `TrailerDeparted` with a new CloudEvents id and a seal number would confirm it. A supervisor decides next steps outside the service. |
| Re-run a consumer from scratch | Give it a new group id (replays the topic). The inbox dedupes on `(consumer, event_id)`, and an event handled as `ignored` is also claimed, so a replay does not re-apply it: delete its `processed_events` row first if it must be re-evaluated. |
| Check consumer progress | Kafka consumer-group lag for the six groups; `processed_events` rows per `consumer` (`outbound-shipping-fulfillment`, `-dock`, `-yard`, `-cpt`, `-layout`, `-projector`). |
