---
id: bounded-context-canvas
title: Bounded context canvas
sidebar_label: Bounded context canvas
description: "The ddd-crew Bounded Context Canvas of outbound-shipping: purpose, classification, roles, ubiquitous language, business decisions, inbound and outbound communication, assumptions and open questions."
---

# Bounded context canvas

Following the ddd-crew [Bounded Context Canvas](https://github.com/ddd-crew/bounded-context-canvas), filled from the code and the ADRs.

Source: `docs/adr/0001` to `0006`, `internal/domain/*`, `internal/application/usecases/*`, `apis/openapi.yaml`, `apis/asyncapi.yaml`.

## Name

`outbound-shipping`: the WMS context that gets manifested packages onto the right load, proves what left on which trailer and tells the fleet an order shipped.

## Purpose

Own the last mile inside the building: staging, the outbound load, the physical ship-confirm and the bill of lading. Before it existed nothing owned "what left on which truck" and an order's last state was Released.

## Strategic classification

| Aspect | Value |
| --- | --- |
| Domain | Warehouse management (WMS), outbound |
| Subdomain | **Supporting** ([reasoning](/docs/ddd/subdomain-classification)) |
| Business model | Cost reducer and revenue protector: fewer missed cutoffs, an accurate "shipped" fact |
| Evolution | Custom-built (the bill of lading leans Product) ([chart](/docs/ddd/core-domain-chart)) |

## Domain roles

| Role | Where |
| --- | --- |
| **Execution context** | scans move packages through staged, loaded and shipped |
| **Specification-lite** | the load plan is derived from the cutoff schedule, not authored here |
| **Audit** | the bill of lading and the once-only missed-load fact |
| **Gateway for downstream** | `ShipmentConfirmed` and `PackageMissedLoad` on `warehouse.outbound-shipping.events` are a published language for order-management, exception-management and (intended) network-fulfillment |
| **Analysis (small)** | the load board and the adherence report |

## Inbound communication

| Message | From | Type | Collaborator relationship | Policy / effect |
| --- | --- | --- | --- | --- |
| `PackageManifested` | fulfillment-execution | event | Customer/Supplier | plan or join the open load, expect the package |
| `PackageDiverted` | fulfillment-execution | event | Customer/Supplier | short-ship the package with reason `DIVERTED` |
| `CPTScheduleChanged` | process-path-management | event | Conformist | replace the site's cutoff schedule copy |
| `ZoneRegistered`, `LocationSlotRegistered` | facility-layout | event | Conformist | fill the staging-location copy |
| `DockAppointmentBooked` | dock-scheduling | event | Customer/Supplier | assign the door of an outbound appointment naming the cutoff; keep carrier and SCAC |
| `TrailerDocked` | yard-management | event | Customer/Supplier | attach the trailer and start loading |
| `DoorWorkCompleted` | yard-management | event | Customer/Supplier | record the completion (no state change in v1) |
| `TrailerDeparted` | yard-management | event | Customer/Supplier | ship-confirm a `CLOSED` load, or flag a departure while loading |
| Stage, load, unload scans | dock operators | REST command | | package moves |
| Close, reopen, cancel, void | dock supervisors | REST command | | load and document decisions |
| Missed-load sweep | a scheduler | REST command | | flag missed packages |
| Reads and adherence report | any client | REST query | | |
| `loads_at_risk`, `missed_loads` | agents | MCP query (read-only) | | |

## Outbound communication

| Message | To | Type | Relationship |
| --- | --- | --- | --- |
| `ShipmentConfirmed` | order-management (implemented), network-fulfillment (intended, not implemented) | event | Open Host Service and Published Language |
| `PackageMissedLoad` | order-management, exception-management (both implemented) | event | OHS and PL |
| `LoadingStarted`, `LoadClosed`, `LoadReopened` | yard-management (intended, not implemented) | event | OHS and PL |
| `LoadPlanned`, `LoadDoorAssigned`, `LoadCancelled`, `PackageStaged`, `PackageLoaded`, `PackageUnloaded`, `BillOfLadingIssued`, `BillOfLadingVoided` | no integration consumer; own projector via the analytics twin | event | |

## Ubiquitous language

See [Ubiquitous language](/docs/ddd/ubiquitous-language). Key terms: outbound load, cutoff key, shipping package, staging, short-ship, ship-confirm, bill of lading, missed load.

## Business decisions

- One open load per `(site, cpt, operating date, ship method)`, planned from the first manifested package.
- Loading starts only from a physical docking at the load's door; there is no REST route to start it.
- A load closes only when every package is loaded or short-shipped with a reason.
- Ship-confirm follows the trailer's departure and needs a seal; a departure while the load is `LOADING` never confirms ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)).
- A bill of lading is issued once per confirmed load and only with the yard's seal; voiding it does not undo the shipment.
- `PackageMissedLoad` is raised at most once per package.
- A manifest without a site or cutoff correlation plans nothing ([ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)).

## Assumptions

- fulfillment-execution will populate `site_id` on `PackageManifested`; until it does, no load is planned in a real flow (its AsyncAPI says the field is omitted for now).
- yard-management records and publishes a seal on `TrailerDeparted`.
- A dock appointment carries the carrier; the bill of lading refuses an empty one.
- The platform runs one Kafka broker; topics are auto-created.
- Access control is the network boundary: REST and MCP are unauthenticated.

## Verification metrics

Load adherence (`loads_closed / loads_total` per cutoff and date), packages missed, loads at risk, the age of the oldest unpublished outbox row (see [Observability](/docs/operations/observability)).

## Open questions

- Who calls `POST /missed-load-sweeps` and when: nothing in the repository schedules it.
- Who consumes `LoadingStarted`, `LoadClosed` and `LoadReopened`: yard-management declares no handler.
- When network-fulfillment replaces its manual shipment confirmation with `ShipmentConfirmed` ([ADR 0004](/docs/adr/0004-shipment-confirmed-replaces-the-manual-network-confirmation)).
- Whether the process-path-management, facility-layout and fulfillment-execution payloads should be pinned like the yard and dock-scheduling ones ([ADR 0007](/docs/adr/0007-dock-scheduling-contract-pinned) lists the first and last as follow-ups). The dock-scheduling pin itself is done and was refreshed in PR 18.
