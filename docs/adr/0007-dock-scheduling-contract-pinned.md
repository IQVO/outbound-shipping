# ADR 0007: The dock-scheduling contract is pinned and tested

## Status

Accepted (2026-11-10). Completes ADR 0006 (its dock-scheduling follow-up).

## Context

A load is planned from `DockAppointmentBooked` of `warehouse.dock-scheduling.events`. Until now the consumer's struct was
copied by hand from the producer's feature branch, with a note to re-pin once the producer merged. dock-scheduling is merged
and released (v0.1.2), so the copy can be the real contract. Its AsyncAPI is 2.6 (a channel with `subscribe.message.oneOf`),
not the 3.0 layout of the yard contract, so the yard test cannot be reused as is.

## Decision

- `contracts/dock-scheduling.asyncapi.yaml` is a copy of dock-scheduling's `apis/asyncapi.yaml` on `develop`, listed in
  `contracts/pinned.json` and kept equal to it by `scripts/harness/pinned_contract_gate.py`.
- `TestDockAppointmentBookedMatchesTheProducersIntegrationContract` proves that the type is declared on the integration
  channel (an analytics-only twin does not count), that the producer's example decodes into the struct this service reads,
  and that the fields it acts on are present (`cpt_id` may be null, its key must exist).
- `TestDockPublishedOnIntegrationIgnoresTheAnalyticsTwin` keeps the check itself honest.

## Consequences

- A producer that drops or renames the type, or a field this service acts on, turns CI red here before loads silently stop
  being planned.
- Both producer contracts this service consumes from the yard domain are now pinned; CPTScheduleChanged (process-path-management)
  and PackageManifested (fulfillment-execution) are not and remain follow-ups.
