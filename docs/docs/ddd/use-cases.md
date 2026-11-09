---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every outbound-shipping application use case with its trigger, inputs, invariants and the events it raises."
---

# Use cases

Source: `internal/application/usecases/*.go`, `internal/adapters/inbound/http/{server,errors}.go`, `internal/adapters/inbound/kafka/*.go`, `internal/adapters/inbound/mcp/tools.go`.

Seventeen use cases live in `internal/application/usecases`: eight REST commands and nine consumer policies, plus the read side (`Queries`). The runtime sequence of the main ones is on [Sequence diagrams](/docs/ddd/sequence-diagrams); the events are described on [Events](/docs/api-reference/events).

## Conventions

- **One transaction.** Every use case runs in one `ports.UnitOfWork`: the aggregate writes, the outbox rows and, for consumers, the inbox claim commit or roll back together.
- **Every event is two outbox rows** (integration topic and analytics twin, same CloudEvents id).
- **Optimistic concurrency.** Saves carry the version the aggregate was loaded at; a mismatch is `repository.ErrConcurrentModification` (`409 concurrent-modification`).
- **Consumers return an outcome**: `applied`, `duplicate` (the CloudEvents id was already claimed for that consumer) or `ignored` (nothing to do, still claimed). `ErrInvalidEvent` is a deterministic payload problem and is dead-lettered without retry.
- **REST errors** are `application/problem+json` with `type` `https://errors.outbound-shipping.warehouse-systems.dev/<slug>`. Every POST needs an `Idempotency-Key`.

## Error catalogue (`problemCatalogue`)

| Error | HTTP | Slug |
|---|---|---|
| malformed body, bad parameter, unknown query parameter, `ErrInvalidListQuery`, `ErrCancelReasonRequired`, `ErrRequiredField`, `ErrNoTimestamp`, `ErrVoidReasonRequired` | 400 | `bad-request`, `invalid-parameter`, `unknown-query-parameter` |
| missing / reused `Idempotency-Key` | 400 / 409 | `idempotency-key-required` / `idempotency-key-reused` |
| `shippingpackage.ErrNotStagingLocation` | 422 | `not-staging-location` |
| `shippingpackage.ErrWrongLoad` | 409 | `wrong-load` |
| `shippingpackage.ErrPackageOnAnotherLoad` | 409 | `package-on-another-load` |
| `shippingpackage.ErrAlreadyShipped` | 409 | `already-shipped` |
| `shippingpackage.ErrInvalidTransition` | 409 | `package-state` |
| `outboundload.ErrPackagesOutstanding` | 409 | `packages-outstanding` |
| `outboundload.ErrTrailerNotDocked`, `ErrWrongDoor` | 409 | `trailer-not-docked` |
| `outboundload.ErrLoadNotClosed` | 409 | `load-not-closed` |
| `outboundload.ErrLoadConfirmed` | 409 | `load-confirmed` |
| `outboundload.ErrDuplicateOpenLoad` | 409 | `duplicate-open-load` |
| `outboundload.ErrInvalidTransition` | 409 | `load-state` |
| `usecases.ErrLoadHasPackages` | 409 | `load-has-packages` |
| `billoflading.ErrBolAlreadyIssued` | 409 | `bol-already-issued` |
| `billoflading.ErrBolAlreadyVoided` | 409 | `bol-already-voided` |
| `billoflading.ErrSealMissing` | 409 | `seal-missing` |
| `repository.ErrLoadNotFound`, `ErrPackageNotFound`, `ErrBolNotFound` | 404 | `load-not-found`, `package-not-found`, `bol-not-found` |
| `repository.ErrConcurrentModification` | 409 | `concurrent-modification` |
| anything else | 500 | `internal-error` |
| a dependency check failure on `/readyz` | 503 | `dependency-unavailable` |

## REST commands

### StagePackage: `POST /packages/{packageId}/stagings`

- **Trigger:** a dock operator scans a package into a staging location.
- **Inputs:** path `packageId`; body `{"staging_location": "<code>"}` (required).
- **Flow:** load the package, read the location from the layout copy, `ShippingPackage.Stage`.
- **Invariants:** the location must be a `Staging` location of the package's own site (`ErrNotStagingLocation`); only an `EXPECTED` package stages (a `STAGED` one at the same location is a no-op; any other state is `ErrInvalidTransition`).
- **Raises:** `PackageStaged`.

### LoadPackage: `POST /packages/{packageId}/loadings`

