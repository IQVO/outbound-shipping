# ADR 0010: Rulings on the spec-versus-code divergences the BDD wave found

## Status

Accepted (2026-11-15). Rules seven divergences between `apis/openapi.yaml`, the docs and the code. Refines the
REST error mapping of ADR 0005 and the load-adherence reading of ADR 0008 without changing their bodies.

## Context

The documentation-derived BDD scenarios (`features/`) pinned every documented rule and found seven places where
the OpenAPI spec, the docs and the code disagree. No ADR decided any of them. The authority order is: ADR, then
`apis/openapi.yaml`, then the repo docs, then the warehouse-docs pack. Where the spec is right the code changes;
where the code is deliberate (an ADR says so, tests pin it, the fleet relies on it) the spec text changes.

## Decision

| # | Divergence | Ruling | Why |
|---|---|---|---|
| 1 | A `short_ships` entry without a reason closed the load with `500 internal-error`. | **Code.** `CloseLoad` validates every entry (`ShortShip.Valid`) before it touches a package; `ErrShortShipReasonRequired` (both packages), `ErrShortShipPackageRequired` and `ErrInvalidShortShip` map to `400 bad-request`. | openapi `ShortShip.reason` is `minLength: 1` and `BadRequest` is the missing or invalid required field; use-cases.md: "each entry needs both fields". A client error is never a 500. |
| 2 | Cancelling a `SHIP_CONFIRMED` load answered `load-has-packages`. | **Code.** `CancelLoad` asks the aggregate (`CheckCancellable`) for the state refusal before it looks at packages: `load-confirmed`. | openapi cancelLoad ("refuses with `409 load-confirmed`") and the canvas ("`refuseIfConfirmed` on every command") agree. |
| 3 | Cancelling a `LOADING` load: openapi `load-state`, use-cases.md and the code `load-has-packages`. | **Code.** Any non-`PLANNED` load is `load-state` (and `SHIP_CONFIRMED` is `load-confirmed`); `load-has-packages` stays for a `PLANNED` load that has packages. The spec text lists `load-has-packages` too, because openapi never named the slug the code and the docs use for that case. | openapi cancelLoad states `load-state` for a `LOADING` load. Both readings agree that a load with packages cannot be cancelled; they differ only in which slug a `LOADING` load gets, and the spec outranks the docs. |
| 4 | `loads_closed`: openapi says "closed at or before their cutoff"; the code counts every closed load. | **Text.** The openapi description now says every load that has closed, on time or late. | ADR 0008 ("counts `loads_closed / loads_total`"), use-cases.md, ubiquitous-language.md (`closed_on_time` is stored but unused), the Postgres query (`closed_at IS NOT NULL`) and the projector tests agree with the code. |
| 5 | `nextCursor` after an exactly-full last page was set, so the next page was empty. | **Code.** `ListLoads` and `ListLoadPackages` read one row past the page; the cursor is set only when that row exists. | openapi `LoadPage`: "null after the last". The spec does not allow a cursor to an empty page. |
| 6 | `Idempotency-Key`: openapi `minLength: 16`; the code accepts 1 to 128. | **Text.** The openapi key is 1 to 128 characters. | facility-layout, the fleet reference for the middleware, accepts 1 to 128; fleet clients (UIs, e2e, simulators, agents) send keys under that contract; troubleshooting.md already documented the code's bound. Raising the floor to 16 here alone would turn working callers' requests into 400s for no domain reason. |
| 7 | A close that names a package the diversion policy already short-shipped: the docs say "recorded once with `DIVERTED`". | **Text.** Recorded once. When the command names it, the entry carries the operator's reason; when it does not, `DIVERTED`. | The `DIVERTED` wording describes the entry the close adds on its own. A named entry is the operator's explicit statement and is kept once (the "does not record it twice" scenario). No consumer reads the reason as a code. |

## Consequences

- `POST /loads/{id}/closures` with a bad short-ship is `400`, with nothing changed (the check runs before the
  unit of work). `POST /loads/{id}/cancellations` names the load's state first; a `PLANNED` load with packages
  is still `load-has-packages`, a `LOADING` one is now `load-state` (before: `load-has-packages`).
- List responses never carry a cursor that leads to an empty page. A client that stopped at a full page of
  exactly `limit` items now sees `null` instead of a cursor.
- The openapi changes are text and one bound (`Idempotency-Key` `minLength` 16 to 1); the REST reference is
  regenerated from it. No Kafka contract, envelope or outbox rule changes.
- Not decided here: the appointment-before-load gap (ADR 0011, Proposed).
