---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
description: "Runtime sequences of the main outbound-shipping flows: planning, door and docking, scans, close, ship-confirm, early departure, sweep and the outbox path."
---

# Sequence diagrams

One diagram per main flow, drawn from the use-case code. Every consumer flow starts with the inbox claim and runs in one unit of work, so a failed step rolls the claim back and the message is retried. "Outbox" below means two rows per event (integration and analytics twin) inserted in the same transaction.

## 1. Manifest plans or joins the load

```mermaid
sequenceDiagram
    participant K as Kafka fulfillment.events
    participant C as fulfillment consumer
    participant U as ExpectPackage
    participant DB as Postgres
    K->>C: PackageManifested
    C->>U: Handle(eventId, Manifest)
    U->>DB: claim processed_events
    U->>DB: find package by id
    U->>DB: read cpt_schedules for the site
    U->>U: ResolveCutoff in site timezone
    alt no site or cpt or no schedule entry
        U-->>C: ignored
    else cutoff key resolved
        U->>DB: find open load by key
        opt no open load
            U->>U: Plan load (LoadPlanned)
            U->>DB: insert load and outbox rows
        end
        U->>DB: insert package EXPECTED assigned to the load
        U-->>C: applied
    end
    C->>K: commit offset
```

Source: `internal/application/usecases/expect_package.go`, `internal/application/usecases/apply_cpt_schedule.go` (`ResolveCutoff`), `internal/adapters/inbound/kafka/fulfillment_consumer.go`. A raced insert fails on `uq_open_load_per_cutoff`, rolls the whole unit back and `Handle` retries once on a fresh transaction, which then finds the winner's load.

## 2. Door assignment and docking start loading

```mermaid
sequenceDiagram
    participant D as dock-scheduling
    participant Y as yard-management
    participant B as BookAppointment
    participant T as DockTrailer
    participant L as OutboundLoad
    participant DB as Postgres
    D->>B: DockAppointmentBooked OUTBOUND with cpt_id
    B->>DB: save dock_appointments copy
    B->>DB: list open loads of site and cpt
    B->>L: AssignDoor(door, appointment)
    L-->>B: LoadDoorAssigned
    B->>DB: save load and outbox rows
    Y->>T: TrailerDocked at the door
    T->>DB: list open loads of site and door
    T->>L: AttachTrailer(trailer, door)
    T->>L: StartLoading
    L-->>T: LoadingStarted
    T->>DB: save load and outbox rows
```

Source: `internal/application/usecases/book_appointment.go`, `internal/application/usecases/dock_trailer.go`, `internal/domain/outboundload/commands.go`. An appointment that is inbound, names no cpt, or arrives before a load of that cpt exists only updates the copy.

## 3. Scans: stage, load, unload

```mermaid
sequenceDiagram
    participant Op as Dock operator
    participant H as REST handler
    participant I as Idempotency middleware
    participant U as StagePackage or LoadPackage
    participant P as ShippingPackage
    participant DB as Postgres
    Op->>H: POST stagings or loadings with Idempotency-Key
    H->>I: wrap request
    I->>DB: begin unit of work and look up the key
    I->>U: Handle
    U->>DB: get package, layout copy or load
    U->>P: Stage or Load (invariants)
    P-->>U: PackageStaged or PackageLoaded
    U->>DB: save with expected version and outbox rows
    I->>DB: store the 2xx answer and commit
    I-->>Op: 200 package
```

Source: `internal/application/usecases/scan_package.go`, `internal/adapters/inbound/http/idempotency.go`, `internal/adapters/inbound/http/handlers.go`. A refusal (`422 not-staging-location`, `409 wrong-load`) rolls the unit back and the 4xx answer is stored under the key; a 5xx is never stored.

## 4. Close the load

```mermaid
sequenceDiagram
    participant S as Supervisor
    participant U as CloseLoad
    participant L as OutboundLoad
    participant P as ShippingPackage
    participant DB as Postgres
    S->>U: POST closures with short_ships
    U->>DB: get load and list its packages
    loop each commanded short-ship
        U->>P: MarkShortShipped(reason)
        U->>DB: save package
    end
    U->>L: CloseLoad(outstanding, short_ships, loaded count)
    alt a package is neither loaded nor short-shipped
        L-->>U: ErrPackagesOutstanding
        U-->>S: 409 packages-outstanding
    else all accounted for
        L-->>U: LoadClosed
        U->>DB: save load and outbox rows
        U-->>S: 200 load CLOSED
    end
```

