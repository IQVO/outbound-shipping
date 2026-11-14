# outbound-shipping

Outbound Shipping (WMS-tier Supporting bounded context of the warehouse fleet):
get every manifested package onto the right load before its cutoff, prove what
left on which trailer, and tell the fleet that an order has actually shipped.

## Disclaimer

This project is a domain-driven design study. It is not associated with,
endorsed by or affiliated with any company, and it uses no company's code,
data, logos or trademarks. All names, figures and examples are illustrative or
synthetic.

Status: **released as `v0.1.0`** (2026-11-11, from `main`; image `ghcr.io/iqvo/outbound-shipping:v0.1.0`
and the Helm chart `outbound-shipping-0.1.0.tgz` are published), **registered and image-pinned in
warehouse-infra (PR 205), and applied to the k3s cluster on 2026-11-12**: one `api` replica runs at
`857edfd` on `k3s-agent` (Argo CD Synced/Healthy, the Kong route `/api/outbound-shipping` answers 200;
read 2026-11-13). The seven Kafka topics were pre-created before the first sync, the MCP server is off
and there is no relay Deployment (the relay runs inside `cmd/api`). Edges to other contexts are live
where both ends run. Not verified: consumer-group lag and delivery from the outbox. The service has its three aggregates,
REST and read-only MCP surfaces, the Kafka consumers, the transactional outbox and
the ship-confirm that follows the trailer's physical departure (`yard-management`
`TrailerDeparted`, ADR 0002). The Helm chart is in `charts/outbound-shipping/` (api, optional mcp, Gateway API routes, Kong plugins).
The apply and what running looks like are in the runbook and in
`docs/docs/operations/before-first-release.md` (every checklist row done).
It owns `OutboundLoad`, `ShippingPackage` and `BillOfLading` (see `docs/adr/`, ADRs
0001 to 0008). Binaries: `cmd/api` (REST, six Kafka consumers, outbox relay) and
`cmd/mcp` (read-only MCP).

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`,
`KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.warehouse.wms.outbound-shipping.<entity>.<Event>`), transactional outbox.

## Documentation

The documentation site of this service is published at <https://iqvo.github.io/outbound-shipping/>
(source in `docs/`, built by `.github/workflows/docs.yml`).

| Page | What it covers |
| --- | --- |
| [Introduction](docs/docs/overview/introduction.md) | What the context owns and does not own, subdomain, capabilities |
| [Architecture](docs/docs/overview/architecture.md) | Hexagonal layout, binaries and ports, container diagram, data stores |
| [Quickstart](docs/docs/overview/quickstart.md) | Prerequisites, make targets, booting, first calls |
| [Configuration](docs/docs/operations/configuration.md) | Every environment variable of `cmd/api` and `cmd/mcp` |
| [Runbook](docs/docs/operations/runbook.md) | Deployment status, probes, migrations, Kafka, outbox relay, dead letters |
| [Observability](docs/docs/operations/observability.md) | OpenTelemetry signals, logs, probes, SQL signals, what is not instrumented yet |
| [Troubleshooting](docs/docs/operations/troubleshooting.md) | Symptom, cause, check, fix |
| [Before the first release](docs/docs/operations/before-first-release.md) | Packaging checklist and its state |
| [Testing](docs/docs/development/testing.md) | Test pyramid, make targets, CI jobs |
| [Integration](docs/docs/ecosystem/integration.md) | Every upstream and downstream edge |
| [Use cases](docs/docs/ddd/use-cases.md) | Trigger, inputs, invariants, events per use case |
| [Subdomain classification](docs/docs/ddd/subdomain-classification.md) | Why Supporting, neighbours |
| [DDD artifacts](docs/docs/ddd/ddd-artifacts.md) | Index of the ddd-crew pack |
| [Ubiquitous language](docs/docs/ddd/ubiquitous-language.md) | Glossary with Go identifiers |
| [Core domain chart](docs/docs/ddd/core-domain-chart.md) | Differentiation against complexity |
| [Bounded context canvas](docs/docs/ddd/bounded-context-canvas.md) | Purpose, messages, assumptions |
| [Context map](docs/docs/ddd/context-map.md) | Neighbours with pattern and channel |
| [Aggregate design canvas](docs/docs/ddd/aggregate-design-canvas.md) | States, invariants, commands, events |
| [Domain message flow](docs/docs/ddd/domain-message-flow.md) | Message flows between actors and contexts |
| [EventStorming](docs/docs/ddd/eventstorming.md) | Timeline and hotspots |
| [Class diagram](docs/docs/ddd/class-diagram.md) | Domain types, ports and adapters |
| [Entity relationship](docs/docs/ddd/entity-relationship.md) | PostgreSQL schema |
| [Sequence diagrams](docs/docs/ddd/sequence-diagrams.md) | Runtime sequences of the main flows |
| [Domain events](docs/docs/ddd/domain-events.md) | Published and consumed events with CloudEvents types |
| [API overview](docs/docs/api-reference/overview.md) | Interfaces, endpoint table, contract conventions |
| [Events](docs/docs/api-reference/events.md) | The event catalogue |
| [MCP tools](docs/docs/mcp/tools.md) | The read-only MCP tools, resource and prompt |
| [ADRs](docs/adr/index.md) | Architecture decision records 0001 to 0008 |

## Observability

Both binaries export OpenTelemetry traces and metrics over OTLP/gRPC when `OTEL_EXPORTER_OTLP_ENDPOINT` is set (empty means telemetry off), write JSON logs whose records carry `trace_id` and `span_id` while a span is active, and keep `GET /healthz` unchanged ([ADR 0008](docs/adr/0008-adopt-the-fleet-observability-standard.md)).

| Variable | Default | Meaning |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | none (telemetry off) | OTLP/gRPC Collector, `host:port` or URL |
| `OTEL_SERVICE_NAME` | `outbound-shipping` (`cmd/api`), `outbound-shipping-mcp` (`cmd/mcp`) | `service.name` |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

Exported: `http.server.request.duration` and `http.server.active_requests` on the REST and MCP routers, the Go runtime instruments, one server span per request named after the route pattern, and the business counter `outbound.loads.closures` (`outcome` = `closed` or `failed`, `reason` = the problem slug on failure). Consumer lag, outbox depth and dead-letter rate are not instrumented yet. Details and the full variable list: [Observability](docs/docs/operations/observability.md), [Configuration](docs/docs/operations/configuration.md).
