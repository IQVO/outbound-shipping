<!-- outbound-shipping: filled from the canonical model (campaign model.yaml). -->
# REST API guideline (this repo)

Fleet style: hexagonal, the HTTP adapter is a thin translator; one use case
per route; no auth (consistent with the fleet); JSON, snake_case; errors as
`{"error": {"code": "<ErrName>", "message": "..."}}` with the domain error
name, 4xx for domain refusals, 409 for state conflicts, 404 for unknown ids.

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
- `GET /reports/load-adherence` — adherence report (wave 5)

Every route MUST exist in `apis/openapi.yaml` (api-lint gate) and every
operation carries a non-empty description.
