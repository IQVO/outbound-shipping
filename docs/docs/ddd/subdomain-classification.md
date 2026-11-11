---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "Why outbound-shipping is a Supporting subdomain in the WMS area, with the classification of its neighbours."
---

# Subdomain classification

**outbound-shipping is a Supporting subdomain** of the WMS area. [ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context) takes the decision (`Create outbound-shipping, Supporting, subdomain wms`). Its CloudEvents types therefore start `com.warehouse.wms.outbound-shipping.` (see [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue)).

Source: ADR 0001 and ADR 0005; the aggregates in `internal/domain/outboundload`, `shippingpackage` and `billoflading`; the neighbours' rows are copied from the fleet table at [Subdomain Classification](https://iqvo.github.io/warehouse-docs/strategic-design/subdomain-classification) in warehouse-docs (`docs/strategic-design/subdomain-classification.md` on `origin/main`, read 2026-11-11). That table now has a row for outbound-shipping (Supporting, `wms`); it still has **none for dock-scheduling or yard-management**.

## Why Supporting

- **Not Core.** Nobody chooses a warehouse for how it records what was loaded on a truck. The model is three small aggregates with fixed state machines (`OutboundLoad` five states, `ShippingPackage` five states, `BillOfLading` two states) and about a dozen invariants. The rules that carry business judgement, such as the cutoff schedule, the door a load uses and the physical departure, belong to the neighbours that own them; this context consumes them as facts and keeps local copies. The competitive core of the fleet stays the work-planning and execution tier.
- **Not Generic.** A shipping document and load tally exist in any WMS, but the rules here are specific to this fleet: one open load per `(site, cutoff, operating date, ship method)` planned automatically from the first manifested package, ship-confirm that follows the trailer's physical departure rather than a manual click ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)), the once-only `PackageMissedLoad` fact that feeds re-promise and exception handling, and correlation fields carried by `PackageManifested` ([ADR 0003](/docs/adr/0003-package-manifested-carries-ship-correlation)). An off-the-shelf shipping module would not fit that event vocabulary without a translation layer.
- **It is necessary.** Before it existed, nothing in the fleet owned "what left on which truck": an order's last state was Released, and the manual network shipment confirmation ([ADR 0004](/docs/adr/0004-shipment-confirmed-replaces-the-manual-network-confirmation)) had no factual source.
- **Parts that lean Generic:** the bill of lading document (carrier, seal, count, weight, void) is a commodity record; it is one small aggregate and is unlikely to change shape. **Parts that could rise:** the cutoff and missed-load logic if loading adherence ever drives dispatch decisions; today `PackageMissedLoad` is only a fact that others act on.

What it deliberately does not own: transportation management (rating, tendering), pallets, load sequencing and master BOL (deferred, decision D4 of ADR 0001), sortation equipment, and everything before `PackageManifested` and the trailer, door and seal facts themselves (yard-management).

## Neighbours

Copied from warehouse-docs; the edges and relationships are on the [Context map](/docs/ddd/context-map) and [Integration](/docs/ecosystem/integration).

| Neighbour | Classification (warehouse-docs) | Its stated reason |
| --- | --- | --- |
| `fulfillment-execution` | Core | Pull-based dispatch with lease semantics is the throughput differentiator; pack and SLAM are Supporting and kept thin. |
| `process-path-management` | Generic | A process-path catalogue is well understood and does not differentiate; extracted because several contexts need the same definition. |
| `facility-layout` | Generic | The location hierarchy is the industry's WMS convention; it has to be correct, not clever. |
| `order-management` | Generic / Supporting | Intake is commodity; the fulfillment rules layered on top lift it toward Supporting. |
| `network-fulfillment` | Supporting | Lets the building's capability be sold through an external network; Conformist to the network and an Anti-Corruption Layer for the fleet. |
| `exception-management` | Supporting | Anomalies get an owner and an outcome; Supporting today, candidate Core. |
| `dock-scheduling` | not in the table | No row on `origin/main` of warehouse-docs. |
| `yard-management` | not in the table | No row on `origin/main` of warehouse-docs. |

For the fleet-wide plot see the [Core Domain Chart](https://iqvo.github.io/warehouse-docs/strategic-design/core-domain-chart) in warehouse-docs, and this context's own [Core domain chart](/docs/ddd/core-domain-chart).
