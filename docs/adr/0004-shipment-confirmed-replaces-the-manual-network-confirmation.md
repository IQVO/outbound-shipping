# ADR 0004: ShipmentConfirmed replaces the manual network shipment confirmation

## Status

Accepted (2026-11-06). Finalizes design draft OS-0004; decision D6 is FINAL.
Depends on [ADR 0003](./0003-package-manifested-carries-ship-correlation.md)
and amends `network-fulfillment` ADR 0014 and ADR 0026 (their change lands in
wave 6, not here).

## Context

`POST /network-orders/{networkRef}/shipment-confirmation` is the only trigger
today, and network-fulfillment ADR 0014 says an event-driven caller can
replace it once a persisted correlation exists. ADR 0026 reports one package
per order until a fleet fact carries the real number. Both premises are now
met: `ShipmentConfirmed.orders[].order_id` joins `NetworkOrder.localOrderId`
(the spike proved the id chain), and the load knows exactly which packages
left.

## Decision

network-fulfillment (wave 6) consumes `ShipmentConfirmed`, finds each
`NetworkOrder` by `localOrderId`, and confirms it through the existing
idempotent use case. It reports the package count from the event. An order
with no matching `NetworkOrder` is not network work and is skipped. A
re-delivered event confirms nothing twice: the consumer dedupes on the
CloudEvents `id` and the use case is idempotent on its own key.

The POST stays for one release as an operator fallback, then is deprecated.
Neither path double-submits: both go through the same idempotent use case.

No `retailnetwork.*` topic is consumed or produced by this service;
network-fulfillment stays the only channel to the network (hard boundary,
ADR 0001).

## Consequences

- The network hears about real departures without a human call.
- An order spread over two loads produces two events. Whether the network
  accepts partial confirmation has to be checked against the Vendor API
  before wave 6 ships (open question, recorded in the plan).
- order-management (wave 6, D7) consumes the same event to add order-level
  `Shipped`/`PartiallyShipped` and line `Shipped`; this service does not
  write order state.

## Addendum 2026-11-13: current state

The consumer is built on network-fulfillment's `develop`: `internal/adapters/inbound/kafka/shipment_confirmed_consumer.go`
(its PR 165, merged 2026-11-12), started from `cmd/netfulfil/outbound_shipping.go`. It dedupes through the
confirmation use case's `(shipment-ref, order-id)` key (load id plus order id), not on the CloudEvents `id`.
It reads an optional `package_count` per order that `ShipmentConfirmed` does not carry yet; `0` keeps the
single-package placeholder. The running pod predates the merge ([Integration](/docs/ecosystem/integration)).
