# ADR 0006: The yard-management contract is pinned and tested

## Status

Accepted (2026-11-09). Refines ADR 0002 and ADR 0005 (the yard rows of the consumed catalogue).

## Context

Ship-confirm follows the physical departure (ADR 0002): this service consumes `TrailerDocked`, `DoorWorkCompleted` and
`TrailerDeparted` from `warehouse.yard-management.events`. While yard-management had no contract, those messages were validated
as fixtures against a proposed model. That hid a break: yard-management published `DoorWorkCompleted` and `TrailerDeparted`
only on its analytics topic, so a closed load could never be confirmed. No test crossed the two services.

## Decision

- `contracts/yard-management.asyncapi.yaml` is a copy of yard-management's `apis/asyncapi.yaml` on `develop` (which, since its
  ADR 0010, declares all three types on the integration channel). `contracts/pinned.json` lists it.
- `scripts/harness/pinned_contract_gate.py` (CI job `pinned-contracts`, not required) fails when the copy differs from the
  producer's `develop`; the fix is to copy the file again and re-run the test below.
- `TestConsumedYardTypesMatchTheProducersIntegrationContract` proves, from the pinned copy, that each consumed type is declared
  on the producer's integration channel and that the producer's own example decodes into the struct this service reads, with
  every field it acts on present.
- Fixture events stay in the BDD scenarios; the shape they use is now checked against the pinned contract.

## Consequences

- A producer that moves a consumed type off its integration topic turns the gate red here before the consumer silently stops
  receiving it.
- The dock-scheduling row is closed by ADR 0007.
