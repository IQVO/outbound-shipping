# ADR 0003: PackageManifested carries the correlation ship-confirm needs

## Status

Accepted (2026-11-06). Finalizes design draft OS-0003; decision D5 is FINAL.
The wave-1 correlation spike verified the premise at code level on every
producer's `origin/develop` (spike-correlation.md, verdict GO); the live
one-order proof on the kind cluster is pending-e2e.

## Context

`PackageManifested` from fulfillment-execution carries `package_id` and
`order_ref`, where `order_ref` is a work-unit reference whose shape depends
on the packing path (bare order id on the rebin route, `{order}-line-{n}` on
released work). `network-fulfillment` ADR 0014 records that no table stores
the hop from a work unit to the order-management `OrderId`, and its hard rule
4 forbids string-convention joins. `Task.sourceOrderId` carries the upstream
order on released work, but the PACK task created at rebin did not have it
stamped.

The spike (Q1, Q2) proved: `WorkReleased.ref` == order-management's
`OrderId` == `NetworkOrder.localOrderId`, and the order id is reachable at
SLAM time for every package path once the rebin-created PACK task is stamped;
the CPT is on the task and the weight is a `RunSlam` parameter. The site has
NO source anywhere in the order-work chain (spike Q3).

## Decision

fulfillment-execution adds these optional fields to `PackageManifested`:
`site_id`, `source_order_id`, `cpt`, `weight_g` — additive, `omitempty`,
never parsed from an identifier, dataschema stays `:v1`. The prerequisite
producer change (stamping the rebin PACK task's `sourceOrderId`) ships in the
same producer PR (wave 2, `feature/manifest-correlation`).

This service consumes them as the only package-to-order correlation:

- `ShipmentConfirmed.orders[].order_id` comes from `source_order_id`.
- `PackageMissedLoad.source_order_id` likewise, so order-management can
  re-promise without parsing anything.
- `weight_g` accumulates into the bill of lading's `total_weight_g`.

A package manifested without `source_order_id` can still be staged, loaded
and shipped, but it is left out of `orders[]` and counted in analytics.
`site_id` is omitted by the producer today (no source, spike Q3); the load's
site comes from the local CPT copy keyed by `cpt` until a site fact flows,
which is recorded as a model gap, not worked around by parsing.

## Consequences

- Additive, so the three existing consumers are unaffected (spike Q7: every
  decode site is a fixed struct without `DisallowUnknownFields`).
- Closes the gap that forced the manual endpoint in network-fulfillment ADR
  0014, and removes its single-package assumption (ADR 0026) by carrying the
  real count.
- `order_ref` stays semantically ambiguous fleet-wide (work-unit shape vs
  bare order id); reconciling it is out of scope here and left as a recorded
  follow-up.
