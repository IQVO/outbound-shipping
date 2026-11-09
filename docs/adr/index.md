---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `outbound-shipping`, in the order they were taken. The ADR bodies are
immutable; a changed decision gets a new ADR that supersedes the old one. The
*Status* column is copied from each ADR's own status line.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](/docs/adr/0001-outbound-shipping-bounded-context) | outbound-shipping is a bounded context of the warehouse fleet | Accepted (bootstrap, 2026-11-05). Finalizes design draft OS-0001 |
| [0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure) | Ship-confirm follows the physical departure | Accepted (2026-11-06). Finalizes design draft OS-0002; decision D3 is FINAL |
| [0003](/docs/adr/0003-package-manifested-carries-ship-correlation) | PackageManifested carries the correlation ship-confirm needs | Accepted (2026-11-06). Finalizes design draft OS-0003; decision D5 is FINAL. |
| [0004](/docs/adr/0004-shipment-confirmed-replaces-the-manual-network-confirmation) | ShipmentConfirmed replaces the manual network shipment confirmation | Accepted (2026-11-06). Finalizes design draft OS-0004; decision D6 is FINAL. |
| [0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue) | CloudEvents envelope and type catalogue | Accepted (2026-11-06) |
| [0006](/docs/adr/0006-yard-contract-pinned) | The yard-management contract is pinned and tested | Accepted (2026-11-09). Refines ADR 0002 and ADR 0005 (the yard rows of the consumed catalogue). |
