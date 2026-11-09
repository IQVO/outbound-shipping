---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: "The shared vocabulary of outbound-shipping with the Go identifier, state or event that carries each term."
---

# Ubiquitous language

The terms below are the ones the code, the API and the events use. Each row names where the term lives, so a word in a conversation can be found in the source. Terms owned by a neighbour are marked and are used here only as facts.

Source: `internal/domain/outboundload/load.go`, `internal/domain/shippingpackage/package.go`, `internal/domain/billoflading/bol.go`, `internal/domain/shared/ids.go`, `internal/domain/shared/shortship.go`, `internal/application/usecases/usecases.go`.

## Core terms

| Term | Meaning | In the code |
|---|---|---|
| **Outbound load** | The packages planned to leave together on one trailer for one cutoff. Planned automatically from the first manifested package. | aggregate `outboundload.OutboundLoad`, id `shared.LoadID`, table `outbound_loads` |
| **Cutoff key** | `(site, cpt, operating date, ship method)`: at most one open load exists per key. | `outboundload.LoadKey`, partial unique index `uq_open_load_per_cutoff` |
| **CPT (critical pull time)** | The time of day a site's cutoff is due, from the site's schedule. Neighbour term (process-path-management). | `shared.CptID`, local copy `cpt_schedules` |
| **Cutoff** | The instant by which the load's packages must be on the truck. | `OutboundLoad.Cutoff()`, column `cutoff` |
| **Operating date** | The local calendar date a load ships on (`YYYY-MM-DD`, resolved in the site's timezone). | `outboundload.OperatingDate` |
| **Ship method** | The carrier service the cutoff entry ships by. | `shared.ShipMethod` |
| **Open load** | A load in `PLANNED` or `LOADING`. | `outboundload.StatePlanned`, `StateLoading` |
| **Shipping package** | One manifested package and where it is on its way to the truck. v1 ships packages, not pallets. | aggregate `shippingpackage.ShippingPackage`, id `shared.PackageID` |
| **Manifest** | The upstream fact that a package was packed and labelled. | `PackageManifested` (consumed), `usecases.Manifest` |
| **Expected** | A manifested package that has not been staged yet. | `shippingpackage.StateExpected` |
| **Staging** | Putting a package at a Staging location of its site before loading. | `ShippingPackage.Stage`, `PackageStaged` |
| **Staging location** | A facility-layout location whose role is `Staging`. Neighbour term (facility-layout). | local copies `zones`, `locations`; `ErrNotStagingLocation` |
| **Loading** | Scanning a package onto its load through the load's door. Also the load state while that happens. | `ShippingPackage.Load`, `StateLoading`, `PackageLoaded` |
| **Door** | The dock door a load is loaded through, assigned from a dock appointment. | `shared.DoorCode`, `OutboundLoad.AssignDoor`, `LoadDoorAssigned` |
| **Dock appointment** | The booking that names the door, the carrier and optionally the cutoff. Neighbour term (dock-scheduling). | `shared.AppointmentID`, local copy `dock_appointments` |
| **Trailer** | The physical trailer at the door. Neighbour term (yard-management). | `shared.TrailerID`, `OutboundLoad.Trailer()` |
| **Seal number** | The seal the yard recorded on departure; the bill of lading certifies it. | `Departure.SealNumber`, `billoflading.ErrSealMissing` |
| **Unload** | Taking a loaded package back off the load, back to `STAGED`, before departure. | `ShippingPackage.Unload`, `PackageUnloaded` |
| **Short-ship** | Deliberately leaving a package off a load, always with a reason. | `shared.ShortShip`, `StateShortShipped` |
| **Close** | The supervisor's declaration that every package is loaded or short-shipped. | `OutboundLoad.CloseLoad`, `LoadClosed` |
| **Reopen** | Returning a `CLOSED` load to `LOADING` before departure. | `OutboundLoad.ReopenLoad`, `LoadReopened` |
| **Cancel** | Dropping a `PLANNED` load that has no packages. | `OutboundLoad.CancelLoad`, `LoadCancelled` |
| **Ship-confirm** | The fact that the load physically left, taken from the trailer's departure, never from a click. | `OutboundLoad.ConfirmShipment`, `ShipmentConfirmed`, `StateShipConfirmed` |
| **Departure** | The yard's fact that a trailer left: trailer, seal and time. Neighbour fact. | `TrailerDeparted` (consumed), `usecases.Departure` |
| **Shipped** | State of a package on a confirmed load. | `shippingpackage.StateShipped`, `MarkShipped` |
| **Bill of lading (BOL)** | The shipping document of one confirmed load, issued exactly once, voidable. | aggregate `billoflading.BillOfLading`, id `shared.BolNumber`, table `bills_of_lading` |
| **Void** | Invalidating an issued bill of lading with a reason; the shipment is not undone. | `BillOfLading.Void`, `BillOfLadingVoided` |
| **Missed load** | A package that was not loaded by its cutoff (sweep) or by the trailer's departure. Raised once per package. | `ShippingPackage.FlagMissedLoad`, `PackageMissedLoad`, column `missed_flagged` |
| **Missed-load sweep** | The scheduled run that flags missed packages after a cutoff. | `usecases.SweepMissedLoads`, `POST /missed-load-sweeps` |
| **Diverted** | A package that fulfillment-execution diverted before loading; it is short-shipped with reason `DIVERTED`. | `PackageDiverted` (consumed), `usecases.DivertPackage` |
| **Source order id** | The order-management order the package ships for; what `ShipmentConfirmed.orders` lists. | `ShippingPackage.SourceOrderID()` |
| **Order ref (work unit ref)** | The manifest's own order reference, stored verbatim and never parsed. | `ShippingPackage.WorkUnitRef()` |
| **Load adherence** | The share of a site's loads that have closed, per cutoff and operating date (`loads_closed / loads_total`). The board also stores `closed_on_time`, which the report does not use. | `load_board`, `GET /reports/load-adherence` |
| **Load board** | The projection folded from this service's own analytics stream. | table `load_board`, `usecases.ProjectBoard` |
| **Loads at risk** | Open loads whose cutoff is within the horizon (default four hours). | MCP tool `loads_at_risk` |

## Technical terms

| Term | Meaning | In the code |
|---|---|---|
| **Outbox** | Events written in the aggregate's transaction and relayed to Kafka. | `outbox_events`, `postgres.OutboxRepo`, `outbox.Relay` |
| **Inbox (processed event)** | The `(consumer, event id)` claim that makes a consumer idempotent. | `processed_events`, `usecases.Intake` |
| **Outcome** | What a consumer did with an event: applied, duplicate, ignored. | `usecases.Outcome` |
| **Analytics twin** | The copy of every published event on the analytics topic. | `warehouse.outbound-shipping.analytics` |
| **Idempotency key** | The REST header that makes a POST safely repeatable. | `Idempotency-Key`, table `idempotency_keys` |
| **Local copy** | A neighbour's data kept from its events instead of queried live. | `cpt_schedules`, `zones`, `locations`, `dock_appointments`, `door_work_completions` |
| **Dead letter** | A consumed message that could not be applied, parked on `<topic>.dlq`. | `internal/adapters/inbound/kafka/deadletter.go` |

## State vocabularies

- **Load:** `PLANNED`, `LOADING`, `CLOSED`, `SHIP_CONFIRMED`, `CANCELLED`.
- **Package:** `EXPECTED`, `STAGED`, `LOADED`, `SHIPPED`, `SHORT_SHIPPED`.
- **Bill of lading:** `ISSUED`, `VOIDED`.
