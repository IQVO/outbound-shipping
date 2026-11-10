# CLAUDE.md — outbound-shipping

`outbound-shipping` is a Supporting bounded context (`wms` tier, CloudEvents
subdomain `wms`, ADR 0001) of the `warehouse-systems` fleet (GitHub org
`IQVO`, Go module `github.com/claudioed/outbound-shipping`). It gets every
manifested package onto the right load before its cutoff, proves what left on
which trailer, and tells the fleet that an order has actually shipped. It owns
the `OutboundLoad`, `ShippingPackage` and `BillOfLading` aggregates. It does
no transportation management (rating, tendering), no pallets or master BOL,
and no automated loading (ADR 0001). Two binaries: `cmd/api` (REST `:8080`,
six Kafka consumers, the in-process outbox relay) and `cmd/mcp` (read-only MCP
`:8090`, needs only `DATABASE_URL`). Status: built on `develop`, not deployed:
there is no Helm chart and no warehouse-infra entry yet
(`docs/docs/operations/before-first-release.md`).

## Hard rules (each one is in an ADR or fitness-tested)

1. **Hexagonal dependency rule.** `internal/domain` depends on nothing internal
   but domain; `internal/application` only on domain and application; inbound
   and outbound adapters never depend on each other; only `cmd/` wires layers.
   `make arch-test` (`TestHexagonalArchitecture`) enforces it.
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message produced or
   consumed (structured mode, no flat envelope, no envelope toggle). Build and
   decode ONLY through `internal/adapters/kafka/cloudevents/`; `type` is
   `com.warehouse.wms.outbound-shipping.<entity>.<EventName>` (`<entity>` one
   of `outboundload`, `shippingpackage`, `billoflading`), `source`
   `/warehouse/outbound-shipping`, key = aggregate id. Every type goes to BOTH
   `warehouse.outbound-shipping.events` and `warehouse.outbound-shipping.analytics`
   under one `id`. Every payload carries the aggregate `version` after the
   change. A breaking payload change is a new `.v2` type. Catalogue:
   `docs/adr/0005-cloudevents-envelope-and-type-catalogue.md`; payloads
   `apis/asyncapi.yaml` (`TestCloudEventsOnly`, `TestEventCatalogueMatchesContract`).
3. **Events leave through the transactional outbox only**: the aggregate save
   and its outbox row commit together; the relay in `cmd/api` is the only path
   out (`TestOutboxRelaysPassAttemptCap`). Never write to Kafka from a handler
   or use case.
4. **Ship-confirm follows the physical departure** (ADR 0002).
   `ShipmentConfirmed` is raised only when yard-management's `TrailerDeparted`
   arrives for a CLOSED load; a departure that finds the load LOADING never
   confirms: it raises `PackageMissedLoad` for every package not loaded and
   leaves the load to a supervisor. The domain never invents trailer, seal or
   departure time.
5. **Package-to-order correlation comes only from `PackageManifested`**
   (`source_order_id`, `cpt`, `weight_g`, ADR 0003). Never parse an id to
   derive an order or site.
6. **No live lookup into a sibling context.** Cutoffs, staging locations, dock
   appointments and yard facts are event-fed local copies. There is no HTTP
   client to another service; no `retailnetwork.*` topic is consumed or
   produced (network-fulfillment is the only channel to the network, ADR 0004).
7. **The yard and dock contracts are pinned** (ADR 0006, 0007): `contracts/yard-management.asyncapi.yaml`
   and `contracts/dock-scheduling.asyncapi.yaml` are copies of the producers' `develop` specs listed in `contracts/pinned.json`;
   the `pinned-contracts` job fails when it drifts. Re-copy it, never edit it.
8. **Consumers**: group ids only from env vars (`FULFILLMENT_`, `DOCK_`,
   `YARD_`, `CPT_`, `LAYOUT_`, `ANALYTICS_CONSUMER_GROUP`;
   `TestKafkaConsumerGroupNeverHardcodedInline`); dedupe on the CloudEvents
   `id`; poison goes to `<topic>.dlq` with a cancellation guard
   (`TestDeadLetterCallsGuardCancellation`).
9. **Every POST is behind the `Idempotency-Key` middleware**; errors are RFC
   7807 problem documents with a stable `code`.
10. **Real Postgres and Kafka only.** `DATABASE_URL` and `KAFKA_BROKERS` are
    mandatory for `cmd/api`; `internal/adapters/outbound/memory` is a test
    double no binary imports (`TestRuntimeBinaryHasNoInMemoryAdapters`).
11. **No auth layer on REST or MCP** (fleet rule): never add bearer/JWT/API-key
    middleware (`TestNoAuthMiddlewareReintroduced`). MCP is read-only: every
    tool is L1 (`TestEveryMCPToolDeclaresAuthority`).
12. **An ADR comes first** for a new cross-context contract, a new consumed or
    published type, a write over MCP, or any auth. ADRs live in `docs/adr/`.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test (lefthook pre-push)
make check-all    # check + coverage (89% gate on domain + application) + arch-test + bdd
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins on the domain packages (CI job mutation-fast, blocking)
make guide-lint   # these guides: skills load, references resolve, context budget
make harness-test # unit tests of the agent hooks and red-issue loop
```

Run locally: `DATABASE_URL=... KAFKA_BROKERS=... <the six *_CONSUMER_GROUP
vars> go run ./cmd/api`; `DATABASE_URL=... go run ./cmd/mcp`. Every variable:
`docs/docs/operations/configuration.md`.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md`: language, aggregates, invariants, use
  cases. `.claude/rules/rest-api.md`: routes and conventions.
  `.claude/rules/integration-events.md`: published and consumed events.
- `.claude/rules/fleet/*.md`: fleet-wide rules copied from the harness
  template; never edit them here.
- `docs/adr/`: decisions (index `docs/adr/index.md`). Contracts:
  `apis/openapi.yaml`, `apis/asyncapi.yaml`, pinned producer copies in
  `contracts/`. Operating docs: `docs/docs/operations/` (configuration,
  runbook, observability, troubleshooting), `docs/docs/development/testing.md`,
  `docs/docs/overview/quickstart.md`.
- `HARNESS.md`: what each sensor runs (no `helm-lint`: there is no chart).
  `INCIDENTS.md`: fleet incident log. `.claude/memory/MEMORY.md`: repo
  heuristics.

harness-template: v4
(hooks, guide-lint).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/domain/**`, `internal/application/**`, `features/**` | `.claude/rules/domain-model.md` |
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/events/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
