---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
description: "The domain types of outbound-shipping and its hexagonal ports and adapters, drawn from the Go code."
---

# Class diagram

Two diagrams: the domain model (aggregates, value objects, events) and the hexagonal structure (use cases, ports, adapters). Go has no classes: the boxes are structs and interfaces, the methods are the exported ones that matter.

## Domain model

```mermaid
classDiagram
    class OutboundLoad {
        +LoadID id
        +SiteID site
        +CptID cpt
        +OperatingDate operatingDate
        +ShipMethod shipMethod
        +time cutoff
        +State state
        +DoorCode door
        +TrailerID trailer
        +AppointmentID appointment
        +ShortShip[] shortShips
        +int packageCount
        +int64 version
        +Plan(PlanInput)
        +AssignDoor(AssignDoorInput)
        +AttachTrailer(TrailerDocked)
        +StartLoading(at)
        +CloseLoad(CloseInput)
        +ReopenLoad(at)
        +ConfirmShipment(ConfirmInput)
        +CancelLoad(reason, at)
        +PullEvents()
    }
    class ShippingPackage {
        +PackageID id
        +SiteID site
        +string sourceOrderID
        +string workUnitRef
        +float weightG
        +LoadID loadID
        +string stagingLoc
        +State state
        +bool missedFlagged
        +int64 version
        +Expect(ExpectInput)
        +AssignToLoad(LoadID)
        +Stage(StageInput)
        +Load(LoadInput)
        +Unload(reason, at)
        +MarkShipped(at)
        +MarkShortShipped(reason, at)
        +FlagMissedLoad(cutoff, at)
        +PullEvents()
    }
    class BillOfLading {
        +BolNumber bolNumber
        +LoadID loadID
        +SiteID site
        +string carrier
        +string carrierScac
        +TrailerID trailerID
        +string sealNumber
        +int packageCount
        +float totalWeightG
        +State state
        +int64 version
        +Issue(IssueInput)
        +Void(reason, at)
        +PullEvents()
    }
    class ShortShip {
        +PackageID packageID
        +string reason
        +Valid()
    }
    class LoadKey {
        +SiteID site
        +CptID cpt
        +OperatingDate date
        +ShipMethod shipMethod
    }
    class Event {
        <<interface>>
        +EventName()
        +OccurredAt()
    }
    OutboundLoad "1" o-- "0..*" ShortShip : records
    OutboundLoad ..> LoadKey : Key()
    ShippingPackage "0..*" --> "0..1" OutboundLoad : loadID
    BillOfLading "0..1" --> "1" OutboundLoad : loadID
    OutboundLoad ..> Event : raises
    ShippingPackage ..> Event : raises
    BillOfLading ..> Event : raises
```

Source: `internal/domain/outboundload/load.go`, `internal/domain/outboundload/commands.go`, `internal/domain/shippingpackage/package.go`, `internal/domain/shippingpackage/commands.go`, `internal/domain/billoflading/bol.go`, `internal/domain/shared/shortship.go`, `internal/domain/shared/ids.go`.

Events raised per aggregate: `OutboundLoad` raises `LoadPlanned`, `LoadDoorAssigned`, `LoadingStarted`, `LoadClosed`, `LoadReopened`, `ShipmentConfirmed`, `LoadCancelled`; `ShippingPackage` raises `PackageStaged`, `PackageLoaded`, `PackageUnloaded`, `PackageMissedLoad`; `BillOfLading` raises `BillOfLadingIssued`, `BillOfLadingVoided`. Each package declares its own `Event` interface and `Header` (id, site, version after the change, instant). Aggregates have private fields and are rebuilt through `Rehydrate`, which validates every field.

## Hexagonal structure

```mermaid
classDiagram
    class RestServer {
        <<inbound adapter>>
        chi router, problems, Idempotency-Key
    }
    class KafkaConsumers {
        <<inbound adapter>>
        fulfillment dock yard cpt layout analytics
    }
    class McpServer {
        <<inbound adapter>>
        loads_at_risk missed_loads
    }
    class UseCases {
        <<application>>
        StagePackage LoadPackage UnloadPackage
        CloseLoad ReopenLoad CancelLoad
        VoidBillOfLading SweepMissedLoads
        ExpectPackage DivertPackage BookAppointment
        DockTrailer RecordDoorWork OnDeparture
        ApplyCptSchedule ApplyLayout ProjectBoard
        Queries
    }
    class Ports {
        <<application ports>>
        UnitOfWork EventSink Clock LoadIDs
        LoadRepo PackageRepo BolRepo
        CptCopy LayoutCopy AppointmentCopy DoorWorkCopy
        ProcessedEvents IdempotencyStore BoardStore
    }
    class Domain {
        <<domain>>
        OutboundLoad ShippingPackage BillOfLading
    }
    class PostgresAdapters {
        <<outbound adapter>>
        repositories, UnitOfWork, OutboxRepo, migrations
    }
    class EventsSink {
        <<outbound adapter>>
        domain events to outbox messages
    }
    class OutboxRelay {
        <<outbound adapter>>
        drain loop plus Kafka RelaySink
    }
    class SystemAdapters {
        <<outbound adapter>>
        Clock RandomIDs
    }
    RestServer --> UseCases
    KafkaConsumers --> UseCases
    McpServer --> UseCases : Queries only
    UseCases --> Domain
    UseCases --> Ports
    PostgresAdapters ..|> Ports
    EventsSink ..|> Ports
    SystemAdapters ..|> Ports
    EventsSink --> PostgresAdapters : outbox insert
    OutboxRelay --> PostgresAdapters : drain
```

Source: `cmd/api/wire.go`, `cmd/api/consumers.go`, `cmd/mcp/main.go`, `internal/application/ports/ports.go`, `internal/application/ports/unit_of_work.go`, `internal/application/usecases/*.go`, `internal/adapters/**`, `internal/architecture/architecture_test.go`.

Notes: `cmd/` is the only place adapters meet (adapters never import each other); `EventsSink` writes through the `Inserter` interface that `postgres.OutboxRepo` implements, wired in `cmd/api/wire.go`. The in-memory adapters in `internal/adapters/outbound/memory` implement the same ports for tests only and are never imported by `cmd/`.
