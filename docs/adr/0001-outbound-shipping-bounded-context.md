# 1. outbound-shipping is a bounded context of the warehouse fleet

Status: Accepted (bootstrap, 2026-11-05). Finalizes design draft OS-0001
(decisions D1 and D2 are FINAL, campaign decision log).

## Context

`fulfillment-execution` stops at `PackageManifested`. `yard-management` owns the
trailer, the door work, the seal and the departure (YD-0004: the yard is the
only recorder of physical facts). Nothing owns staging, what is on a load, the
bill of lading, or the fact that goods left; an order's last state is Released.

## Decision

Create `outbound-shipping`, Supporting, subdomain `wms`, owning the
`OutboundLoad`, `ShippingPackage` and `BillOfLading` aggregates.

Context map (every edge is event-fed; no synchronous sibling call):

| Neighbour | Relationship | Edge |
| --- | --- | --- |
| fulfillment-execution | Customer/Supplier | consumes `PackageManifested`, `PackageDiverted` (`warehouse.fulfillment.events`); local copies only |
| process-path-management | Conformist | consumes `CPTScheduleChanged` (the cutoff schedule copy) |
| facility-layout | Conformist | consumes `ZoneRegistered`, `LocationSlotRegistered` (staging locations, zone to site) |
| dock-scheduling | Customer/Supplier | consumes `DockAppointmentBooked`, outbound direction only, `cpt_id` nullable |
| yard-management | Customer/Supplier | consumes `TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` (seal, departure); yard consumes `LoadingStarted`, `LoadClosed`, `LoadReopened` (deferred until the yard contract merges) |
| order-management | Customer/Supplier | consumes `ShipmentConfirmed`, `PackageMissedLoad` (re-promise) |
| network-fulfillment | Customer/Supplier | consumes `ShipmentConfirmed`, joins `localOrderId`; replaces the manual POST (OS-0004) |
| exception-management | Customer/Supplier | consumes `PackageMissedLoad`, one case each |

Explicitly excluded and why: transportation management (rating, tendering — a
TMS concern, the network and carriers own it); pallets, load sequencing and
master BOL (deferred, D4); sortation equipment and automated loading
(automation out of scope, user decision 2026-11-05); anything before
`PackageManifested` (fulfillment-execution) and the trailer/door/seal facts
themselves (yard-management).

No live cross-context REST or MCP lookup: sibling data reaches this service
only as event-fed local copies. No `retailnetwork.*` topic is consumed or
produced; network-fulfillment stays the only channel to the network.

## Consequences

- The fleet gets one owner for "what left on which truck".
- Another service to run; it depends on yard-management for docking and
  departure facts (consumed per the yard contract, pinned in ADR 0006).
- `fulfillment-execution` stays focused on task execution.
