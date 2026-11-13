---
id: overview
title: API reference
sidebar_label: Overview
slug: /api-reference
description: "The interfaces outbound-shipping exposes (REST, events, MCP), the endpoint table and the contract conventions."
---

# API reference

Source: `apis/openapi.yaml`, `apis/asyncapi.yaml`, `internal/adapters/inbound/http/*.go`, `internal/adapters/inbound/mcp/*.go`.

## Interfaces

| Interface | Binary | Address | Contract | Notes |
|---|---|---|---|---|
| REST | `cmd/api` | `ADDR`, default `:8080` | `apis/openapi.yaml` (OpenAPI 3, 13 business routes plus 2 probes); the generated reference is in the sidebar under this page | JSON, RFC 7807 problems |
| Events | `cmd/api` (outbox relay) | Kafka | `apis/asyncapi.yaml` (AsyncAPI 2.6): publishes 13 types, consumes 9 | CloudEvents 1.0 structured mode; see [Events](/docs/api-reference/events) |
| MCP | `cmd/mcp` | `MCP_ADDR`, default `:8090`, paths `/` and `/mcp` | Streamable HTTP only | Read-only: 2 tools, 1 resource, 1 prompt; see [MCP tools](/docs/mcp/tools) |
| A2A | none | none | none | There is no `cmd/a2a`. |

