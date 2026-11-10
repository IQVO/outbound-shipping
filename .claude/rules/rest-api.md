---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

<!-- outbound-shipping: filled from the canonical model (campaign model.yaml). -->
# REST API guideline (this repo)

Fleet style: hexagonal, the HTTP adapter is a thin translator; one use case
per route; no auth (consistent with the fleet, `TestNoAuthMiddlewareReintroduced`);
JSON, snake_case; errors are RFC 7807 `application/problem+json` (`type`,
`title`, `status`, `code`, `detail`, `instance`; `code` is the stable error
code of the `Problem` schema in `apis/openapi.yaml`), 4xx for domain refusals,
409 for state conflicts, 404 for unknown ids. Every POST goes through the
`Idempotency-Key` middleware (`internal/adapters/inbound/http/idempotency.go`).

## Routes (the contract is `apis/openapi.yaml`)

Commands:

- `POST /packages/{packageId}/stagings` — StagePackage (dock operator scan)
- `POST /packages/{packageId}/loadings` — LoadPackage (dock operator scan)
- `POST /packages/{packageId}/unloadings` — UnloadPackage (dock operator scan)
- `POST /loads/{loadId}/closures` — CloseLoad (dock supervisor)
- `POST /loads/{loadId}/reopenings` — ReopenLoad (dock supervisor)
- `POST /loads/{loadId}/cancellations` — CancelLoad (dock supervisor)
- `POST /bills-of-lading/{bolNumber}/voids` — VoidBillOfLading (supervisor)
- `POST /missed-load-sweeps` — SweepMissedLoad (cron)

Queries:

- `GET /loads` — load board list (per site/cutoff: expected, staged, loaded)
- `GET /loads/{loadId}` — one load
- `GET /loads/{loadId}/packages` — the load's packages
- `GET /bills-of-lading/{bolNumber}` — one BOL
- `GET /reports/load-adherence` — adherence report

Every route MUST exist in `apis/openapi.yaml` (api-lint gate) and every
operation carries a non-empty description.
