# outbound-shipping

Outbound Shipping (WMS-tier Supporting bounded context of the warehouse fleet):
get every manifested package onto the right load before its cutoff, prove what
left on which trailer, and tell the fleet that an order has actually shipped.

## Disclaimer

This project is a domain-driven design study. It is not associated with,
endorsed by or affiliated with any company, and it uses no company's code,
data, logos or trademarks. All names, figures and examples are illustrative or
synthetic.

Status: under construction. Decisions D1-D7 are FINAL (see the campaign's
decision log); this repo owns `OutboundLoad`, `ShippingPackage` and
`BillOfLading` (see `docs/adr/`).

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`,
`KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.warehouse.wms.outbound-shipping.<entity>.<Event>`), transactional outbox.
