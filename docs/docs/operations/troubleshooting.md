---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause, check and fix for outbound-shipping failures, taken from the failure modes in the code."
---

# Troubleshooting

Each entry is a symptom, the cause in the code, how to check it and the fix. Source: `cmd/api/config.go`, `internal/application/usecases/*.go`, `internal/adapters/inbound/kafka/*.go`, `internal/adapters/inbound/http/errors.go`, `internal/adapters/outbound/outbox/relay.go`, `migrations/`.

Most consumer problems are visible in one log line per message: `event processed` with `outcome` `applied`, `duplicate` or `ignored`. `ignored` is not an error; it means the use case found nothing to do, and the causes are listed below.

## Boot

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| `api exited with error` naming `DATABASE_URL is required`, `KAFKA_BROKERS is required` or `<X>_CONSUMER_GROUP is required` | `loadConfig` collects every missing variable and fails once | the joined error lists each missing name | Set them all; see [Configuration](/docs/operations/configuration). There is no default and no in-memory fallback. |
| `run migrations: ...` at boot | the DSN cannot run DDL (a pooler in transaction mode, a read-only role) or an earlier run left the schema dirty | the error text; `SELECT * FROM schema_migrations` | Provide `MIGRATIONS_DATABASE_URL` with a direct connection and a role that owns the schema. If `dirty = true`, repair the schema by hand and reset the version; there is no automatic down-migration. |
| `cmd/mcp` exits `DATABASE_URL is required: this context runs on real Postgres` | no `DATABASE_URL` | the exit log | Set it; `cmd/mcp` reads no other mandatory variable. |
| Process up, `/readyz` returns 503 `dependency-unavailable` | Postgres ping failed or the first broker in `KAFKA_BROKERS` does not accept TCP | the problem `detail` names `postgres` and/or `kafka`; the log line `readiness: dependency is not ready` | Fix the network or credentials. List a reachable broker **first** in `KAFKA_BROKERS`: only the first one is dialled. |
| `/readyz` is 200 but nothing is consumed | readiness dials TCP only, and consumers connect lazily | `kafka reader error; retrying` lines with `consumer`, `topic`, `detail` | Fix the group join or ACL; a topic that does not exist yet is retried silently by kafka-go but logged by the error logger. |

## A load is not planned, or does not progress

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| `PackageManifested` logged `outcome=ignored`, no package and no load | the manifest lacks `site_id` or `cpt` (producer fields are additive and omitted when unknown, [ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)), or the site has no schedule copy, or the manifest's cutoff matches no entry of the schedule | `SELECT * FROM cpt_schedules WHERE site_id = ...`; compare the manifest's `cpt` instant, converted to the schedule's `timezone`, with each entry's `local_time` and `days_of_week` (the match is on the local `HH:MM` and, when listed, the weekday) | Make the producer send `site_id` and `cpt`; make sure `CPTScheduleChanged` for the site was consumed (`CPT_CONSUMER_GROUP`). The package is **not** stored when ignored, but the event's CloudEvents id **is** claimed in `processed_events` (an `ignored` outcome commits), so replaying the same message is a `duplicate`. Recover by having the producer re-emit the manifest with a new id, or by deleting that row: `DELETE FROM processed_events WHERE consumer = 'outbound-shipping-fulfillment' AND event_id = '<id>'` and then replaying it. |
| `LoadDoorAssigned` never raised | the appointment is not `OUTBOUND`, names no `cpt_id`, or arrived **before** any open load of that cpt existed | `SELECT * FROM dock_appointments WHERE appointment_id = ...`; `outcome=ignored` on the `DockAppointmentBooked` line | The appointment copy is saved either way, but the door is assigned only when a later appointment event for the same cpt arrives while a load is open. Re-publish or re-book the appointment after the load exists. This is a real ordering gap of `BookAppointment`. |
| Loading never starts after the trailer docked | `TrailerDocked` was `ignored`: no open load has that `(site, door)`, because no door was assigned yet; or the load is not `PLANNED` | `SELECT load_id, state, door_code, trailer_id FROM outbound_loads WHERE site_id = ... AND door_code = ...` | Fix the door assignment first, then a new `TrailerDocked` (new CloudEvents id) at that door starts loading. Loading starts only from a docking; there is no REST route for it. |
| Two loads for one cutoff | cannot happen: the partial unique index `uq_open_load_per_cutoff` allows one `PLANNED`/`LOADING` load per `(site, cpt, operating_date, ship_method)`; a raced insert is retried once and joins the winner | `\d outbound_loads` | none |

## REST refusals

Every error is `application/problem+json` with `type` `https://errors.outbound-shipping.warehouse-systems.dev/<slug>`. The catalogue is in the [API overview](/docs/api-reference).

