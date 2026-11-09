# ADR 0002: Ship-confirm follows the physical departure

## Status

Accepted (2026-11-06). Finalizes design draft OS-0002; decision D3 is FINAL
(campaign decision log). Requires yard-management to be live before the
behaviour is exercised end to end.

## Context

WMS products commonly treat ship-confirm as an operator step that triggers
documents: close the load, print the bill of lading, mark shipped. Here,
`yard-management` is the only recorder of physical facts (YD-0004), including
`TrailerDeparted` with its seal number. If an operator confirmed at load
close, "shipped" would be true before the truck left, and could stay wrong if
it never did — the fleet would be back to telling the network about a
shipment on a belief, which is exactly the manual-POST problem OS-0004
removes.

## Decision

`ShipmentConfirmed` is raised when `TrailerDeparted` arrives for a CLOSED
load. The load's `ConfirmShipment` command requires the `CLOSED` state
(`ErrLoadNotClosed` otherwise) and consumes the departure facts — trailer id,
seal number, departed-at — as inputs; the domain never invents them.

If the departure arrives while the load is still LOADING, the load is NOT
confirmed (`ErrLoadNotClosed`): `PackageMissedLoad` is raised for every
package not loaded, and the load is left for a supervisor (who may short-ship
what is missing and close, or reopen). The same one-per-cutoff sweep rule
applies: a re-run raises nothing new.

Ordering note (recorded per PLAN wave 5): `TrailerDeparted` can arrive
BEFORE `LoadClosed` — the two facts travel on different topics, so the
consumption order is not guaranteed. The departure is handled, never
dropped, and never confirms: the load it finds is LOADING, so the
FlagDepartureWithoutClose path runs (packages flagged, load left for a
supervisor) even though the physical close had already happened upstream.
A load that closes only after its departure was consumed stays CLOSED
unconfirmed — no later event confirms it. A re-delivery of the same
departure occurrence changes nothing (the inbox dedupes on the CloudEvents
id); only a genuinely new `TrailerDeparted` for the trailer would confirm
the then-CLOSED load. The BDD scenario "A departure arriving before the
load closes is handled, never confirmed" pins this end to end.

The bill of lading is issued with the confirmation, using the seal number the
departure event carried (`ErrSealMissing` without one).

## Consequences

- "Shipped" means the goods left: the event is a physical fact, not an
  intention.
- No document exists until departure. A carrier that needs the bill of lading
  in hand before leaving cannot be served by v1. This must be checked against
  real carrier practice (PLAN section 8), and it is the most likely reason to
  revisit this decision.
- Requires yard-management to be live; until its contract merges, departures
  are consumed per the yard-management contract pinned in `contracts/` (ADR 0006); they were first validated as fixtures against the proposed yard model.
- A duplicate `TrailerDeparted` delivery must not confirm twice: the
  `SHIP_CONFIRMED` state itself is the guard (`ErrLoadConfirmed`), so the
  consumer's idempotency (dedupe on CloudEvents `id`) has a domain backstop.
