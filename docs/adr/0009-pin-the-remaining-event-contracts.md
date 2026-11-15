# ADR 0009: The remaining four event contracts are pinned (E1–E4)

## Status

Accepted (2026-11-15). Completes ADR 0006/0007 (the yard and dock pins).

## Context

This service consumes five sibling event streams; until now only yard-management's and dock-scheduling's
AsyncAPI copies were pinned (ADR 0006, 0007). facility-layout (`warehouse.facility.events`),
fulfillment-execution (`warehouse.fulfillment.events`) and process-path-management
(`warehouse.process-path-management.events`) were restated by hand in the consumer structs with no drift alarm,
and this repo's own `apis/asyncapi.yaml` — the contract its own analytics projector depends on — was never
pinned against itself. The fleet contract wave (warehouse-docs standard) requires every consumer edge pinned
with the E1–E4 archetypes.

The pinned specs are not uniform: facility-layout and fulfillment-execution document their own topics under
`subscribe:` (channel-perspective), process-path-management under `publish:` (app-perspective), and
yard-management keys its channel by name (`yardManagementEvents`). E1 therefore derives the subscriptions from
this repo's reader config (the topic constants) and reads a channel's message catalogue as the union of
subscribe:, publish: and a direct messages map.

## Decision

- `contracts/{facility-layout,fulfillment-execution,process-path-management}.asyncapi.yaml` are byte-copies of
  the producers' `apis/asyncapi.yaml` on `develop`, listed in `contracts/pinned.json`.
- `contracts/outbound-shipping.asyncapi.yaml` pins this repo's OWN `apis/asyncapi.yaml` (the self-pin: the
  projector's only feed is this service's own analytics stream, so the repo is its own producer for it).
- The archetype tests live beside the consumers in `internal/adapters/inbound/kafka/`:
  - E1 `TestPinnedAsyncAPICoversSubscriptions` — every reader topic constant and every dispatched type is
    documented exactly once on the pinned channel; `TestChannelMessagesReadsEveryLayout` keeps the union
    lookup honest for all three spec layouts.
  - E2 — `fulfillment_contract_test.go`, `cpt_layout_contract_test.go`, `selfpin_contract_test.go`: the
    producers' own golden examples decode through the real decoder and apply through the REAL consumers over
    memory ports (a correlated `PackageManifested` plans exactly one load, id-deduped on replay; a versionless
    `CPTScheduleChanged` stores a storable version; both layout goldens land placeable copies).
  - E3 `TestCloudEventsEnvelopeInvariants` — every consumed type's golden envelope carries specversion 1.0,
    id, source, subject, time, `application/json`, and the dataschema the producer's own spec declares
    (resolved from the spec, never built from a formula; single-value enums count as consts).
  - E4 `tolerance_contract_test.go` — unknown `data` keys tolerated at decode and dispatch; unknown types
    skipped without claiming; non-CloudEvents skipped; an unappliable payload surfaces `errPoison` for the
    documented DeadLetterInvalid DLQ path.
- `scripts/harness/pinned_contract_gate.py` and its self-test are refreshed verbatim from shopping-cart's
  `develop` (the kit generation that carries the `_doc` field in `pinned.json`).

## Consequences

- A producer that drops a type, renames a channel or breaks the envelope turns CI red here the moment its
  `develop` moves, before loads silently stop being planned.
- The one adapted clone (the CPT golden seeded under the manifest's site `WH1` — only `site_id`/`subject`
  renamed and a fresh CloudEvents id) is documented in the test; everything else runs the producers' own
  examples verbatim.
- No production code changed; the consumers already tolerated everything the goldens carry.
