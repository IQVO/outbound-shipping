---
id: tools
title: MCP tools
sidebar_label: MCP tools
description: "The read-only MCP surface of outbound-shipping: two tools, one resource, one prompt, with inputs, outputs and annotations."
---

# MCP tools

Source: `internal/adapters/inbound/mcp/{server,tools,resources}.go`, `cmd/mcp/{main,router}.go`. This page is written from the registration code, **not** from a live `tools/list` call (the binary was not run to write it).

`cmd/mcp` is a separate binary serving MCP over **Streamable HTTP only** (no stdio, no SSE) at `/` and `/mcp` on `MCP_ADDR` (default `:8090`), plus `GET /healthz`. It is unauthenticated, like the rest of the fleet. The server name is `outbound-shipping-mcp`, version `1.0.0`.

The surface is **read-only**: every mutating operation (staging, loading, unloading, closing, reopening, cancelling, voiding a bill of lading, the missed-load sweep) stays on REST. `cmd/mcp` builds no event sink, no unit of work and no outbox repository, so it cannot write. It reads Postgres directly; the board behind `loads_at_risk` is the projector-fed `load_board`, which may lag the OLTP store by a few seconds.

The server's `initialize` instructions tell a client the same: the two tools below, the board may lag, and nothing here changes data.

## Tools

Both tools carry `ReadOnlyHint: true`, `IdempotentHint: true`, `OpenWorldHint: false` and the authority metadata `com.warehouse/authority: "L1"` (reads facts, changes nothing). A handler error is returned as an `isError` tool result carrying the error text, never as a protocol failure.

| Tool | Read-only | Authority | Purpose |
|---|---|---|---|
| `loads_at_risk` | yes | L1 | The open outbound loads whose cutoff falls within a horizon, ordered by cutoff, with the staged, loaded and missed package counts. |
| `missed_loads` | yes | L1 | The packages of a site that raised `PackageMissedLoad`, newest first. |

### `loads_at_risk`

Input:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `site_id` | string | no | Narrow to one site; empty means every site. |
| `horizon_hours` | integer | no | How far ahead a cutoff counts as at risk; default 4. |
| `limit` | integer | no | Row cap; default 50, maximum 200 (`invalid-limit` above). |

Output: `{"rows": [...]}` where each row has `load_id`, `site_id`, `cpt_id`, `operating_date`, `ship_method`, `cutoff` (RFC 3339), `minutes_left` (number), `staged_count`, `loaded_count`, `packages_missed`.

### `missed_loads`

Input:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `site_id` | string | **yes** | The site; a blank value fails with `invalid-site-id`. |
| `limit` | integer | no | Row cap; default 50, maximum 200 (`invalid-limit` above). |

Output: `{"rows": [...]}` where each row has `package_id`, `load_id`, `site_id`, optional `source_order_id`, `state`.

Optional inputs carry `omitempty`, so `tools/list` does not publish them as required (`TestMCPOptionalToolInputsCarryOmitempty`).

## Resource

| URI template | Name | MIME type | Content |
|---|---|---|---|
| `warehouse://outbound-shipping/loads/{loadId}` | outbound load | `application/json` | One load as `GET /loads/{loadId}` returns it: cutoff key, cutoff instant, state, door, trailer, appointment, loaded package count and version. An unknown id is `load-not-found`. |

## Prompt

| Name | Argument | Content |
|---|---|---|
| `load-close-readiness` | `site_id` (required) | A user message asking the model to summarise whether the site's loads can close before their cutoff: it instructs the client to call `loads_at_risk` (horizon four hours) and `missed_loads`, and to recommend finishing loading, short-shipping with reasons, or letting the sweep flag the misses. It forbids proposing any write through this server. |

## Error codes

Tool errors start with a stable code: `load-not-found`, `package-not-found`, `invalid-parameter`, `invalid-limit`, `invalid-site-id`. Anything else is the underlying error text.

## Fitness checks on this surface

`internal/architecture` pins the MCP adapter: it depends inward only (`TestMCPAdapterDependencyRule`), has no auth (`TestNoAuthMiddlewareReintroduced`), every tool declares an authority level and a write tool would have to be at least L3 (`TestEveryMCPToolDeclaresAuthority`, `TestMCPWriteToolsAreAtLeastL3`), and the server offers a resource and a prompt (`TestMCPServerOffersResourceAndPrompt`).