REST and MCP are **unauthenticated**: there is no auth middleware and a fitness test (`TestNoAuthMiddlewareReintroduced`) keeps it that way. The service is released as `v0.1.0` and registered in warehouse-infra, and it is applied to the k3s cluster (2026-11-12): the Kong route `/api/outbound-shipping` its helm values declare is rendered there and answered 200 (read 2026-11-13; see the [runbook](/docs/operations/runbook#deployment-status-applied-to-k3s-running-v010)).

## REST endpoints

Writes are named for the thing they create (`stagings`, `closures`...) and every POST requires an `Idempotency-Key`. The operation ids are the ones of the OpenAPI file.

| Method and path | Operation | Use case | Success | Refusals |
|---|---|---|---|---|
| `POST /packages/{packageId}/stagings` | `stagePackage` | [StagePackage](/docs/ddd/use-cases) | 200 package | 400, 404, 409, 422 |
| `POST /packages/{packageId}/loadings` | `loadPackage` | LoadPackage | 200 package | 400, 404, 409 |
| `POST /packages/{packageId}/unloadings` | `unloadPackage` | UnloadPackage | 200 package | 400, 404, 409 |
| `POST /loads/{loadId}/closures` | `closeLoad` | CloseLoad | 200 load | 400, 404, 409 |
| `POST /loads/{loadId}/reopenings` | `reopenLoad` | ReopenLoad | 200 load | 400, 404, 409 |
| `POST /loads/{loadId}/cancellations` | `cancelLoad` | CancelLoad | 200 load | 400, 404, 409 |
| `POST /bills-of-lading/{bolNumber}/voids` | `voidBillOfLading` | VoidBillOfLading | 200 bill of lading | 400, 404, 409 |
| `POST /missed-load-sweeps` | `runMissedLoadSweep` | SweepMissedLoads | 200 `{flagged, swept_at}` | 400, 409 |
| `GET /loads` | `listLoads` | Queries | 200 page | 400 |
| `GET /loads/{loadId}` | `getLoad` | Queries | 200 load | 404 |
| `GET /loads/{loadId}/packages` | `listLoadPackages` | Queries | 200 page | 400, 404 |
| `GET /bills-of-lading/{bolNumber}` | `getBillOfLading` | Queries | 200 bill of lading | 404 |
| `GET /reports/load-adherence` | `getLoadAdherenceReport` | Queries | 200 report | 400 |
| `GET /healthz` | `getLiveness` | | 200 | |
| `GET /readyz` | `getReadiness` | | 200 | 503 |

There is no route that creates a load or a package and none that ship-confirms: packages and loads are created by consumed events, and confirmation follows the trailer's departure ([ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)).

### Request bodies

| Route | Body |
|---|---|
| stagings | `{"staging_location": "<code>"}` (required) |
| loadings | `{"load_id": "...", "door_code": "..."}` (both required) |
| unloadings | `{"reason": "..."}` (optional) |
| closures | `{"short_ships": [{"package_id": "...", "reason": "..."}]}` (optional) |
| reopenings | none |
| cancellations | `{"reason": "..."}` (required) |
| voids | `{"reason": "..."}` (required) |
| missed-load-sweeps | `{"site_id": "...", "cutoff": "<RFC 3339>"}` (`site_id` required) |

### Query parameters

| Route | Parameters |
|---|---|
| `GET /loads` | `site_id`, `state` (`PLANNED`, `LOADING`, `CLOSED`, `SHIP_CONFIRMED`, `CANCELLED`), `operating_date` (`YYYY-MM-DD`), `limit`, `cursor` |
| `GET /loads/{loadId}/packages` | `state` (`EXPECTED`, `STAGED`, `LOADED`, `SHIPPED`, `SHORT_SHIPPED`), `limit`, `cursor` |
| `GET /reports/load-adherence` | `site_id`, `operating_date` |

An undeclared query parameter is `400 unknown-query-parameter`.

## Contract conventions

- **Idempotency.** Every POST takes `Idempotency-Key` (spec: 16 to 128 characters; the code requires it present and at most 128). The first request runs; a replay with the same method, path and body returns the stored answer with `Idempotency-Replayed: true`; the same key with another request is `409 idempotency-key-reused`. A 2xx or 4xx answer is stored; a 5xx is rolled back and never stored, so the key can retry.
- **Errors.** `application/problem+json` (RFC 7807/9457) with `type` `https://errors.outbound-shipping.warehouse-systems.dev/<slug>`. The slug catalogue is in [Use cases](/docs/ddd/use-cases). Domain refusals map one to one to the model's invariant errors (`409 packages-outstanding`, `409 load-not-closed`, `422 not-staging-location`).
- **Pagination.** Keyset: `limit` 1 to 200 (default 50) and an opaque `cursor`; the page is `{"items": [...], "nextCursor": <string or null>}`.
- **Concurrency.** Optimistic, on the aggregate `version`; a lost race is `409 concurrent-modification`.
- **Naming.** JSON fields are snake_case, except the page field `nextCursor`. Timestamps are RFC 3339 UTC; operating dates are `YYYY-MM-DD`; weights are grams.
- **Resources.** `OutboundLoad` returns `id`, `site_id`, `cpt_id`, `operating_date`, `ship_method`, `cutoff`, `state`, optional `door_code`, `trailer_id`, `appointment_id`, `short_ships`, `package_count`, `version`. `ShippingPackage` returns `id`, `site_id`, optional `source_order_id`, `work_unit_ref`, `load_id`, `staging_location`, `state`, `version`. `BillOfLading` returns `bol_number`, `load_id`, `site_id`, `carrier`, optional `carrier_scac`, `trailer_id`, `seal_number`, `package_count`, `total_weight_g`, `issued_at`, optional `voided_at`, `void_reason`, `state`, `version`. The adherence report is `{rows: [{site_id, cpt_id, operating_date, loads_closed, loads_total, packages_missed, adherence}], generated_at}`.
- **Probes.** `GET /healthz` is `{"status":"ok"}`; `GET /readyz` is `{"status":"ready"}`, `{"status":"not_ready"}` while draining, or a `503 dependency-unavailable` problem.
- **Events.** CloudEvents 1.0, `type` `com.warehouse.wms.outbound-shipping.<entity>.<EventName>`, `source` `/warehouse/outbound-shipping`, `subject` the aggregate id (also the Kafka key), `dataschema` `urn:warehouse:outbound-shipping:<events|analytics>:<EventName>:v1`, snake_case `data` with the aggregate `version`. A breaking payload change is a new `.v2` type, never a mutation.
