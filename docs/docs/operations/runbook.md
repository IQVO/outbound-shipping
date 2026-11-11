---
id: runbook
title: Runbook
sidebar_label: Runbook
description: "Deployment status, probes, migrations, Kafka topics and consumer groups, the outbox relay, dead letters and the routine procedures for outbound-shipping."
---

# Runbook

Source: `cmd/api/*.go`, `cmd/mcp/*.go`, `internal/adapters/inbound/http/readiness.go`, `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `migrations/`, `Dockerfile`.

## Deployment status: released v0.1.0, registered and pinned, not applied

State checked on 2026-11-11. Nothing below is observed from a running pod, because no outbound-shipping pod exists.

- **Released.** GitHub release `v0.1.0` (tag at `857edfd`, the head of `main`; release PR 20) carries the image `ghcr.io/iqvo/outbound-shipping:v0.1.0` (also tagged `latest` and `857edfd`) and the chart package `outbound-shipping-0.1.0.tgz`. `docker-publish` and `release` both succeeded on that push (CI run 43000267854). The first push to `main` had failed the required `vuln` job on GO-2026-6859 in `net/http` of Go 1.27.3; PR 19 bumped `go.mod` to `go 1.27.4` and re-pinned the Dockerfile builder digest, and nothing was published from the failed run.
- **The Helm chart is merged and released.** `charts/outbound-shipping/` (PR 17, merged on `develop`) renders the `api` Deployment and Service and, behind flags, the `mcp` Deployment and Service, the Gateway API routes and three Kong plugins (see [Helm chart](#helm-chart)). The `chart-tests` CI job runs its two tests, and the `release` job packages it with `helm package charts/outbound-shipping --version X.Y.Z --app-version X.Y.Z`.
- **Registered in warehouse-infra** (PR 205, merged on its `develop`): the `terraform/apps.yaml` entry (`kind: service`, `database: true`, no analytics database, no frontend remote), `helm-values/outbound-shipping.yaml`, the CPU overlay in `helm-values/kind-resources/`, a dashboard and alert rules generated for the context, the chart-selector dummy in `scripts/check-chart-selectors.py`, `docs/kafka-topics-outbound-shipping.md`, and the image pin `outbound-shipping: image: 857edfd-arm64` in `terraform/targets/k3s.images.yaml` (publish-images run 43001089814). Those are the facts of that repository's `origin/develop`; this repository does not own them.
- **Not applied.** A read-only `kubectl get pods,deploy,application -A` on the k3s-multipass context shows no outbound-shipping pod, Deployment or Argo application. I did not check whether the database, the Kafka topics or the consumer groups exist; with no pod and no Argo application, no Kong route for the service is rendered. The dashboard and the alert rules are generated in warehouse-infra but are not in the cluster either: the `prometheus-server` ConfigMap (`alerting_rules.yml`) has no outbound-shipping rule and `grafana-dashboards-warehouse` has no outbound-shipping dashboard (see [Observability](/docs/operations/observability#dashboards-and-alerts)). They reach the cluster only through warehouse-infra's platform apply.
- **Consequence for operators:** nothing here runs in the k3s cluster yet. Everything below describes how the binaries behave and what the chart does with them, read from the code and the chart; none of it was observed live.

### What an apply needs

From the helm values and the topic note in warehouse-infra, none of which has been run:

1. **Pre-create the seven Kafka topics before the first sync.** `warehouse.outbound-shipping.events`, `warehouse.outbound-shipping.analytics` and the five it reads (`warehouse.fulfillment.events`, `warehouse.dock-scheduling.events`, `warehouse.yard-management.events`, `warehouse.process-path-management.events`, `warehouse.facility.events`), with the commands in warehouse-infra's `docs/kafka-topics-outbound-shipping.md` (`kafka-topics.sh --create --if-not-exists`, 8 partitions, replication factor 1). That note's reason is that a kafka-go group consumer that starts before its topic exists is never handed the partition, and the platform creates no topics itself. The service never creates a topic it consumes. The `.dlq` topics need nothing.
2. **Pod budget.** The helm values run **one `api` replica and nothing else**: the MCP server is off (`mcp.enabled: false`, its Kong route flag stays declared) and there is no relay Deployment because the relay runs inside the `api`. The values file states that the k3s nodes were at 108 of 110 and 98 of 110 pods when it was written; that capacity was not re-measured for this page. Turning the MCP server on adds a second pod, in one values PR.
3. **Database and secrets.** The `outbound-shipping-db` Secret holds `DATABASE_URL` (through PgBouncer) and `MIGRATIONS_DATABASE_URL` (direct); the role and database come from the platform's provisioning Job before the first sync.
4. **Apply** with warehouse-infra's own procedure for one app (the topic note names `scripts/tf.sh app outbound-shipping apply`), after the topics exist. The image comes from the pin, not from a local build.

After an apply, check `/healthz` and `/readyz`, the six consumer groups (`outbound-shipping-fulfillment`, `-dock`, `-yard`, `-cpt`, `-layout`, `-analytics`) and `kafka consumer stopped` in the log (see [Observability](/docs/operations/observability)).

- **The image builds.** The `Dockerfile` compiles every `cmd/*` directory into `/app/<name>` (`/app/api`, `/app/mcp`), runs as UID 1000 on Alpine, `EXPOSE 8080` and `ENTRYPOINT ["./api"]`. The builder stage is `golang:1.27-alpine` pinned by digest (Go 1.27.4). The MCP server runs with `command: ["/app/mcp"]` and port 8090. `docker-publish` pushes to `ghcr.io/iqvo` only on a push to `main`; it has done so once, for `v0.1.0`.

## Helm chart

`charts/outbound-shipping/` is modelled on the sibling charts (dock-scheduling, returns-processing) and renders only what this service has. There is no analytics projector or reports Deployment, no sweep CronJob, no A2A server, no frontend and no separate relay Deployment: the relay and the projector's consumer run inside `cmd/api`.

| Component | Rendered | What |
|---|---|---|
| `api` | always | `Deployment` (runs the image `ENTRYPOINT ./api`, probes `/healthz` and `/readyz`), `Service` `ClusterIP` port 80 to 8080, `ConfigMap` mounted with `envFrom`, `ServiceAccount`, and a `Secret` only when `database.url` is set. |
| `mcp` | `mcp.enabled` (default `false`) | `Deployment` (`command: ["/app/mcp"]`, `MCP_ADDR` `:8090`, all probes `/healthz`: `cmd/mcp` has no `/readyz`) and `Service` on port 8090. It reads no Kafka variable. |
| api route | `gatewayApi.enabled` (default `false`) | `HTTPRoute` `/api/outbound-shipping`, prefix stripped to `/`. |
| mcp route | `mcp.route.enabled` (default `false`, needs `mcp.enabled`) | `HTTPRoute` `/api/mcp/outbound-shipping`, prefix rewritten to `/mcp`. |
| Kong plugins | `kongPlugins.cors`, `.prometheus`, `.opentelemetry` (each default `false`) | `KongPlugin` objects attached to the routes. There is **no auth plugin**: the fleet is unauthenticated. |

Values that matter (all keys of `charts/outbound-shipping/values.yaml`):

| Value | Env var | Notes |
|---|---|---|
| `database.url` or `database.existingSecret` (key `database.existingSecretKey`, default `DATABASE_URL`) | `DATABASE_URL` | Required; the render fails without one. Always a `secretKeyRef`, never in the ConfigMap. |
| `database.migrationsExistingSecretKey` (default `MIGRATIONS_DATABASE_URL`) | `MIGRATIONS_DATABASE_URL` | Optional `secretKeyRef` into the same Secret; the binaries fall back to `DATABASE_URL`. |
| `kafka.brokers` | `KAFKA_BROKERS` | Required (comma-separated); `api` only. |
| `config.fulfillmentConsumerGroup`, `dockConsumerGroup`, `yardConsumerGroup`, `cptConsumerGroup`, `layoutConsumerGroup`, `analyticsConsumerGroup` | the six `*_CONSUMER_GROUP` variables | Default to `outbound-shipping-fulfillment`, `-dock`, `-yard`, `-cpt`, `-layout`, `-analytics`. The render fails when one is empty. Do not change them on a running release: a new id replays its topic from the earliest offset. |
| `config.addr`, `config.logLevel` | `ADDR`, `LOG_LEVEL` | Defaults `:8080` and `info`. `mcp.httpAddr` sets `MCP_ADDR` (`:8090`). |
| `config.outboxIntervalMs`, `outboxBatch`, `outboxMaxAttempts` | `OUTBOX_INTERVAL_MS`, `OUTBOX_BATCH`, `OUTBOX_MAX_ATTEMPTS` | Empty by default: not rendered, the binary defaults (250 ms, 100, 20) apply. |
| `image.repository`, `image.tag` | none | Set by the platform (Terraform `valuesObject`); the chart default is `warehouse/outbound-shipping:local`. |

**What warehouse-infra sets** (`helm-values/outbound-shipping.yaml`): `replicaCount: 1`; the six consumer groups at the chart defaults; `database.existingSecret: outbound-shipping-db` with `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` keys; `gatewayApi.enabled: true` on `/api/outbound-shipping` (prefix stripped; the parent gateway comes from the platform values); `kongPlugins.cors`, `.prometheus` and `.opentelemetry` all enabled; `mcp.enabled: false` with `mcp.route.enabled: true` and path `/api/mcp/outbound-shipping`, which renders only once the MCP server is switched on; `image.repository: warehouse/outbound-shipping`, with the tag taken from the pin in `terraform/targets/k3s.images.yaml`. The brokers, CORS origin and OTLP endpoint come from the platform values. `extraEnv` is empty. Its header comment still says the image is not pinned; the pin is in `k3s.images.yaml` on the same branch.

The chart sets no `ENVIRONMENT`, `SERVICE_VERSION` or `OTEL_*` variable because no binary reads one (see [Observability](/docs/operations/observability)). `chart-tests` fails if the chart renders a variable the Go code does not read, or misses one it does.

**How migrations run.** There is no migration Job. `cmd/api` and `cmd/mcp` both run the embedded `golang-migrate` migrations on every start (idempotent, advisory-locked). With a pooler in front of Postgres put a direct DSN under `MIGRATIONS_DATABASE_URL` in the Secret. Whether a pod can reach and migrate a real database in the cluster is **not verified**: nothing has been applied.

Local checks (no cluster needed): `helm lint charts/outbound-shipping -f charts/outbound-shipping/ci/default-values.yaml` (and `ci/all-components-values.yaml`), `bash charts/outbound-shipping/tests/render_test.sh` and `python3 charts/outbound-shipping/tests/test_service_selectors.py`.

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

One broker platform-wide. The service never creates topics it consumes; it relies on the broker's auto-creation for topics it writes (`AllowAutoTopicCreation` on the writers). warehouse-infra asks for all seven topics to be pre-created before the first sync (see [What an apply needs](#what-an-apply-needs)).

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

Group ids must be stable and chosen by the deployer; the binary has no default (the chart and the warehouse-infra values set the six names above). Changing a group id replays the whole topic: that is safe (the inbox dedupes) but slow, and a replayed `TrailerDeparted` for a load that already confirmed is ignored.

## Outbox relay

Every event is written to `outbox_events` in the same transaction as the aggregate (two rows per event: integration and analytics topic). The relay in `cmd/api` drains them:

- passes every `OUTBOX_INTERVAL_MS` (250) when a pass did not fill a batch, `OUTBOX_BATCH` (100) rows per pass, ordered by `id`, claimed `FOR UPDATE SKIP LOCKED`;
- the Kafka writer uses `RequireAll` acks and a hash balancer on the key (the aggregate id), so all events of one load, package or bill of lading stay ordered on one partition;
- a successful send sets `published_at`; a failed one increments `attempts` and stores `last_error`;
- after `OUTBOX_MAX_ATTEMPTS` (default 20; `0` = unlimited, the row is then retried forever) failures a row is excluded from future claims, one ERROR is logged (`outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause`, with `id`, `topic`, `type`, `key`, `attempts`), and later rows keep flowing.

Delivery is at-least-once: a crash between a successful send and the `UPDATE` re-sends the row with the same CloudEvents `id`, so consumers must dedupe on `id`.

Inspect the backlog and the parked rows:

```sql
-- unpublished rows and how old the oldest is
SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL;

-- parked rows (cap reached); replace 20 with your OUTBOX_MAX_ATTEMPTS
SELECT id, topic, event_type, subject, attempts, last_error
FROM outbox_events
WHERE published_at IS NULL AND attempts >= 20
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

On `SIGTERM`/`SIGINT` `cmd/api`: flips `/readyz` to not ready first, stops the six consumers, then the relay, waits for each loop and closes the readers, drains HTTP for up to 10 s, then closes the relay writer and the Postgres pool. There is no drain delay between the readiness flip and the listener close, so the chart sets a `preStop` sleep (`config.preStopSleepSeconds`, default 5 s) and `terminationGracePeriodSeconds: 45`; it refuses to render a grace period below the sleep plus 20 s (10 s of HTTP drain and 10 s for a consumer handler).

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