Source: `internal/application/usecases/load_commands.go` (`CloseLoad`, `applyShortShips`), `internal/domain/outboundload/commands.go`.

## 5. Departure confirms a closed load

```mermaid
sequenceDiagram
    participant Y as Kafka yard.events
    participant U as OnDeparture
    participant L as OutboundLoad
    participant P as ShippingPackage
    participant B as BillOfLading
    participant DB as Postgres
    Y->>U: TrailerDeparted with seal
    U->>DB: claim, find latest load of site and trailer
    U->>DB: list packages of the load
    loop each LOADED package
        U->>P: MarkShipped
        U->>DB: save package
    end
    U->>DB: read carrier and SCAC from the appointment copy
    U->>L: ConfirmShipment(departure, carrier, orders)
    L-->>U: ShipmentConfirmed
    U->>DB: save load and outbox rows
    U->>B: Issue(seal, count, weight)
    B-->>U: BillOfLadingIssued
    U->>DB: save bill of lading and outbox rows
```

Source: `internal/application/usecases/on_departure.go` (`confirm`, `markShipped`, `carrierOf`), `internal/domain/outboundload/commands.go`, `internal/domain/billoflading/bol.go`. All inside one unit of work. Without a seal the departure is ignored; an empty carrier makes `Issue` fail, the unit rolls back and the message is retried then dead-lettered.

## 6. Departure while the load is still loading

```mermaid
sequenceDiagram
    participant Y as Kafka yard.events
    participant U as OnDeparture
    participant P as ShippingPackage
    participant DB as Postgres
    Y->>U: TrailerDeparted
    U->>DB: claim, find latest load of site and trailer
    Note over U: load is LOADING, never confirmed
    U->>DB: list packages of the load
    loop each package not loaded
        U->>P: FlagMissedLoad(cutoff)
        alt first time
            P-->>U: PackageMissedLoad
            U->>DB: save package and outbox rows
        else already flagged
            P-->>U: no event
        end
    end
```

Source: `internal/application/usecases/on_departure.go` (`flagWithoutClose`), `internal/domain/shippingpackage/commands.go` (`FlagMissedLoad`), `features/ship_confirm_on_departure.feature` (scenarios 2 and 3). The load stays `LOADING` for a supervisor.

## 7. Missed-load sweep

```mermaid
sequenceDiagram
    participant Cron as Scheduler
    participant U as SweepMissedLoads
    participant P as ShippingPackage
    participant DB as Postgres
    Cron->>U: POST missed-load-sweeps with site_id
    U->>DB: list EXPECTED or STAGED packages of open loads past cutoff
    loop each sweepable package
        U->>P: FlagMissedLoad(cutoff)
        alt not yet flagged
            P-->>U: PackageMissedLoad
            U->>DB: save package and outbox rows
        else already flagged
            Note over U: nothing published
        end
    end
    U-->>Cron: 200 flagged and swept_at
```

Source: `internal/application/usecases/sweep_missed.go`, `features/ship_confirm_on_departure.feature` (scenario 6).

## 8. The outbox path

```mermaid
sequenceDiagram
    participant UC as Use case
    participant DB as Postgres outbox_events
    participant R as Outbox relay
    participant K as Kafka
    participant A as analytics consumer
    participant LB as load_board
    UC->>DB: insert 2 rows per event in the aggregate transaction
    loop every OUTBOX_INTERVAL_MS
        R->>DB: claim batch FOR UPDATE SKIP LOCKED
        R->>K: write events and analytics topics RequireAll
        R->>DB: set published_at or bump attempts
    end
    K->>A: analytics message
    A->>DB: claim and fold into load_board
    A->>LB: update counts and stamps
```

Source: `internal/adapters/outbound/events/sink.go`, `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/postgres/outbox_repository.go`, `internal/application/usecases/project_board.go`.
