---
id: context-map
title: Context map
sidebar_label: Context map
description: "outbound-shipping's neighbourhood on the fleet context map, with the upstream and downstream side, pattern and channel of every relationship."
---

# Context map

Following ddd-crew [Context Mapping](https://github.com/ddd-crew/context-mapping). This is **outbound-shipping's slice** of the fleet map: every relationship it has, with the upstream (U) and downstream (D) side, the pattern and the channel. Abbreviations: OHS = Open Host Service, PL = Published Language, CF = Conformist, C/S = Customer/Supplier.

```mermaid
flowchart LR
    FE["fulfillment-execution<br/>Core"]
    PP["process-path-<br/>management"]
    FL["facility-layout"]
    DS["dock-<br/>scheduling"]
    YM["yard-management"]
    OS["outbound-shipping<br/>Supporting"]
    OM["order-management"]
    NF["network-fulfillment"]
    EM["exception-<br/>management"]
    YM2["yard-management"]
    DK([dock operators])
    SC([scheduler])
    AG([MCP agents])

    FE -->|"U/D: C/S<br/>Kafka PackageManifested,<br/>PackageDiverted"| OS
    PP -->|"U/D: CF<br/>Kafka CPTScheduleChanged"| OS
    FL -->|"U/D: CF<br/>Kafka ZoneRegistered,<br/>LocationSlotRegistered"| OS
    DS -->|"U/D: C/S<br/>Kafka DockAppointmentBooked"| OS
    YM -->|"U/D: C/S<br/>Kafka TrailerDocked,<br/>DoorWorkCompleted,<br/>TrailerDeparted"| OS
    DK -->|"REST scans and decisions"| OS
    SC -->|"REST missed-load-sweeps"| OS
    AG -.->|"MCP read-only"| OS
    OS -->|"OHS+PL<br/>ShipmentConfirmed,<br/>PackageMissedLoad"| OM
    OS -->|"OHS+PL<br/>PackageMissedLoad"| EM
    OS -.->|"intended, not built<br/>ShipmentConfirmed"| NF
    OS -.->|"intended, not built<br/>LoadingStarted, LoadClosed,<br/>LoadReopened"| YM2
```

Source: `internal/adapters/inbound/kafka/fulfillment_consumer.go`, `internal/adapters/inbound/kafka/cpt_layout_consumers.go`, `internal/adapters/inbound/kafka/dock_yard_consumers.go`, `internal/adapters/outbound/events/sink.go`, `apis/asyncapi.yaml`, `docs/adr/0001-outbound-shipping-bounded-context.md`, `docs/adr/0005-cloudevents-envelope-and-type-catalogue.md`.

Legend: arrows point from upstream to downstream. Solid = events or commands that exist on both sides; dotted = a synchronous read, or an edge that [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue) intends but whose consumer does not exist yet on the consumer's `develop` (checked 2026-11-11). "Exists on both sides" is about code and configuration, not about the cluster: outbound-shipping is released and registered in warehouse-infra but **not applied**, so **no edge here is live** today (see [Integration](/docs/ecosystem/integration#edge-status-in-the-cluster)). `yard-management` appears twice because it is on both sides of the intended relationship. People and callers are rounded boxes.

Omitted: Kafka and Postgres as infrastructure. **Separate Ways** (no edge at all): the retail network; no `retailnetwork.*` topic is consumed or produced, and network-fulfillment stays the only channel to it. No sibling is called synchronously: sibling data reaches this service only as event-fed local copies.

## Relationships

| Neighbour | Direction | Pattern | Channel | Notes |
| --- | --- | --- | --- | --- |
| fulfillment-execution | upstream | Customer/Supplier | `warehouse.fulfillment.events` | The producer's correlation fields (`site_id`, `source_order_id`, `cpt`, `weight_g`) are additive and optional ([ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)); the consumer never parses `order_ref`. |
| process-path-management | upstream | Conformist | `warehouse.process-path-management.events` | Takes `CPTScheduleChanged` as published; no translation beyond the cutoff resolution. |
| facility-layout | upstream | Conformist | `warehouse.facility.events` | Takes `ZoneRegistered` and `LocationSlotRegistered`; a location's site is its zone's site. |
| dock-scheduling | upstream | Customer/Supplier | `warehouse.dock-scheduling.events` | Outbound `DockAppointmentBooked` only; `cpt_id` and `carrier_scac` nullable. Pinned and tested ([ADR 0007](/docs/adr/0007-dock-scheduling-contract-pinned)). |
| yard-management | upstream | Customer/Supplier | `warehouse.yard-management.events` | The only recorder of physical facts. Pinned and tested ([ADR 0006](/docs/adr/0006-yard-contract-pinned)). |
| order-management | downstream | OHS + PL | `warehouse.outbound-shipping.events` | Implemented in code (shipped lines from `ShipmentConfirmed`, re-promise from `PackageMissedLoad`); off by default and not switched on in warehouse-infra. |
| exception-management | downstream | OHS + PL | `warehouse.outbound-shipping.events` | Implemented: one case per `PackageMissedLoad`; its consumer group is set in warehouse-infra. |
| network-fulfillment | downstream | OHS + PL (intended) | `warehouse.outbound-shipping.events` | Absent on its `develop`. |
| yard-management (reverse edge) | downstream | C/S (intended) | `warehouse.outbound-shipping.events` | Absent: no handler on its `develop`. |
| dock operators, schedulers | clients | none | REST | Unauthenticated. |
| MCP agents | clients | none | MCP over Streamable HTTP | Read-only, unauthenticated. |