- **Trigger:** a dock operator scans a package onto its load.
- **Inputs:** body `{"load_id", "door_code"}` (both required).
- **Invariants:** the package must be assigned to a load, that load must be `load_id`, and `door_code` must equal the load's assigned door (`ErrWrongLoad` otherwise, including when the load has no door yet); a shipped package refuses with `ErrAlreadyShipped`; `EXPECTED` or `STAGED` only.
- **Raises:** `PackageLoaded`.

### UnloadPackage: `POST /packages/{packageId}/unloadings`

- **Trigger:** an operator takes a loaded package back off the load.
- **Inputs:** body `{"reason": "<text>"}` (optional).
- **Invariants:** only `LOADED` unloads (to `STAGED`); a `SHIPPED` package refuses `ErrAlreadyShipped`.
- **Raises:** `PackageUnloaded` (the reason is omitted from the event when empty).

### CloseLoad: `POST /loads/{loadId}/closures`

- **Trigger:** a dock supervisor closes the load.
- **Inputs:** body `{"short_ships": [{"package_id", "reason"}]}` (optional list; each entry needs both fields).
- **Flow:** mark each commanded short-ship `SHORT_SHIPPED` first, then collect the packages still outstanding. A package already short-shipped by the diversion policy is recorded once with reason `DIVERTED`.
- **Invariants:** the load must be `LOADING`; no package may be neither loaded nor short-shipped (`ErrPackagesOutstanding`); an unknown package in `short_ships` is `package-not-found`.
- **Raises:** `LoadClosed` (`package_count` = loaded packages, `short_shipped_count`).

### ReopenLoad: `POST /loads/{loadId}/reopenings`

- **Trigger:** a supervisor reopens a closed load.
- **Invariants:** `CLOSED` only (to `LOADING`); a `SHIP_CONFIRMED` load never reopens (`ErrLoadConfirmed`).
- **Raises:** `LoadReopened`.

### CancelLoad: `POST /loads/{loadId}/cancellations`

- **Trigger:** a supervisor cancels a planned load.
- **Inputs:** body `{"reason"}` (required).
- **Invariants:** `PLANNED` only; a load that already has packages assigned refuses `ErrLoadHasPackages` (short-ship and close instead).
- **Raises:** `LoadCancelled`.

### VoidBillOfLading: `POST /bills-of-lading/{bolNumber}/voids`

- **Trigger:** a supervisor voids an issued bill of lading.
- **Inputs:** body `{"reason"}` (required).
- **Invariants:** `ISSUED` only (`ErrBolAlreadyVoided`). The shipment is not undone.
- **Raises:** `BillOfLadingVoided`.

### SweepMissedLoads: `POST /missed-load-sweeps`

- **Trigger:** a scheduler around a cutoff.
- **Inputs:** body with `site_id` (required) and `cutoff` (optional RFC 3339, default now).
- **Flow:** list the site's `EXPECTED` or `STAGED` packages on still-open loads whose cutoff has passed, flag each with `FlagMissedLoad`.
- **Invariants:** at most once per package (the `missed_flagged` column); a re-run flags nothing and publishes nothing.
- **Returns:** `{"flagged": N, "swept_at": ...}`. **Raises:** `PackageMissedLoad` per newly flagged package.

## Consumer policies

Each handles one CloudEvents `type` on one topic (see [Integration](/docs/ecosystem/integration)), claims `(consumer, event id)` first and returns an outcome.

### ExpectPackage: `PackageManifested`

PlanLoadFromCutoff plus AssignPackageToLoad. Resolves the manifest's `cpt` instant in the site's timezone against the local schedule (`ResolveCutoff`) to a `(cpt, operating date, ship method)` key; finds the open load of that key or plans one; records the package `EXPECTED` and assigns it.
- **Ignored when:** no `site_id` or `cpt` on the manifest, no schedule copy for the site, or no schedule entry matches the cutoff's local time and weekday.
- **Invariants:** one open load per key (`ErrDuplicateOpenLoad`, backed by `uq_open_load_per_cutoff`; a raced insert retries the whole application once and joins the winner); an already-known package id is a no-op.
- **Raises:** `LoadPlanned` (only when this call planned the load).

### DivertPackage: `PackageDiverted`

DropDiverted. An `EXPECTED` or `STAGED` package becomes `SHORT_SHIPPED` with reason `DIVERTED`, so its load can still close. Unknown, loaded, shipped or already short-shipped packages are ignored. **Raises:** nothing.

