---
id: testing
title: Testing
sidebar_label: Testing
description: "The outbound-shipping test pyramid as it is: unit, godog features, integration, contract, mutation and fitness tests, the make targets and the CI jobs."
---

# Testing

Source: `Makefile`, `.github/workflows/ci.yml`, `.gremlins.yaml`, `features/`, `internal/architecture/`, `internal/**/*_test.go`, `contracts/`. Counts below are `func Test...` declarations and Gherkin scenarios counted in the tree on `develop`, not coverage claims.

## The pyramid as it is

| Layer | Where | Count | Needs | Notes |
|---|---|---|---|---|
| Domain unit | `internal/domain/{outboundload,shippingpackage,billoflading,shared}` | 70 test functions | nothing | Invariants, transitions, rehydration validation, short-ship validity. Pure Go, no I/O. |
| Use-case unit | `internal/application/usecases/*_test.go` | 43 | nothing | The consumer policies and REST commands over the in-memory doubles (`internal/adapters/outbound/memory`): expect, scan, load commands, on-departure, edges, projector, queries. |
| Adapter unit | `internal/adapters/kafka/cloudevents`, `internal/adapters/outbound/kafka` | 8 | nothing | CloudEvents build and decode, writer settings. |
| BDD (godog) | `features/ship_confirm_on_departure.feature`, runner `features/features_test.go` (`TestFeatures`) | 1 feature file, **6 scenarios** | nothing | Drives the same use cases `cmd/api` wires, over the in-memory doubles, with an outbox recorder standing in for the relay. Strict mode. |
| Contract | `internal/adapters/inbound/kafka/producer_contract_test.go`, `contracts/` | 2 | nothing | See below. |
| Integration | `internal/adapters/outbound/postgres/service_integration_test.go` (`//go:build integration`) | 3 (one is `TestMain`) | Docker | Real Postgres through testcontainers (`internal/testing/pgtest`): `TestExpectPackagePlansExactlyOneLoadUnderConcurrency` (the one-open-load race backstop) and `TestServicePathOverPostgres` (a full path through the real adapters). |
| Fitness (architecture) | `internal/architecture/` | 12 files, 37 test functions | nothing | See below. |
| Mutation | `.gremlins.yaml` | n/a | `gremlins` v0.6.0 | See below. |

There are **no handler tests** for the REST adapter (`internal/adapters/inbound/http`) and **none for the MCP adapter** (`internal/adapters/inbound/mcp`); both are covered only indirectly (architecture sensors, the BDD feature through the use cases). The Kafka consumers' decode paths are likewise exercised mostly through the use-case tests and the yard contract test. There is no end-to-end test of the Kafka consumers against a live broker in this repository (`internal/testing/kafkatest` exists as a helper; no test in the tree uses it).

### The BDD scenarios

All six are in `features/ship_confirm_on_departure.feature`: manifest, stage, load, close and depart with one bill of lading; a departure while `LOADING` that never confirms and flags what missed; a departure that arrives before the load closes (handled, never confirmed, and a re-delivery changes nothing); a departure delivered twice confirms once; a diverted package short-ships and the load still closes; the missed-load sweep flags once even when it runs twice.

### Contract test: the yard-management pin

`contracts/yard-management.asyncapi.yaml` is a copy of yard-management's `apis/asyncapi.yaml` on `develop`, listed in `contracts/pinned.json`. `TestConsumedYardTypesMatchTheProducersIntegrationContract` proves each consumed yard type (`TrailerDocked`, `DoorWorkCompleted`, `TrailerDeparted`) is declared on the producer's **integration** channel and that the producer's example decodes into the struct this service reads, with every field it acts on present; `TestOnIntegrationChannelRejectsAnalyticsOnlyTypes` proves the detector can fail. The CI job `pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when the pinned copy differs from the producer's `develop` ([ADR 0006](/docs/adr/0006-yard-contract-pinned)). The dock-scheduling contract is **not** pinned the same way: its payload is mirrored by hand in `internal/adapters/inbound/kafka/dock_yard_consumers.go`.

### Fitness tests (`internal/architecture`)

