---
id: integration
title: Integration
sidebar_label: Integration
description: "Every upstream and downstream of outbound-shipping: protocol, contract, and what happens when an edge is absent or fails."
---

# Integration

outbound-shipping integrates **only through Kafka events and local copies**. Every edge is event-fed; the service makes no synchronous REST or MCP call to a sibling and no `retailnetwork.*` topic is consumed or produced ([ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context)). The only synchronous surfaces are its own REST and MCP, which are unauthenticated.

Source: `internal/adapters/inbound/kafka/*.go`, `internal/adapters/outbound/events/*.go`, `apis/asyncapi.yaml`, `contracts/`; the other repositories' `origin/develop` and warehouse-infra's `origin/develop` (checked 2026-11-11). The [edge status table](#edge-status-in-the-cluster) says which edges are wired in the cluster configuration and which are live. The map is drawn on [Context map](/docs/ddd/context-map); every event is on [Events](/docs/api-reference/events).

## Upstream: what this service consumes

All consumers decode CloudEvents 1.0 only, dispatch on the **full** `type`, ignore every other type on the topic, and dedupe on the CloudEvents `id` in `processed_events`. Failure behaviour is common: a transient failure is retried (5 attempts, 200 ms doubling to 5 s) then dead-lettered to `<topic>.dlq`; a payload that can never be applied is dead-lettered at once; a message that is not a CloudEvent is skipped. See the [runbook](/docs/operations/runbook).

| Producer | Topic | Types consumed | What this service does with them | Contract status |
|---|---|---|---|---|
| fulfillment-execution | `warehouse.fulfillment.events` | `...wes.fulfillment-execution.package.PackageManifested`, `...PackageDiverted` | A manifest expects the package and plans or joins the load; a diversion short-ships it with reason `DIVERTED`. | Mirrored by hand from the producer's AsyncAPI. The producer's `site_id` and `source_order_id` (and `cpt`, `weight_g`) are **optional and omitted when unknown** ([ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)). Its AsyncAPI on `develop` says `site_id` has no source in the order-work chain yet, so it is omitted until a later wave: **a manifest without `site_id` is ignored and no load is planned**. |
| process-path-management | `warehouse.process-path-management.events` | `...wes.process-path-management.cptschedule.CPTScheduleChanged` | Replaces the site's cutoff schedule copy. | Mirrored by hand. |
| facility-layout | `warehouse.facility.events` | `...wms.facility-layout.zone.ZoneRegistered`, `...locationslot.LocationSlotRegistered` | Fills the staging-location copy (zone to site, location role). | Mirrored by hand. |
| dock-scheduling | `warehouse.dock-scheduling.events` | `...wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `OUTBOUND` appointments naming a `cpt_id` assign the load's door; carrier and SCAC are kept for the bill of lading. **Pinned and tested**: `contracts/dock-scheduling.asyncapi.yaml` is a copy of the producer's `develop`, gated by `pinned-contracts` and proven by `TestDockAppointmentBookedMatchesTheProducersIntegrationContract` ([ADR 0007](/docs/adr/0007-dock-scheduling-contract-pinned)); refreshed in PR 18. |
| yard-management | `warehouse.yard-management.events` | `...wms.yard-management.trailer.TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` | Docking attaches the trailer and starts loading; the departure confirms the shipment ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)). | **Pinned and tested**: `contracts/yard-management.asyncapi.yaml` is a copy of the producer's `develop`, gated in CI by `pinned-contracts` and proven by `TestConsumedYardTypesMatchTheProducersIntegrationContract` ([ADR 0006](/docs/adr/0006-yard-contract-pinned)). |
| outbound-shipping (itself) | `warehouse.outbound-shipping.analytics` | its own 13 types | Folded into the `load_board` projection. | Own contract. |

The payload fields each consumer reads are fixed in code (`dock_yard_consumers.go`, `fulfillment_consumer.go`, `cpt_layout_consumers.go`); extra fields the producers send are ignored. `seal_number` on `TrailerDeparted` is optional on the wire and mandatory for a confirmation: a departure without a seal confirms nothing.

### Failure behaviour that matters

- **A producer that is down or silent:** nothing is planned or confirmed; there is no polling fallback and no REST call to ask. The local copies keep their last value.
- **Ordering between topics is not guaranteed.** The design copes in these cases: schedule and layout copies may arrive in any order; a `DockAppointmentBooked` that arrives before any load of its cpt exists is saved but does **not** assign a door when the load is planned later (planned, not implemented: it only assigns when a later appointment event for that cpt arrives while a load is open, see [Troubleshooting](/docs/operations/troubleshooting#appointment-before-the-load-planned)); a `TrailerDeparted` that arrives before the load closes never confirms (it flags what is not loaded) and the redelivery after close does not confirm either.
- **A consumed topic that does not exist yet** is retried by the reader and logged; the service stays ready. warehouse-infra nevertheless asks for all seven topics to be pre-created before the first sync, because a kafka-go group consumer that starts before its topic exists is never handed the partition (its note `docs/kafka-topics-outbound-shipping.md`; see the [runbook](/docs/operations/runbook#what-an-apply-needs)). Whether that happens here was not observed: the service has not run in the cluster.

## Downstream: who consumes what this service publishes

The service publishes 13 types on `warehouse.outbound-shipping.events` (and the same 13 on the analytics twin). The intended integration consumers, per [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue) and `apis/asyncapi.yaml`:

| Consumer | Types | State on the consumer's `origin/develop` (checked 2026-11-11) |
|---|---|---|
| order-management | `ShipmentConfirmed`, `PackageMissedLoad` | **Implemented**: `internal/adapters/inbound/kafka/outbound_shipping_consumer.go` turns `ShipmentConfirmed` into shipped order lines and `PackageMissedLoad` into an order-wide re-promise; own group variable `OUTBOUND_SHIPPING_CONSUMER_GROUP` and an on/off mode variable. |
| exception-management | `PackageMissedLoad` | **Implemented**: `internal/adapters/inbound/kafka/outbound_shipping_consumer.go` opens or touches one case per missed package; it ignores every other type. |
| network-fulfillment | `ShipmentConfirmed` | **Absent**: no code on its `develop` reads `warehouse.outbound-shipping.events` or the `ShipmentConfirmed` type (ADR 0004 intends this edge to replace the manual shipment confirmation). Its `NetworkOrderShipmentConfirmed` is its own event, raised through its own REST confirmation. |
| yard-management | `LoadingStarted`, `LoadClosed`, `LoadReopened` | **Absent**: ADR 0005 names yard-management as the consumer, but yard-management declares no such consumer and has no handler for these types on `develop` (the AsyncAPI marks them `pending`). |
| (none) | `LoadPlanned`, `LoadDoorAssigned`, `LoadCancelled`, `PackageStaged`, `PackageLoaded`, `PackageUnloaded`, `BillOfLadingIssued`, `BillOfLadingVoided` | Analytics only by design; the service's own projector reads them from the analytics stream. |

Publishing guarantees: events go through the transactional outbox (same transaction as the aggregate), at-least-once, `RequireAll` acks, the aggregate id as the Kafka key so one load, package or bill of lading stays ordered on a partition, CloudEvents `id` stable across retries. Every payload carries the aggregate `version` after the change.

## Edge status in the cluster

An edge is **live** only when both ends run in the cluster. outbound-shipping is registered in warehouse-infra but **not applied**: no outbound-shipping pod exists (read-only `kubectl get pods,deploy,application -A` on k3s-multipass, 2026-11-11), so **no edge is live**. The column "wired" says what the warehouse-infra values already configure on the outbound-shipping side or on the neighbour's side. Whether the neighbour's pod would reach its topic was not checked.

| Edge | Wired in warehouse-infra (`origin/develop`) | Neighbour in the cluster | Live |
|---|---|---|---|
| fulfillment-execution to outbound-shipping | Yes, consumer side: group `outbound-shipping-fulfillment` in `helm-values/outbound-shipping.yaml`. | `fulfillment-execution` pod running | Not live |
| dock-scheduling to outbound-shipping | Yes: group `outbound-shipping-dock`. | `dock-scheduling` pod running | Not live |
| yard-management to outbound-shipping | Yes: group `outbound-shipping-yard`. | `yard-management` pod running | Not live |
| process-path-management to outbound-shipping | Yes: group `outbound-shipping-cpt`. | `process-path-management` pod running | Not live |
| facility-layout to outbound-shipping | Yes: group `outbound-shipping-layout`. | `facility-layout` pod running | Not live |
| outbound-shipping to itself (analytics stream, LoadBoardView) | Yes: group `outbound-shipping-analytics`. | the service itself: not running | Not live |
| outbound-shipping to exception-management | Yes, consumer side: group `exception-management-outbound-shipping` in `helm-values/exception-management.yaml`, and the running pod's ConfigMap carries `OUTBOUND_SHIPPING_EXCEPTIONS_CONSUMER_GROUP`. | `exception-management` pod running | Not live (no producer) |
| outbound-shipping to order-management | **No.** The consumer exists in its code but is off by default (`OUTBOUND_SHIPPING_CONSUMER_MODE`, `off` unless set to `kafka`); neither its chart nor `helm-values/order-management.yaml` sets it, and the running pod has no such variable. | `order-management` pod running | Not live, and the consumer end is off |
| outbound-shipping to network-fulfillment | No: no consumer code exists ([ADR 0004](/docs/adr/0004-shipment-confirmed-replaces-the-manual-network-confirmation) intends it). | `network-fulfillment` pod running | Not built |
| outbound-shipping to yard-management (`LoadingStarted`, `LoadClosed`, `LoadReopened`) | No: no handler exists. | `yard-management` pod running | Not built |

The topics for all of these are pre-created by a manual step that has not been run (not checked from the cluster; see the [runbook](/docs/operations/runbook#what-an-apply-needs)).

## Not integrated

- **No A2A, no cross-context MCP or REST client.** The `cmd/mcp` server is for external agents (such as warehouse-ops-agent) to read; it does not call anyone.
- **No retail-network edge.** The retail network is reached only through network-fulfillment.
- **No transportation system.** Carrier, SCAC and seal come from the dock appointment and the yard; nothing is rated or tendered here.
- **No Kong route in the cluster yet.** warehouse-infra's values declare `/api/outbound-shipping` for REST and, with the MCP server switched on, `/api/mcp/outbound-shipping`; the MCP server is off and nothing is applied, so no edge route exposes either ([runbook](/docs/operations/runbook#deployment-status-released-v010-registered-and-pinned-not-applied)).