| Status and slug | Cause | Fix |
|---|---|---|
| `400 idempotency-key-required` | a POST without `Idempotency-Key` | Send the header (spec: 16 to 128 characters; the code enforces 128 at most). |
| `409 idempotency-key-reused` | the key was used with another method, path or body | Use a new key for a new request. A replay of the same request returns the stored answer with `Idempotency-Replayed: true`. |
| `400 unknown-query-parameter` | a query parameter the route does not declare | Remove it. `GET /loads` takes `site_id`, `state`, `operating_date`, `limit`, `cursor`. |
| `422 not-staging-location` | the location is not a `Staging` location of the package's own site, or the layout copy has no such location yet (`zones` and `locations` come from `warehouse.facility.events`) | `SELECT l.location_code, l.role, z.site_code FROM locations l LEFT JOIN zones z USING (zone_id) WHERE l.location_code = ...`. An empty `role` means Storage and never stages. |
| `409 wrong-load` | the package is on another load, has no load, or the door scanned is not the load's door (a load with no door assigned refuses every loading) | `GET /loads/{id}` for `door_code`; check the package's `load_id` | Scan through the load's door; fix the door assignment if it is empty. |
| `409 package-state` / `409 already-shipped` | the scan is not allowed from the package's state; a shipped package cannot be unloaded | `GET /loads/{id}/packages?state=...` | none for `already-shipped`: the truck has left. |
| `409 packages-outstanding` | a package of the load is neither loaded nor short-shipped | list the load's packages in `EXPECTED` or `STAGED` | Load them, or close with `short_ships` naming each with a reason. |
| `409 load-state`, `load-not-closed`, `load-confirmed` | the command does not fit the load's state; a `SHIP_CONFIRMED` load never changes | `GET /loads/{id}` | none; confirmation is final. Void the bill of lading if it was wrong; the shipment is not undone. |
| `409 load-has-packages` | cancelling a load that already has packages | | Short-ship the packages and close instead. |
| `409 concurrent-modification` | optimistic-lock failure (`WHERE version = ...` matched no row) | re-read and retry | Retry with a new `Idempotency-Key`; the same key replays the stored 409. |
| `404 load-not-found`, `package-not-found`, `bol-not-found` | unknown id | | Packages exist only after a manifest was applied. |

## Ship-confirm does not happen

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Trailer departed, load still `CLOSED`, log `outcome=ignored` | the departure carried **no `seal_number`**: a departure without a seal confirms nothing, because the bill of lading must certify the yard's seal | the `TrailerDeparted` payload | yard-management must record the seal; a later `TrailerDeparted` with a seal (new CloudEvents id) confirms. |
| Departure message in `warehouse.yard-management.events.dlq`, load `CLOSED` | the confirm failed inside the transaction and rolled back, most often because the appointment copy has no `carrier`: the bill of lading refuses an empty carrier (`billoflading.ErrRequiredField`), which is treated as transient, retried 5 times, then dead-lettered | `x-dlq-error` header on the DLQ record; `SELECT carrier FROM dock_appointments WHERE appointment_id = (SELECT appointment_id FROM outbound_loads WHERE load_id = ...)` | Make sure the dock appointment event carries the carrier, then republish the departure to the yard topic. |
| Departure while the load is `LOADING`: no `ShipmentConfirmed`, `PackageMissedLoad` events | by design ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)): the truck left before the close, nothing is confirmed, and every package not loaded raises `PackageMissedLoad` once | `missed_loads` MCP tool, `outbox_events` for `PackageMissedLoad` | Short-ship what missed and close; see the [runbook](/docs/operations/runbook). |
| Departure for a trailer this service never saw | `outcome=ignored`: no load has that `(site, trailer)` attached | | none; not ours to confirm. |

## Kafka and the outbox

| Symptom | Cause | Check | Fix |
|---|---|---|---|
| Events missing downstream, `outbox_events` backlog growing | the relay cannot reach the broker, or the database is slow | `outbox relay pass failed` logs; `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL` | Restore the broker; the relay resumes by itself. |
| One event never arrives, others do | the row reached `OUTBOX_MAX_ATTEMPTS` and is parked | the ERROR `outbox relay: row reached the attempt cap...`; `attempts >= 10` rows | Fix the cause in `last_error`, then `UPDATE outbox_events SET attempts = 0 WHERE id = ...`. |
| A message in `<topic>.dlq` | poison payload (missing id, site or door, undecodable `data`) or five failed attempts | the `x-dlq-error`, `x-dlq-source-*` headers; the `dead-lettering` log lines | Fix the producer or the cause, republish the value to the original topic. Messages that are not CloudEvents are skipped, not dead-lettered. |
| A topic stopped being consumed, process healthy | the consumer loop exited (`kafka consumer stopped` in the log) while probes stay green | consumer-group lag; the log line | Restart the pod; alert on that line. |
| Everything replays after a deploy | a consumer group id changed (or was derived from the pod name) | the group ids in the environment | Keep group ids stable. A replay is safe (the inbox dedupes) but re-reads the whole topic. |
| Duplicate downstream messages | the relay is at-least-once: a crash between send and `UPDATE published_at` re-sends with the same CloudEvents `id` | | Consumers dedupe on `id`. |

## Read models and MCP

| Symptom | Cause | Fix |
|---|---|---|
| `GET /reports/load-adherence` or `loads_at_risk` empty or stale | the board (`load_board`) is fed by this service's own analytics consumer; it lags or its group is stuck | Check `ANALYTICS_CONSUMER_GROUP` lag and `SELECT max(updated_at) FROM load_board`. The OLTP endpoints (`/loads`, `/packages`) are always current. |
| MCP tool result `invalid-limit` / `invalid-site-id` | `limit` above 200, or a blank `site_id` for `missed_loads` | Fix the input. |
| MCP client sees no write tools | by design: `cmd/mcp` is read-only (L1) | Use REST for every write. |
