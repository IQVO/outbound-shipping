---
id: introduction
title: Introduction
sidebar_label: Introduction
description: "What outbound-shipping owns and does not own, its place in the warehouse fleet, and where to read next."
---

# Introduction

`outbound-shipping` is the bounded context that gets every manifested package onto the right load before its cutoff, proves what left on which trailer, and tells the fleet that an order has actually shipped. It is a **Supporting** subdomain in the **WMS** area of the warehouse fleet ([classification](/docs/ddd/subdomain-classification), [ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context)). It is **released as `v0.1.0`** (2026-11-11), **registered and image-pinned in warehouse-infra** (PR 205), and **applied to the k3s cluster** on 2026-11-12: one `api` replica runs at `857edfd` (Argo CD Synced/Healthy, the Kong route answers 200; read 2026-11-13), and the edges to the contexts that also run are live (see the [runbook](/docs/operations/runbook#deployment-status-applied-to-k3s-running-v010) and [Integration](/docs/ecosystem/integration#edge-status-in-the-cluster)).

Source: `docs/adr/0001` to `0006`, `internal/domain/*`, `internal/application/usecases/*`, `cmd/`, `apis/`.

## The business problem

`fulfillment-execution` stops at `PackageManifested`: the package is packed and labelled. `yard-management` owns the trailer, the door work, the seal and the departure. Between them nothing owned staging, what is on a load, the bill of lading, or the fact that the goods left; an order's last state was *Released*. This context fills that gap with one owner for "what left on which truck".

## What it owns

| Aggregate | Identity | What it is | States |
|---|---|---|---|
| `OutboundLoad` | `load_id` | The packages planned to leave together for one `(site, cpt, operating date, ship method)`; planned automatically from the first manifested package. | `PLANNED`, `LOADING`, `CLOSED`, `SHIP_CONFIRMED`, `CANCELLED` |
| `ShippingPackage` | `package_id` | One manifested package and where it is on its way to the truck (v1 ships packages, not pallets). | `EXPECTED`, `STAGED`, `LOADED`, `SHIPPED`, `SHORT_SHIPPED` |
| `BillOfLading` | `bol_number` | The shipping document of one confirmed load, issued exactly once per load, with carrier, trailer, seal, package count and total weight. | `ISSUED`, `VOIDED` |

It also keeps event-fed **local copies** of what it needs from neighbours (cutoff schedule, staging locations, dock appointments, door-work completions) and one **read model**, the load board behind the load-adherence report and the `loads_at_risk` MCP tool.

## What it does not own

- The trailer, the door work, the seal and the physical departure: [yard-management](/docs/ecosystem/integration) records them; this context only reacts.
- The dock appointment and the door assignment: dock-scheduling.
- The cutoff schedule (process-path-management) and the staging locations (facility-layout): consumed as local copies, never queried live.
- The package before it is manifested: fulfillment-execution.
- Transportation management (rating, tendering), pallets, load sequencing, master bill of lading and automated sortation or loading: out of scope ([ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context)).
- The shipment confirmation toward the retail network: network-fulfillment consumes `ShipmentConfirmed`; this service never talks to the network.

## Main capabilities

1. **Plan a load from the first manifest.** `PackageManifested` resolves to a cutoff key through the local cutoff schedule; the first package of a key plans an `OutboundLoad` (`LoadPlanned`), later ones join it. One open load per key is a domain invariant backed by a partial unique index.
2. **Assign the door and start loading from facts.** An outbound `DockAppointmentBooked` naming the load's cutoff assigns the door (`LoadDoorAssigned`); a `TrailerDocked` at that door attaches the trailer and starts loading (`LoadingStarted`). There is no REST route to start loading.
3. **Stage, load and unload by scan.** REST commands move a package to `STAGED` at a Staging location of its site, to `LOADED` through its load's door, and back to `STAGED`.
4. **Close, reopen or cancel a load.** A load closes only when every package is loaded or short-shipped with a reason; a supervisor can reopen a closed load or cancel a planned one that has no packages.
5. **Ship-confirm on the physical departure.** `TrailerDeparted` from yard-management confirms a `CLOSED` load: `ShipmentConfirmed` (carrying the distinct orders), packages `SHIPPED`, and one `BillOfLading` issued with the yard's seal. A departure while the load is still `LOADING` never confirms: it flags every package not loaded with `PackageMissedLoad` ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)).
6. **Sweep missed loads.** `POST /missed-load-sweeps` flags, exactly once per package, every staged or expected package whose load's cutoff has passed.
7. **Report adherence.** `GET /reports/load-adherence` and the MCP tools `loads_at_risk` and `missed_loads` read the board and the package store.

## Interfaces at a glance

| Interface | Surface |
|---|---|
| REST | 13 routes plus `/healthz` and `/readyz` (`cmd/api`, `apis/openapi.yaml`) |
| Events | publishes 13 types on `warehouse.outbound-shipping.events` and its analytics twin; consumes 9 types from 5 topics plus its own analytics stream ([events](/docs/api-reference/events)) |
| MCP | read-only, 2 tools, 1 resource, 1 prompt (`cmd/mcp`, [tools](/docs/mcp/tools)) |
| A2A | none: there is no `cmd/a2a` |

REST and MCP are unauthenticated, like every service of the fleet.

## Where to go next

| You want to | Read |
|---|---|
| Run or test it | [Quickstart](/docs/overview/quickstart), [Testing](/docs/development/testing) |
| See how it is built | [Architecture](/docs/overview/architecture), [Entity relationship](/docs/ddd/entity-relationship) |
| Configure and operate it | [Configuration](/docs/operations/configuration), [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| Know what each trigger does | [Use cases](/docs/ddd/use-cases), [Sequence diagrams](/docs/ddd/sequence-diagrams) |
| Understand the model | [Ubiquitous language](/docs/ddd/ubiquitous-language), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| Integrate with it | [API overview](/docs/api-reference), [Events](/docs/api-reference/events), [Integration](/docs/ecosystem/integration), [MCP tools](/docs/mcp/tools) |
| Know why it is shaped this way | [ADR index](/docs/adr) |