| Test | Guards |
|---|---|
| `TestHexagonalArchitecture` | dependency direction between `domain`, `application` and `adapters` (arch-go). |
| `TestMCPAdapterDependencyRule` | the MCP adapter depends inward on the application layer only. |
| `TestNoAuthMiddlewareReintroduced` | no auth middleware in the REST or MCP routers. |
| `TestRuntimeBinaryHasNoInMemoryAdapters` (+ `TestRuntimeBinarySensorBites`) | `cmd/` never imports the memory adapters. |
| `TestNoGoFilesAtRepoRoot` (+ two sensor tests) | no `.go` file at the repository root. |
| `TestKafkaConsumerGroupNeverHardcodedInline` | group ids come from the environment. |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice` (+ `...SensorBites`) | every writer sets `RequiredAcks`; no Topic on both Writer and Message. |
| `TestDeadLetterCallsGuardCancellation` (+ `...SensorBites`) | a dead-letter call checks for context cancellation first (shutdown is never a dead letter). |
| `TestReplayConsumersSetCommitInterval`, `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope` | CloudEvents 1.0 only, no envelope toggle. |
| `TestEventCatalogueMatchesContract` (+ detector) | every type `apis/asyncapi.yaml` declares for this service is listed in [ADR 0005](/docs/adr/0005-cloudevents-envelope-and-type-catalogue). |
| `TestOutboxRelaysPassAttemptCap` (+ two sensor tests) | the relay is built with `WithMaxAttempts`. |
| `TestMCPOptionalToolInputsCarryOmitempty` (+ sensor test) | optional MCP inputs are not published as required. |
| `TestEveryMCPToolDeclaresAuthority`, `TestMCPWriteToolsAreAtLeastL3`, `TestA2ASkillsDeclareAuthority`, `TestMCPServerOffersResourceAndPrompt` (+ five sensor tests) | agent surface rules: every MCP tool declares an authority level, write tools are L3 or above, the server offers a resource and a prompt. |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers` (+ sensor test) | integration tests boot their own containers and never read `DATABASE_URL`. |

### Mutation testing

`.gremlins.yaml` sets one worker, a timeout coefficient of 30 and thresholds `efficacy: 90` and `mutant-coverage: 90`. The fast gate (`make mutation`, CI `mutation-fast`) runs gremlins on each domain package in turn: `shared`, `outboundload`, `shippingpackage`, `billoflading`. The exhaustive run (`make mutation-full`, CI `mutation`) runs `./internal/domain` and is scheduled weekly (Monday 06:00 UTC), opening a `harness:red` issue when it fails.

### Coverage gate

`make coverage` runs `go test ./... -race` with `-coverpkg=./internal/domain/...,./internal/application/...` and fails below **89 %** (the Makefile notes 89.3 % measured). Adapters are outside that scope.

## Make targets

| Target | Does | Needs |
|---|---|---|
| `make help` | list targets | |
| `make build`, `make vet`, `make fmt`, `make fmt-check` | build, vet, format, format check | |
| `make lint` | `golangci-lint run ./...` (pinned v2.14.0) | golangci-lint |
| `make test` | `go test ./... -race` (unit, godog, fitness; no integration) | |
| `make coverage` | tests plus the 89 % gate | |
| `make bdd` | `go test ./... -run TestFeatures -v` | |
| `make arch-test` | `go test ./internal/architecture/... -v` | |
| `make integration` | `go test -tags=integration ./... -race -count=1` | Docker |
| `make mutation` / `make mutation-full` | gremlins, fast per-package / exhaustive | gremlins |
| `make vuln` | `govulncheck ./...` | govulncheck |
| `make check` | `fmt-check vet build lint test`: the pre-commit loop | |
| `make check-all` | `check` plus `coverage arch-test bdd`: run before pushing | |
| `make check-fast`, `make guide-lint`, `make harness-test` | agent harness: fast gate, guide lint, hook tests | python3 |

`make check` is not enough to run the integration suite; `integration` is deliberately outside both `check` and `check-all`. lefthook runs `fmt-check`, `vet` and `lint` on commit and `make check` on push.

## CI jobs (`.github/workflows/ci.yml`)

Triggers: push and PR on `main` and `develop`, a weekly schedule (mutation), manual dispatch.

| Job | Runs |
|---|---|
| `lint` | golangci-lint |
| `guide-lint` | the harness guide lint and hook tests |
| `pinned-contracts` | pinned-contract gate tests; pins equal the producers' `develop` |
| `complexity` | gocyclo report (gate 15) |
| `file-length` | file length of the Go files a PR touches (blocking), backlog (informational) |
| `test` | build, vet, `make coverage` |
| `bdd` | `go test ./... -run TestFeatures -v` |
| `integration` | `go test -tags=integration ./... -race -count=1` |
| `mutation-fast` | gremlins on every domain package |
| `mutation` | exhaustive gremlins (schedule) |
| `drift` | dead code, `go mod tidy -diff`, coverage-quality (schedule) |
| `api-lint` | spectral over `apis/openapi.yaml` and `apis/asyncapi.yaml` |
| `vuln` | `govulncheck` |
| `chart-tests` | `charts/*/tests` (warns only: there is no chart) |
| `arch-test` | `go test ./internal/architecture/... -v` |
| `docker-publish` | image build, push, signing, SBOM: only on a push to `main`, after the jobs above |
| `release` | version, tag, chart package, GitHub release: only after `docker-publish` |

The documentation site is built by a separate workflow, `.github/workflows/docs.yml` (build on pull request, deploy on push to `main`).
