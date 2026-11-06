---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/events/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

This service BOTH publishes and consumes. It publishes CloudEvents 1.0 to
`warehouse.outbound-shipping.events` (integration; consumed by order-management,
network-fulfillment, exception-management and yard-management) and mirrors its
own facts to `warehouse.outbound-shipping.analytics` (analytics-only rows below). It
consumes the integration topics of fulfillment-execution,
process-path-management, facility-layout, dock-scheduling and yard-management
listed below.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes (integration
`warehouse.outbound-shipping.events` AND analytics `warehouse.outbound-shipping.analytics`) is a
CloudEvents 1.0 event in structured content mode. This is a hard fleet rule,
not a preference — there is nothing to "choose" here:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write,
  no dual-read, no envelope toggle env var (`EVENT_ENVELOPE_MODE` is gone
  fleet-wide). `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build/validate/(un)marshal with `github.com/cloudevents/sdk-go/v2/event`
  (latest v2) via ONE helper package,
  `internal/adapters/kafka/cloudevents/` — generated from the template, only
  the per-repo constants differ. Transport stays `segmentio/kafka-go` (no
  sdk-go protocol/client packages, no hand-rolled CloudEvent structs).
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`
  (`cloudevents.ContentTypeHeader()`), next to the W3C trace headers
  (`traceparent`/`tracestate` stay in headers, never duplicated into
  extension attributes). Message key = aggregate id, `kafkago.Hash{}`
  balancer.
- Required attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per
  domain event and persisted with the outbox row, so redelivery carries
  the same id); `source=/warehouse/outbound-shipping`; `type`; `subject` (aggregate
  instance id, never empty); `time` (domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:warehouse:outbound-shipping:<events|analytics>:<EventName>:v<N>`.
  No custom extension attributes without an ADR.
- `type` = `com.warehouse.wms.outbound-shipping.<entity>.<EventName>` where `<entity>` is
  the raising aggregate in lowercase with NO separators: `outboundload`,
  `shippingpackage`, `billoflading`. The SAME `type` names the occurrence on
  both the integration and the analytics topic; `dataschema` names the payload
  shape. Breaking payload change => new `.v2` type + new dataschema version,
  never mutate an existing one.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the
  FULL `type` string (never a short name or suffix match), ignore unknown
  types, read `time`/`subject` from attributes and the payload via `DataAs`,
  dedupe on `id`, and DLQ/skip — WARN log + commit past, never crash, never
  block the partition, never fall back to parsing a legacy shape — anything
  that fails CloudEvents validation.
- Tests: a golden exact-JSON test per published `type` (all attributes + the
  `content-type` header); a legacy-flat-message-rejected test per consumer;
  Kafka integration tests via testcontainers only.

Full standard, subdomain table and the fleet's cross-service type catalogue:
warehouse-docs `docs/strategic-design/event-standard-cloudevents.md`.
Recorded in this repo as `docs/adr/0006-cloudevents-envelope-and-type-catalogue.md`.

### Published types

| `type` | topic(s) | `subject` | `dataschema` |
| --- | --- | --- | --- |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadPlanned` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:LoadPlanned:v1` |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadDoorAssigned` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:LoadDoorAssigned:v1` |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadingStarted` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:LoadingStarted:v1` |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadClosed` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:LoadClosed:v1` |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadReopened` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:LoadReopened:v1` |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadCancelled` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:LoadCancelled:v1` |
| `com.warehouse.wms.outbound-shipping.outboundload.ShipmentConfirmed` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:ShipmentConfirmed:v1` |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageStaged` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:PackageStaged:v1` |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageLoaded` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:PackageLoaded:v1` |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageUnloaded` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:PackageUnloaded:v1` |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageMissedLoad` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:PackageMissedLoad:v1` |
| `com.warehouse.wms.outbound-shipping.billoflading.BillOfLadingIssued` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:BillOfLadingIssued:v1` |
| `com.warehouse.wms.outbound-shipping.billoflading.BillOfLadingVoided` | `warehouse.outbound-shipping.events` | aggregate id | `urn:warehouse:outbound-shipping:events:BillOfLadingVoided:v1` |

Analytics-only rows (`LoadPlanned`, `LoadDoorAssigned`, `LoadCancelled`,
`PackageStaged`, `PackageLoaded`, `PackageUnloaded`, `BillOfLadingIssued`,
`BillOfLadingVoided`) also mirror to `warehouse.outbound-shipping.analytics` with
`dataschema=urn:warehouse:outbound-shipping:analytics:<EventName>:v1`.

### Consumed types

| `type` | topic | key data |
| --- | --- | --- |
| `com.warehouse.wes.fulfillment-execution.package.PackageManifested` | `warehouse.fulfillment.events` | package_id, order_ref (+ site_id, source_order_id, cpt, weight_g additive, OS-0003) |
| `com.warehouse.wes.fulfillment-execution.package.PackageDiverted` | `warehouse.fulfillment.events` | package_id |
| `com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged` | `warehouse.process-path-management.events` | site_id, timezone, cutoffs |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | zone -> site mapping |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | staging locations (role, zoneId) |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `warehouse.dock-scheduling.events` | direction OUTBOUND only; cpt_id, carrier_scac nullable (re-pin from develop before wave 5) |
| `com.warehouse.wms.yard-management.trailer.TrailerDocked` | `warehouse.yard-management.events` | fixture-validated against the proposed yard model until its contract merges |
| `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | `warehouse.yard-management.events` | same |
| `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | `warehouse.yard-management.events` | trailer_id, site_id, departed_at, load_state, seal_number; same |

Dock-scheduling and yard-management rows are pinned from the branch/proposed
contracts (campaign spike Q5 and the yard model); re-pin both from the
producers' `origin/develop` AsyncAPI before fleet wiring lands, per the
orchestrator's sequencing note.
