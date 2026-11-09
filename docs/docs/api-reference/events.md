---
id: events
title: Events
sidebar_label: Events
description: "The outbound-shipping event catalogue: the 13 published CloudEvents types, the 9 consumed types, topics, keys and payloads."
---

# Events

Source: `apis/asyncapi.yaml` (AsyncAPI 2.6), [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue), `internal/adapters/outbound/events/*.go`, `internal/adapters/kafka/cloudevents/cloudevents.go`, `internal/adapters/inbound/kafka/*.go`. `TestEventCatalogueMatchesContract` fails the build when the contract declares a type that ADR 0005 does not list.

## Envelope

Every message is a CloudEvents 1.0 event in structured content mode, built only through `internal/adapters/kafka/cloudevents`.

| Attribute | Value |
|---|---|
| `specversion` | `1.0` |
| `id` | UUID minted once per occurrence, stored with the outbox row, stable across relay retries; consumers dedupe on it |
| `source` | `/warehouse/outbound-shipping` |
| `type` | `com.warehouse.wms.outbound-shipping.<entity>.<EventName>`, entity one of `outboundload`, `shippingpackage`, `billoflading` |
| `subject` | the aggregate id: `load_id`, `package_id` or `bol_number` (also the Kafka message key, hash-partitioned) |
| `time` | occurred-at instant, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:warehouse:outbound-shipping:<events\|analytics>:<EventName>:v1` |
| Kafka header | `content-type: application/cloudevents+json; charset=UTF-8` |

`data` is snake_case JSON; optional fields are omitted when unset, timestamps are RFC 3339, and every payload carries the aggregate `version` **after** the change, so a consumer can reject out-of-order deliveries.

## Topics

| Topic | Direction | Content |
|---|---|---|
| `warehouse.outbound-shipping.events` | published | the integration stream, 13 types |
| `warehouse.outbound-shipping.analytics` | published, and consumed by this service | the same 13 types, **same `id`, `type`, `subject`, key and payload**; only the `dataschema` stream segment (`analytics`) differs. Not an integration contract: only the service's own projector reads it. |
| `warehouse.fulfillment.events` | consumed | `PackageManifested`, `PackageDiverted` |
| `warehouse.process-path-management.events` | consumed | `CPTScheduleChanged` |
| `warehouse.facility.events` | consumed | `ZoneRegistered`, `LocationSlotRegistered` |
| `warehouse.dock-scheduling.events` | consumed | `DockAppointmentBooked` |
| `warehouse.yard-management.events` | consumed | `TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted` |

Both published streams are written by the same outbox in the same transaction, two rows per event.

## Published events

| Type (after the `com.warehouse.wms.outbound-shipping.` prefix) | Raised when | `data` | Integration consumers |
|---|---|---|---|
| `outboundload.LoadPlanned` | the first manifested package of a cutoff key plans a load | `load_id, site_id, cpt_id, operating_date, ship_method, cutoff, version` | analytics only |
| `outboundload.LoadDoorAssigned` | an outbound dock appointment names the load's cpt | `load_id, site_id, door_code, appointment_id, version` | analytics only |
| `outboundload.LoadingStarted` | a trailer docks at the load's door | `load_id, site_id, door_code, trailer_id, started_at, version` | yard-management (intended, not implemented) |
| `outboundload.LoadClosed` | every package is loaded or short-shipped | `load_id, site_id, door_code, trailer_id, package_count, short_shipped_count, closed_at, version` | yard-management (intended, not implemented) |
| `outboundload.LoadReopened` | a supervisor reopens a closed load | `load_id, site_id, reopened_at, version` | yard-management (intended, not implemented) |
| `outboundload.LoadCancelled` | a planned load is cancelled | `load_id, site_id, cancelled_at, version` | analytics only |
| `outboundload.ShipmentConfirmed` | `TrailerDeparted` arrives for a `CLOSED` load | `load_id, site_id, cpt_id, carrier, trailer_id, seal_number, departed_at, orders:[{order_id}], version` | order-management; network-fulfillment (intended, not implemented) |
| `shippingpackage.PackageStaged` | a package is staged | `package_id, load_id, site_id, staging_location, staged_at, version` | analytics only |
| `shippingpackage.PackageLoaded` | a package is loaded through its load's door | `package_id, load_id, site_id, loaded_at, version` | analytics only |
| `shippingpackage.PackageUnloaded` | a loaded package is taken back off | `package_id, load_id, site_id, unloaded_at, reason (optional), version` | analytics only |
| `shippingpackage.PackageMissedLoad` | a package missed its load: cutoff sweep, or a departure while the load was `LOADING` | `package_id, load_id, site_id, source_order_id (optional), cutoff, detected_at, version` | order-management, exception-management |
| `billoflading.BillOfLadingIssued` | the bill of lading is issued with the ship confirmation | `bol_number, load_id, site_id, carrier, carrier_scac (optional), trailer_id, seal_number, package_count, total_weight_g, issued_at, version` | analytics only |
| `billoflading.BillOfLadingVoided` | an issued bill of lading is voided | `bol_number, load_id, site_id, reason, voided_at, version` | analytics only |

Rules:

- `ShipmentConfirmed.orders` lists every distinct `source_order_id` of the shipped packages, sorted; a package manifested without one is left out ([ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)). `site_id` of the event is the load's site.
- `PackageMissedLoad` is raised at most once per package: the sweep and the departure-while-loading flag share a `missed_flagged` guard, so a re-run publishes nothing.
- The `consumers` column is the intent of ADR 0005; the real state of each edge is on [Integration](/docs/ecosystem/integration). The asyncapi marks the three yard-bound types `pending` for that reason, and the other eight as analytics only by design.
- `LoadClosed.package_count` counts loaded packages; `short_shipped_count` counts the short-ships recorded at close.
- Events raised by use cases but **not published**: package assignment, `MarkShipped` and `MarkShortShipped` changes raise no event ("the model publishes none"); `LoadClosed` carries the short-ship count.

## Consumed events

All dispatch on the full `type`. The fields below are exactly the ones the code reads; other fields are ignored.

| Type | Topic | Fields read |
|---|---|---|
| `com.warehouse.wes.fulfillment-execution.package.PackageManifested` | `warehouse.fulfillment.events` | `package_id`, `order_ref` (stored verbatim, never parsed), `site_id`, `source_order_id`, `cpt` (RFC 3339 cutoff instant), `weight_g`. The last four are additive and optional. |
| `com.warehouse.wes.fulfillment-execution.package.PackageDiverted` | `warehouse.fulfillment.events` | `package_id` |
| `com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged` | `warehouse.process-path-management.events` | `site_id`, `timezone`, `version`, `cutoffs[]` of `cpt_id, local_time, days_of_week, ship_method` |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | `zoneId`, `siteCode`, `zoneCode` (camelCase, as the producer sends them) |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | `locationCode`, `zoneId`, `role` (absent on pre-role events: Storage) |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `warehouse.dock-scheduling.events` | `appointment_id, site_id, direction, door_code, carrier, carrier_scac, cpt_id, window_start, window_end, version`; `cpt_id` and `carrier_scac` nullable |
| `com.warehouse.wms.yard-management.trailer.TrailerDocked` | `warehouse.yard-management.events` | `trailer_id, site_id, door_code, docked_at` |
| `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | `warehouse.yard-management.events` | `trailer_id, site_id, door_code, appointment_id, completed_at` |
| `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | `warehouse.yard-management.events` | `trailer_id, site_id, departed_at, seal_number` (optional; no seal means no confirmation) |

The own analytics consumer reads the 13 published types from `warehouse.outbound-shipping.analytics`. It acts on `LoadPlanned`, `PackageStaged`, `PackageLoaded`, `PackageMissedLoad`, `LoadClosed`, `ShipmentConfirmed` and `LoadCancelled`; the rest are claimed and ignored.

Handling guarantees (retry, dead letters, inbox) are on the [runbook](/docs/operations/runbook); the domain view of each event is on [Domain events](/docs/ddd/domain-events).
