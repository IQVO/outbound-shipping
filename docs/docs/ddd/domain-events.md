---
id: domain-events
title: Domain events
sidebar_label: Domain events
description: "The thirteen published and nine consumed domain events of outbound-shipping with their full CloudEvents types, triggers and policies."
---

# Domain events

The domain view of the event catalogue: which aggregate raises each event, what triggers it and which policy reacts to a consumed one. The wire shapes (fields, topics, envelope) are on [Events](/docs/api-reference/events); the decision record is [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue).

Source: `internal/domain/outboundload/events.go`, `internal/domain/shippingpackage/events.go`, `internal/domain/billoflading/events.go`, `internal/adapters/outbound/events/*.go`, `internal/adapters/inbound/kafka/*.go`, `apis/asyncapi.yaml`.

Every published event is also written to the analytics twin under the same CloudEvents id. The prefix of every published type is `com.warehouse.wms.outbound-shipping.`.

## Published (13)

| Event | Full CloudEvents type | Raised by | Trigger | Consumers |
| --- | --- | --- | --- | --- |
| `LoadPlanned` | `com.warehouse.wms.outbound-shipping.outboundload.LoadPlanned` | `OutboundLoad.Plan` | the first manifested package of a cutoff key | analytics only |
| `LoadDoorAssigned` | `...outboundload.LoadDoorAssigned` | `OutboundLoad.AssignDoor` | an outbound appointment names the load's cpt | analytics only |
| `LoadingStarted` | `...outboundload.LoadingStarted` | `OutboundLoad.StartLoading` | a trailer docked at the load's door | yard-management (intended) |
| `LoadClosed` | `...outboundload.LoadClosed` | `OutboundLoad.CloseLoad` | the supervisor closes the load | yard-management (intended) |
| `LoadReopened` | `...outboundload.LoadReopened` | `OutboundLoad.ReopenLoad` | the supervisor reopens a closed load | yard-management (intended) |
| `LoadCancelled` | `...outboundload.LoadCancelled` | `OutboundLoad.CancelLoad` | the supervisor cancels a planned load | analytics only |
| `ShipmentConfirmed` | `...outboundload.ShipmentConfirmed` | `OutboundLoad.ConfirmShipment` | `TrailerDeparted` for a `CLOSED` load | order-management; network-fulfillment (consumer built on its `develop`, not yet running) |
| `PackageStaged` | `...shippingpackage.PackageStaged` | `ShippingPackage.Stage` | a staging scan | analytics only |
| `PackageLoaded` | `...shippingpackage.PackageLoaded` | `ShippingPackage.Load` | a loading scan | analytics only |
| `PackageUnloaded` | `...shippingpackage.PackageUnloaded` | `ShippingPackage.Unload` | an unload scan | analytics only |
| `PackageMissedLoad` | `...shippingpackage.PackageMissedLoad` | `ShippingPackage.FlagMissedLoad` | the sweep, or a departure while the load was loading | order-management, exception-management |
| `BillOfLadingIssued` | `...billoflading.BillOfLadingIssued` | `BillOfLading.Issue` | the ship confirmation | analytics only |
| `BillOfLadingVoided` | `...billoflading.BillOfLadingVoided` | `BillOfLading.Void` | the supervisor voids the document | analytics only |

"Intended" means [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue) names the consumer but the consumer has no handler on its `develop`; only yard-management is in that state. network-fulfillment's handler exists on its `develop` but the running pod predates it (see [Integration](/docs/ecosystem/integration)).

State changes that raise **no** event: assigning a package to a load, `AttachTrailer`, `MarkShipped`, `MarkShortShipped`. The facts reach the outside through `LoadClosed.short_shipped_count` and `ShipmentConfirmed`.

## Consumed (9, plus the own analytics stream)

| Event | Full CloudEvents type | Producer | Policy | Effect |
| --- | --- | --- | --- | --- |
| `PackageManifested` | `com.warehouse.wes.fulfillment-execution.package.PackageManifested` | fulfillment-execution | PlanLoadFromCutoff, AssignPackageToLoad | plan or join the open load, package `EXPECTED` (`ExpectPackage`) |
| `PackageDiverted` | `com.warehouse.wes.fulfillment-execution.package.PackageDiverted` | fulfillment-execution | DropDiverted | package `SHORT_SHIPPED` with reason `DIVERTED` (`DivertPackage`) |
| `CPTScheduleChanged` | `com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged` | process-path-management | cutoff copy | replace the site's schedule (`ApplyCptSchedule`) |
| `ZoneRegistered` | `com.warehouse.wms.facility-layout.zone.ZoneRegistered` | facility-layout | staging-location copy | save zone to site (`ApplyLayout`) |
| `LocationSlotRegistered` | `com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered` | facility-layout | staging-location copy | save location and role (`ApplyLayout`) |
| `DockAppointmentBooked` | `com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked` | dock-scheduling | DoorFromAppointment | assign the door (`BookAppointment`) |
| `TrailerDocked` | `com.warehouse.wms.yard-management.trailer.TrailerDocked` | yard-management | StartOnDock | attach the trailer, start loading (`DockTrailer`) |
| `DoorWorkCompleted` | `com.warehouse.wms.yard-management.trailer.DoorWorkCompleted` | yard-management | record only | copy the completion (`RecordDoorWork`) |
| `TrailerDeparted` | `com.warehouse.wms.yard-management.trailer.TrailerDeparted` | yard-management | ConfirmOnDeparture, FlagDepartureWithoutClose | confirm or flag (`OnDeparture`) |

The own analytics consumer folds seven of the thirteen published types into `load_board` (`ProjectBoard`).

## Policies (when X then Y)

| When | Then | Policy name in the code |
| --- | --- | --- |
| `PackageManifested` | plan the load if none is open, expect and assign the package | PlanLoadFromCutoff, AssignPackageToLoad |
| `PackageDiverted` | short-ship the package | DropDiverted |
| outbound `DockAppointmentBooked` naming a cpt | assign the door | DoorFromAppointment |
| `TrailerDocked` at the load's door | attach the trailer and start loading | StartOnDock |
| `TrailerDeparted` on a `CLOSED` load | confirm, ship the packages, issue the bill of lading | ConfirmOnDeparture |
| `TrailerDeparted` on a `LOADING` load | flag every package not loaded, never confirm | FlagDepartureWithoutClose |
| the cutoff passes with packages not loaded | flag them once | SweepMissedLoad |

## Delivery and versioning

At-least-once delivery with the CloudEvents `id` as the dedupe key; every payload carries the aggregate `version` after the change; a breaking payload change would be a new `.v2` type and dataschema, never a mutation of v1. Within one aggregate id the Kafka key keeps events ordered on one partition.
