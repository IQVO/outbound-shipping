---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
description: "Domain message flow models of outbound-shipping: plan to ship-confirm, the early departure, diversion and the missed-load sweep."
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling): actors and contexts exchange **commands**, **events** and **queries**. In the diagrams, rectangles are contexts, rounded boxes are people or schedulers, solid arrows are events on Kafka, thick arrows are commands over REST, dotted arrows are queries.

Source: `internal/application/usecases/*.go`, `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/events/*.go`, `features/ship_confirm_on_departure.feature`, [ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure).

## 1. From manifest to ship-confirm (happy path)

```mermaid
flowchart LR
    FE[fulfillment-execution]
    PP[process-path-management]
    FL[facility-layout]
    DS[dock-scheduling]
    YM[yard-management]
    OS{{outbound-shipping}}
    OM[order-management]
    DK([dock operator])
    SV([dock supervisor])

    PP -->|CPTScheduleChanged| OS
    FL -->|ZoneRegistered, LocationSlotRegistered| OS
    FE -->|PackageManifested| OS
    OS -->|LoadPlanned| OS
    DS -->|DockAppointmentBooked| OS
    OS -->|LoadDoorAssigned| OS
    YM -->|TrailerDocked| OS
    OS -->|LoadingStarted| YM
    DK ==>|StagePackage| OS
    OS -->|PackageStaged| OS
    DK ==>|LoadPackage| OS
    OS -->|PackageLoaded| OS
    SV ==>|CloseLoad| OS
    OS -->|LoadClosed| YM
    YM -->|TrailerDeparted with seal| OS
    OS -->|ShipmentConfirmed| OM
    OS -->|BillOfLadingIssued| OS
```

Source: `internal/application/usecases/expect_package.go`, `book_appointment.go`, `dock_trailer.go`, `scan_package.go`, `load_commands.go`, `on_departure.go`. The self arrows are events folded by the context's own projector (analytics only). The arrows to yard-management for `LoadingStarted` and `LoadClosed` are the intended edge of [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue); the yard has no handler for them yet.

Reading: the cutoff schedule and staging locations are copied in first and passively. The first manifested package plans the load; a dock appointment gives it a door; the yard's docking starts loading. Operators scan; the supervisor closes. Only the yard's departure, never a user command, confirms the shipment.

## 2. The departure that comes before the close

```mermaid
flowchart LR
    YM[yard-management]
    OS{{outbound-shipping}}
    EM[exception-management]
    OM[order-management]
    SV([dock supervisor])

    YM -->|TrailerDeparted| OS
    OS -->|PackageMissedLoad per package not loaded| EM
    OS -->|PackageMissedLoad| OM
    SV ==>|CloseLoad with short_ships| OS
    OS -->|LoadClosed| OS
```

Source: `internal/application/usecases/on_departure.go` (`flagWithoutClose`), `features/ship_confirm_on_departure.feature` (scenarios 2 and 3). The load is not confirmed and no bill of lading exists. After the supervisor short-ships and closes, a re-delivery of the same departure confirms nothing either (the inbox claim). exception-management opens one case per package; order-management re-promises the order.

## 3. A diverted package

```mermaid
flowchart LR
    FE[fulfillment-execution]
    OS{{outbound-shipping}}
    SV([dock supervisor])
    FE -->|PackageDiverted| OS
    SV ==>|CloseLoad| OS
    OS -->|LoadClosed with short_shipped_count| OS
```

Source: `internal/application/usecases/divert_package.go`, `load_commands.go`, `features/ship_confirm_on_departure.feature` (scenario 5). The diverted package becomes `SHORT_SHIPPED` with reason `DIVERTED`, raises no event, and no longer blocks the close. The close records it once in the load's short-ship list.

## 4. The missed-load sweep

```mermaid
flowchart LR
    SC([scheduler])
    OS{{outbound-shipping}}
    EM[exception-management]
    OM[order-management]
    SC ==>|POST missed-load-sweeps| OS
    OS -->|PackageMissedLoad once per package| EM
    OS -->|PackageMissedLoad| OM
```

Source: `internal/application/usecases/sweep_missed.go`, `features/ship_confirm_on_departure.feature` (scenario 6). Running the sweep twice flags each package once.

## 5. Reads

```mermaid
flowchart LR
    CL([REST client])
    AG([MCP agent])
    OS{{outbound-shipping}}
    CL -.->|GET loads, packages, bills of lading| OS
    CL -.->|GET load-adherence| OS
    AG -.->|loads_at_risk, missed_loads| OS
    AG -.->|resource loads by id| OS
```

Source: `internal/application/usecases/queries.go`, `internal/adapters/inbound/mcp/tools.go`, `internal/adapters/inbound/http/handlers.go`. REST reads of loads and packages hit the OLTP tables; the adherence report, `loads_at_risk` and the board read the projector-fed `load_board`, which may lag by a few seconds.