### BookAppointment: `DockAppointmentBooked`

DoorFromAppointment. Always saves the appointment to the local copy. For an `OUTBOUND` appointment naming a `cpt_id`, assigns the door of that appointment to the first open load of `(site, cpt)` (`AssignDoor`, allowed while `PLANNED` or `LOADING`; a later appointment re-assigns).
- **Ignored (copy still saved) when:** the appointment is inbound, names no cpt, or no open load of that cpt exists yet.
- **Raises:** `LoadDoorAssigned`.

### DockTrailer: `TrailerDocked`

StartOnDock. Finds the open load at `(site, door)` (`PLANNED` first), attaches the trailer (`ErrTrailerNotDocked` when the load has no door, `ErrWrongDoor` when the door differs) and, when the load is still `PLANNED`, starts loading. A re-dock at a `LOADING` load attaches silently.
- **Ignored when:** no open load uses that door.
- **Raises:** `LoadingStarted` (only when the state moved). There is no REST route to start loading.

### RecordDoorWork: `DoorWorkCompleted`

Records the completion in `door_work_completions`. No state change in v1. **Raises:** nothing.

### OnDeparture: `TrailerDeparted`

ConfirmOnDeparture and FlagDepartureWithoutClose ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)), split on the state of the latest load of `(site, trailer)`:

| Load state | Effect |
|---|---|
| `CLOSED` and the departure carries a `seal_number` | `ConfirmShipment` (`SHIP_CONFIRMED`), every `LOADED` package becomes `SHIPPED`, one `BillOfLading` is issued with the carrier and SCAC read from the appointment copy, the seal, the package count and total weight. |
| `CLOSED` and no seal | Ignored: the document must certify the yard's seal; the load stays `CLOSED`. |
| `LOADING`, or `PLANNED` with a trailer attached | Never confirmed. Every package not loaded raises `PackageMissedLoad` once; the load is left for a supervisor. |
| `SHIP_CONFIRMED` or `CANCELLED` | Ignored. |
| no load has that trailer | Ignored. |

- **Idempotency in three layers:** the inbox claim (same CloudEvents id), the `SHIP_CONFIRMED` refusal (any later departure) and the bill of lading's once-per-load invariant (`UNIQUE load_id`).
- **Invariants:** only a `CLOSED` load confirms (`ErrLoadNotClosed`); the bill of lading needs a non-empty carrier, trailer and seal and at least one shipped package.
- **Raises:** `ShipmentConfirmed` (orders = distinct `source_order_id` of the shipped packages, sorted) and `BillOfLadingIssued`, or `PackageMissedLoad`.

### ApplyCptSchedule: `CPTScheduleChanged`

Replaces the site's whole schedule copy (`cpt_schedules`): last writer wins per site. Needs site, timezone, version at least 1, a changed-at instant, and every entry needs id, local time and ship method. **Raises:** nothing.

### ApplyLayout: `ZoneRegistered`, `LocationSlotRegistered`

Saves the zone-to-site mapping and the location with its role into `zones` and `locations`; they may arrive in any order. A location's site is its zone's site; an empty role means Storage and never stages. **Raises:** nothing.

### ProjectBoard: own analytics stream

Folds `LoadPlanned`, `PackageStaged`, `PackageLoaded`, `PackageMissedLoad`, `LoadClosed`, `ShipmentConfirmed` and `LoadCancelled` into `load_board` (staged, loaded and missed counts, closed-on-time, confirmed and cancelled stamps). Other own types are claimed and ignored. **Raises:** nothing.

## Queries (`Queries`)

| Query | Served by | Notes |
|---|---|---|
| `GetLoad`, `ListLoads` | `GET /loads/{loadId}`, `GET /loads`; MCP resource `warehouse://outbound-shipping/loads/{loadId}` | keyset pagination, `limit` 1 to 200 (default 50) |
| `GetPackage`, `ListLoadPackages` | `GET /loads/{loadId}/packages` | filter by package state |
| `GetBillOfLading` | `GET /bills-of-lading/{bolNumber}` | |
| `AdherenceReport` | `GET /reports/load-adherence` | reads `load_board`; `adherence` = `loads_closed / loads_total` |
| `LoadsAtRisk` | MCP `loads_at_risk` | open loads whose cutoff is within the horizon (default 4 h) |
| `MissedLoads` | MCP `missed_loads` | packages that raised `PackageMissedLoad`, newest first |
