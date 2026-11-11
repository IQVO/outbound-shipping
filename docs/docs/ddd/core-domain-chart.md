---
id: core-domain-chart
title: Core domain chart
sidebar_label: Core domain chart
description: "Where outbound-shipping sits on the ddd-crew Core Domain Chart and why it is a Supporting subdomain."
---

# Core domain chart

Following the ddd-crew [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts): business differentiation (y) against model complexity (x).

```mermaid
quadrantChart
    title outbound-shipping on the Core Domain Chart
    x-axis Low model complexity --> High model complexity
    y-axis Low business differentiation --> High business differentiation
    quadrant-1 Core
    quadrant-2 Decisive - Short-term Core
    quadrant-3 Supporting
    quadrant-4 Generic
    outbound-shipping: [0.40, 0.35]
    Ship-confirm on departure: [0.55, 0.48]
    Load planning from the manifest: [0.45, 0.38]
    Missed-load detection: [0.35, 0.34]
    Bill of lading: [0.22, 0.14]
    Load board and adherence: [0.25, 0.20]
```

Source: `internal/domain/outboundload/load.go`, `internal/domain/outboundload/commands.go`, `internal/domain/shippingpackage/commands.go`, `internal/domain/billoflading/bol.go`, `internal/application/usecases/on_departure.go`, `internal/application/usecases/expect_package.go`, `internal/application/usecases/sweep_missed.go`, `internal/application/usecases/project_board.go`.

Omitted: the other contexts (their points are on their own charts; the fleet plot is the [Core Domain Chart](https://iqvo.github.io/warehouse-docs/strategic-design/core-domain-chart) in warehouse-docs, where this context is plotted at the same point as above, 0.40, 0.35, read on `origin/main` on 2026-11-11). The five slices are this page's reading of the code, not values taken from a design model: they place relative effort and distinctiveness, not measurements. All of them stay in quadrant 3 (Supporting) or on its edge.

## Classification: Supporting

The context point sits in **quadrant 3 (Supporting)** ([classification](/docs/ddd/subdomain-classification), [ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context)).

**Differentiation is moderate to low (y = 0.35).** The fleet does not win customers by how it records what was loaded on a truck. What this context protects is accuracy and timeliness of the "it left" fact that order-management, exception-management and the retail network act on. The highest slice is ship-confirm on departure, because getting it wrong (confirming a load that did not leave, or missing one that did) is the failure other contexts feel.

**Model complexity is moderate (x = 0.40).** Three aggregates, small state machines (5, 5 and 2 states), and cross-aggregate coordination that lives in use cases: one open load per cutoff key under a race backstop, a departure that must be handled differently in three load states, idempotency at three layers, and events that arrive out of order from five producers. There is no optimiser and no scoring.

## Slices

| Slice | Reading | Why |
| --- | --- | --- |
| Ship-confirm on departure (`OnDeparture`) | highest differentiation, highest complexity | Three-way split on the load state, the seal rule, the once-only bill of lading, the "never confirm a loading load" decision of [ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure). |
| Load planning from the manifest (`ExpectPackage`, `ResolveCutoff`) | middle | Resolves a manifested cutoff instant against a site's timezone schedule, with one open load per key and a retried race. |
| Missed-load detection (`FlagMissedLoad`, `SweepMissedLoads`) | lower | A once-per-package flag raised by a sweep or by an early departure. |
| Load board and adherence (`ProjectBoard`) | low | Plain counting over the own analytics stream. |
| Bill of lading (`BillOfLading`) | lowest | A commodity document with one invariant pair (confirmed load, seal). |

## Evolution

| Part | Evolution stage (reading) | Why |
| --- | --- | --- |
| Load, package and ship-confirm model | **Custom-built** | Specific to the fleet's split between the yard (physical facts) and shipping (the load). |
| Bill of lading | **Product** | A shipping document is commodity; a product could issue it if fed the same facts. |
| Board and adherence report | **Custom-built, thin** | A small projection with no analytics database behind it. |
| Plumbing (outbox, inbox, CloudEvents, idempotency) | **Commodity** | The same building blocks as every context of the fleet. |

**Movement to watch.** Pallets, load sequencing and a master bill of lading are deferred ([ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context), decision D4); adding them would raise model complexity but not differentiation. If loading adherence ever drove dispatch decisions rather than being reported, the missed-load slice would move toward the Core quadrant.
