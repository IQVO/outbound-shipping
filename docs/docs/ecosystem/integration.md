---
id: integration
title: Integration
sidebar_label: Integration
description: "Every upstream and downstream of outbound-shipping: protocol, contract, and what happens when an edge is absent or fails."
---

# Integration

outbound-shipping integrates **only through Kafka events and local copies**. Every edge is event-fed; the service makes no synchronous REST or MCP call to a sibling and no `retailnetwork.*` topic is consumed or produced ([ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context)). The only synchronous surfaces are its own REST and MCP, which are unauthenticated.

Source: `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/events/*.go`, `apis/asyncapi.yaml`, `contracts/`; the other repositories' `origin/develop` (checked 2026-11-09). The map is drawn on [Context map](/docs/ddd/context-map); every event is on [Events](/docs/api-reference/events).

## Upstream: what this service consumes

All consumers decode CloudEvents 1.0 only, dispatch on the **full** `type`, ignore every other type on the topic, and dedupe on the CloudEvents `id` in `processed_events`. Failure behaviour is common: a transient failure is retried (5 attempts, 200 ms doubling to 5 s) then dead-lettered to `<topic>.dlq`; a payload that can never be applied is dead-lettered at once; a message that is not a CloudEvent is skipped. See the [runbook](/docs/operations/runbook).

| Producer | Topic | Types consumed | What this service does with them | Contract status |
|---|---|---|---|---|
| fulfillment-execution | `warehouse.fulfillment.events` | `...wes.fulfillment-execution.package.PackageManifested`, `...PackageDiverted` | A manifest expects the package and plans or joins the load; a diversion short-ships it with reason `DIVERTED`. | Mirrored by hand from the producer's AsyncAPI. The producer's `site_id` and `source_order_id` (and `cpt`, `weight_g`) are **optional and omitted when unknown** ([ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)). Its AsyncAPI on `develop` says `site_id` has no source in the order-work chain yet, so it is omitted until a later wave: **a manifest without `site_id` is ignored and no load is planned**. |
| process-path-management | `warehouse.process-path-management.events` | `...wes.process-path-management.cptschedule.CPTScheduleChanged` | Replaces the site's cutoff schedule copy. | Mirrored by hand. |
| facility-layout | `warehouse.facility.events` | `...wms.facility-layout.zone.ZoneRegistered`, `...locationslot.LocationSlotRegistered` | Fills the staging-location copy (zone to site, location role). | Mirrored by hand. |
| dock-scheduling | `warehouse.dock-scheduling.events` | `...wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `OUTBOUND` appointments naming a `cpt_id` assign the load's door; carrier and SCAC are kept for the bill of lading. | Pinned **by hand** from the producer's feature branch ([ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue)); the producer's `develop` AsyncAPI now declares the type, and the pin has not been refreshed. No automated check. |
| yard-management | `warehouse.yard-management.events` | `...wms.yard-management.trailer.TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` | Docking attaches the trailer and starts loading; the departure confirms the shipment ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)). | **Pinned and tested**: `contracts/yard-management.asyncapi.yaml` is a copy of the producer's `develop`, gated in CI by `pinned-contracts` and proven by `TestConsumedYardTypesMatchTheProducersIntegrationContract` ([ADR 0006](/docs/adr/0006-yard-contract-pinned)). |
| outbound-shipping (itself) | `warehouse.outbound-shipping.analytics` | its own 13 types | Folded into the `load_board` projection. | Own contract. |

The payload fields each consumer reads are fixed in code (`dock_yard_consumers.go`, `fulfillment_consumer.go`, `cpt_layout_consumers.go`); extra fields the producers send are ignored. `seal_number` on `TrailerDeparted` is optional on the wire and mandatory for a confirmation: a departure without a seal confirms nothing.

### Failure behaviour that matters

- **A producer that is down or silent:** nothing is planned or confirmed; there is no polling fallback and no REST call to ask. The local copies keep their last value.
- **Ordering between topics is not guaranteed.** The design copes in these cases: schedule and layout copies may arrive in any order; a `DockAppointmentBooked` that arrives before any load of its cpt exists is saved but does **not** assign a door when the load is planned later (planned, not implemented: it only assigns when a later appointment event for that cpt arrives while a load is open, see [Troubleshooting](/docs/operations/troubleshooting#appointment-before-the-load-planned)); a `TrailerDeparted` that arrives before the load closes never confirms (it flags what is not loaded) and the redelivery after close does not confirm either.
- **A consumed topic that does not exist yet** is retried by the reader and logged; the service stays ready.

## Downstream: who consumes what this service publishes

The service publishes 13 types on `warehouse.outbound-shipping.events` (and the same 13 on the analytics twin). The intended integration consumers, per [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue) and `apis/asyncapi.yaml`:

| Consumer | Types | State on the consumer's `origin/develop` (checked 2026-11-09) |
|---|---|---|
| order-management | `ShipmentConfirmed`, `PackageMissedLoad` | **Implemented**: `internal/adapters/inbound/kafka/outbound_shipping_consumer.go` turns `ShipmentConfirmed` into shipped order lines and `PackageMissedLoad` into an order-wide re-promise; own group variable `OUTBOUND_SHIPPING_CONSUMER_GROUP` and an on/off mode variable. |
| exception-management | `PackageMissedLoad` | **Implemented**: `internal/adapters/inbound/kafka/outbound_shipping_consumer.go` opens or touches one case per missed package; it ignores every other type. |
| network-fulfillment | `ShipmentConfirmed` | **Absent**: no code on its `develop` reads `warehouse.outbound-shipping.events` or the `ShipmentConfirmed` type (ADR 0004 intends this edge to replace the manual shipment confirmation). Its `NetworkOrderShipmentConfirmed` is its own event, raised through its own REST confirmation. |
| yard-management | `LoadingStarted`, `LoadClosed`, `LoadReopened` | **Absent**: ADR 0005 names yard-management as the consumer, but yard-management declares no such consumer and has no handler for these types on `develop` (the AsyncAPI marks them `pending`). |
| (none) | `LoadPlanned`, `LoadDoorAssigned`, `LoadCancelled`, `PackageStaged`, `PackageLoaded`, `PackageUnloaded`, `BillOfLadingIssued`, `BillOfLadingVoided` | Analytics only by design; the service's own projector reads them from the analytics stream. |

Publishing guarantees: events go through the transactional outbox (same transaction as the aggregate), at-least-once, `RequireAll` acks, the aggregate id as the Kafka key so one load, package or bill of lading stays ordered on a partition, CloudEvents `id` stable across retries. Every payload carries the aggregate `version` after the change.

## Not integrated

- **No A2A, no cross-context MCP or REST client.** The `cmd/mcp` server is for external agents (such as warehouse-ops-agent) to read; it does not call anyone.
- **No retail-network edge.** The retail network is reached only through network-fulfillment.
- **No transportation system.** Carrier, SCAC and seal come from the dock appointment and the yard; nothing is rated or tendered here.
- **No warehouse-infra registration yet**, so no edge route exposes REST or MCP ([runbook](/docs/operations/runbook)).
