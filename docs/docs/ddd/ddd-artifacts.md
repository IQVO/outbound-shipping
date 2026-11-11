---
id: ddd-artifacts
title: DDD artifacts
sidebar_label: DDD artifacts
description: "The ddd-crew artifact pack of outbound-shipping: core domain chart, bounded context canvas, context map, aggregate design canvas, message flows, EventStorming and the diagrams of the built service."
---

# DDD artifacts

The strategic and tactical design of `outbound-shipping` follows the [ddd-crew](https://github.com/ddd-crew) tools. The pack was written from the code of this repository: every Mermaid diagram carries a `Source:` line naming the files it was drawn from. There is no hand-written design pack in the central documentation site for this context, so where a page reads the design, it reads [ADR 0001](/docs/adr/0001-outbound-shipping-bounded-context) to [ADR 0007](/docs/adr/0007-dock-scheduling-contract-pinned) and the code, and where they differ the code wins and the page says so.

Source: `internal/domain/`, `internal/application/`, `internal/adapters/`, `migrations/`, `apis/`, `docs/adr/`.

| Page | What it holds | ddd-crew tool |
| --- | --- | --- |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Load, cutoff key, package, staging, short-ship, ship-confirm, bill of lading and the neighbour terms, with the Go identifier of each | glossary |
| [Core domain chart](/docs/ddd/core-domain-chart) | Why the context is Supporting, with its internal slices | [core-domain-charts](https://github.com/ddd-crew/core-domain-charts) |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | Purpose, classification, roles, every inbound and outbound message, assumptions | [bounded-context-canvas](https://github.com/ddd-crew/bounded-context-canvas) |
| [Context map](/docs/ddd/context-map) | Every upstream and downstream neighbour with its pattern and channel | [context-mapping](https://github.com/ddd-crew/context-mapping) |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | `OutboundLoad`, `ShippingPackage`, `BillOfLading`: states, invariants, commands, events | [aggregate-design-canvas](https://github.com/ddd-crew/aggregate-design-canvas) |
| [Domain message flow](/docs/ddd/domain-message-flow) | Plan to ship-confirm, the departure that comes too early, diversion and the sweep | [domain-message-flow-modelling](https://github.com/ddd-crew/domain-message-flow-modelling) |
| [EventStorming](/docs/ddd/eventstorming) | The process-level timeline with hotspots and how each was settled | [eventstorming-glossary-cheat-sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) |
| [Class diagram](/docs/ddd/class-diagram) | Domain types and the hexagonal ports and adapters | UML |
| [Entity relationship](/docs/ddd/entity-relationship) | The Postgres schema | ER |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | One runtime sequence per main flow | UML |
| [Domain events](/docs/ddd/domain-events) | The 13 published and 9 consumed events with full CloudEvents types | event catalogue |

Also in this section: [Use cases](/docs/ddd/use-cases) (trigger, inputs, invariants, events per use case) and [Subdomain classification](/docs/ddd/subdomain-classification).

## In numbers (from the code)

| Item | Count | Where |
| --- | --- | --- |
| Aggregates | 3: `OutboundLoad` (5 states), `ShippingPackage` (5 states), `BillOfLading` (2 states) | [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) |
| Application use cases | 17 (8 REST commands, 9 consumer policies) plus the read side | [Use cases](/docs/ddd/use-cases) |
| Published events | 13 types, each on the integration topic and its analytics twin | [Events](/docs/api-reference/events) |
| Consumed events | 9 types from 5 topics, plus the 13 own types from the analytics stream | [Events](/docs/api-reference/events) |
| REST routes | 13 business routes plus `/healthz` and `/readyz` | [API reference](/docs/api-reference) |
| MCP | 2 read-only tools, 1 resource, 1 prompt | [MCP tools](/docs/mcp/tools) |
| Tables | 12 | [Entity relationship](/docs/ddd/entity-relationship) |
| ADRs | 6 | [ADR index](/docs/adr) |
