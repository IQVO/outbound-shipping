# ADR 0005: CloudEvents envelope and type catalogue

## Status

Accepted (2026-11-06). This ADR must exist before `apis/asyncapi.yaml` is
committed: `TestEventCatalogueMatchesContract` fails any push where the
contract declares a type for this service that this catalogue does not list.

## Context

Every Kafka message in the fleet is a CloudEvents 1.0 event in structured
mode (fleet rule `.claude/rules/fleet/cloudevents.md`, warehouse-docs
`docs/strategic-design/event-standard-cloudevents.md`). This service is WMS
subdomain (`wms`, decision D2): its type prefix is
`com.warehouse.wms.outbound-shipping.<entity>.<EventName>`. This ADR is the
catalogue of every type outbound-shipping publishes or consumes.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID minted once per occurrence and persisted
  with the outbox row (stable across relay retries; consumers dedupe on it);
  `source` `/warehouse/outbound-shipping`; `subject` the aggregate root id
  (`loadId`, `packageId` or `bolNumber`), also the Kafka partition key;
  `time` the occurred-at instant in UTC; `datacontenttype`
  `application/json`; `dataschema`
  `urn:warehouse:outbound-shipping:events:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key =
  the aggregate id (`kafkago.Hash{}`), so all events of one aggregate stay
  ordered on one partition.
- `data` is snake_case; optional fields are omitted when unset; every event
  payload carries the aggregate `version` AFTER the change (the model's
  version convention), so consumers can reject out-of-order deliveries.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.

### Published on `warehouse.outbound-shipping.events` (+ `warehouse.outbound-shipping.analytics`)

`<entity>` is the raising aggregate, lowercase, NO separators: `outboundload`,
`shippingpackage`, `billoflading`. Every type below is published on BOTH
streams under one `id` per occurrence; only the `dataschema` stream segment
differs. The `consumers` column names the INTEGRATION consumers; the service's
own projector also folds every type from the analytics stream.

| type | raised when | `data` | integration consumers |
|---|---|---|---|
| `com.warehouse.wms.outbound-shipping.outboundload.LoadPlanned` | the first manifested package plans a load for its (site, cpt, date, ship method) | `{load_id, site_id, cpt_id, operating_date, ship_method, cutoff, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadDoorAssigned` | an outbound dock appointment names the load's cpt | `{load_id, site_id, door_code, appointment_id, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadingStarted` | loading begins at a door with a docked trailer | `{load_id, site_id, door_code, trailer_id, started_at, version}` | yard-management |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadClosed` | every package is loaded or short-shipped with a reason | `{load_id, site_id, door_code, trailer_id, package_count, short_shipped_count, closed_at, version}` | yard-management |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadReopened` | a supervisor reopens a closed load | `{load_id, site_id, reopened_at, version}` | yard-management |
| `com.warehouse.wms.outbound-shipping.outboundload.LoadCancelled` | a planned load is cancelled | `{load_id, site_id, cancelled_at, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.outboundload.ShipmentConfirmed` | `TrailerDeparted` arrives for a CLOSED load (ADR 0002) | `{load_id, site_id, cpt_id, carrier, trailer_id, seal_number, departed_at, orders:[{order_id}], version}` | order-management, network-fulfillment |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageStaged` | a package is staged at a Staging location | `{package_id, load_id, site_id, staging_location, staged_at, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageLoaded` | a package is loaded through its load's door | `{package_id, load_id, site_id, loaded_at, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageUnloaded` | a loaded package comes back off the load | `{package_id, load_id, site_id, unloaded_at, reason, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.shippingpackage.PackageMissedLoad` | a package missed its load (departure while LOADING, cutoff sweep, or re-run) | `{package_id, load_id, site_id, source_order_id, cutoff, detected_at, version}` | exception-management, order-management |
| `com.warehouse.wms.outbound-shipping.billoflading.BillOfLadingIssued` | the BOL is issued with the ship confirmation | `{bol_number, load_id, site_id, carrier, trailer_id, seal_number, package_count, total_weight_g, issued_at, version}` | analytics only |
| `com.warehouse.wms.outbound-shipping.billoflading.BillOfLadingVoided` | an issued BOL is voided | `{bol_number, load_id, site_id, reason, voided_at, version}` | analytics only |

Payload rules:

- `ShipmentConfirmed.orders` lists every distinct `source_order_id` of the
  shipped packages; a package manifested without one is excluded and counted
  in analytics only (ADR 0003).
- `PackageMissedLoad` is raised at most once per package and load: the sweep
  is idempotent and a re-run publishes nothing.
- `PackageUnloaded.reason` is the operator's reason, omitted when none was
  given. A breaking payload change is a new `.v2` type and dataschema, never
  a mutation of v1.

### Consumed

| type | topic | key data |
|---|---|---|
| `com.warehouse.wes.fulfillment-execution.package.PackageManifested` | `warehouse.fulfillment.events` | `package_id, order_ref` (+ `site_id`, `source_order_id`, `cpt`, `weight_g` additive, ADR 0003) |
| `com.warehouse.wes.fulfillment-execution.package.PackageDiverted` | `warehouse.fulfillment.events` | `package_id` |
| `com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged` | `warehouse.process-path-management.events` | `site_id, timezone, cutoffs` (local CPT copy) |
| `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | `warehouse.facility.events` | zone to site mapping |
| `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | `warehouse.facility.events` | staging locations (role, zoneId) |
| `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | `warehouse.dock-scheduling.events` | `direction=OUTBOUND` only; `cpt_id`, `carrier_scac` nullable — pinned from the producer's `origin/feature/service` until it merges (spike Q5) |
| `com.warehouse.wms.yard-management.trailer.TrailerDocked` | `warehouse.yard-management.events` | fixture-validated against the proposed yard model until its contract merges |
| `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | `warehouse.yard-management.events` | same |
| `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | `warehouse.yard-management.events` | `trailer_id, site_id, departed_at, load_state, seal_number`; same |

Every other type on those topics is ignored. A message that is not a valid
CloudEvent is logged and skipped, never retried forever. The dock-scheduling
and yard-management rows are re-pinned from each producer's `origin/develop`
AsyncAPI before fleet wiring lands (orchestrator sequencing note, 2026-11-05).

### Analytics stream

Every published event above is ALSO written, in the same transaction and
under the same `id`, to `warehouse.outbound-shipping.analytics`. The `type`,
`subject`, Kafka key and payload are identical to the integration message;
only the `dataschema` stream segment differs
(`urn:warehouse:outbound-shipping:analytics:<EventName>:v1`). That stream is
not an integration contract: only this service's own projector consumes it.
