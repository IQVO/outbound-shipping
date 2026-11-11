---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
description: "Prerequisites, make targets, the environment to boot cmd/api and cmd/mcp, and the first REST calls for outbound-shipping."
---

# Quickstart

This page tells you how to build, test and boot the service and which calls to make first. Every command and request below is read from the `Makefile`, `cmd/`, `apis/openapi.yaml` and `features/*.feature`; none of it was executed to write this page, so no output is shown.

Source: `Makefile`, `cmd/api/config.go`, `cmd/mcp/main.go`, `apis/openapi.yaml`, `features/ship_confirm_on_departure.feature`.

## Prerequisites

| Need | Version / note | Used for |
|---|---|---|
| Go | `1.27.3` (`go.mod`) | build, tests |
| `golangci-lint` | `v2.14.0` (`GOLANGCI_VERSION` in the Makefile) | `make lint`, part of `make check` |
| Docker | any recent engine | **only** `make integration` (testcontainers boots its own Postgres and Kafka per test) |
| `gremlins` | `v0.6.0` | `make mutation`, `make mutation-full` |
| `govulncheck` | latest | `make vuln` |
| PostgreSQL | a reachable instance and a database you can migrate | booting `cmd/api` or `cmd/mcp` |
| Kafka | the platform's ONE broker (k3s cluster) | booting `cmd/api` only |

There is no docker-compose file and no kind setup in this repository: the platform runs one Kafka broker and one k3s cluster, and a local boot points at them. `cmd/api` has no in-memory mode, so it cannot start without both Postgres and a broker address (`TestRuntimeBinaryHasNoInMemoryAdapters` keeps it that way).

## Build and check

```bash
make help          # list every target
make build         # go build ./...
make check         # fmt-check vet build lint test: the fast pre-commit loop
make check-all     # check + coverage + arch-test + bdd: run before pushing
```

The tests that need no infrastructure (unit, httptest, godog features over in-memory doubles, architecture fitness) run with `make test`, `make bdd` and `make arch-test`. See [Testing](/docs/development/testing) for the full pyramid.

## Boot the API

`cmd/api` refuses to start unless six consumer groups, `DATABASE_URL` and `KAFKA_BROKERS` are set. Migrations run at boot, so the database only has to exist.

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/outbound_shipping?sslmode=disable'
export KAFKA_BROKERS='BROKER_HOST:9092'
export FULFILLMENT_CONSUMER_GROUP='outbound-shipping-fulfillment-local'
export DOCK_CONSUMER_GROUP='outbound-shipping-dock-local'
export YARD_CONSUMER_GROUP='outbound-shipping-yard-local'
export CPT_CONSUMER_GROUP='outbound-shipping-cpt-local'
export LAYOUT_CONSUMER_GROUP='outbound-shipping-layout-local'
export ANALYTICS_CONSUMER_GROUP='outbound-shipping-analytics-local'
go run ./cmd/api
```

Use group ids that no deployed instance shares: a fresh group starts at the first offset and replays the whole topic, which is how the local copies (cutoff schedule, staging locations, appointments) converge. `ADDR` defaults to `:8080`. All variables are in [Configuration](/docs/operations/configuration).

The process logs `api listening` as JSON on stdout. `GET /healthz` answers 200 as soon as the listener is up; `GET /readyz` answers 200 only while Postgres answers a ping and a TCP dial of the first broker succeeds, otherwise `503` with a `dependency-unavailable` problem naming the failing dependency.

## Boot the MCP server

`cmd/mcp` needs only Postgres:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/outbound_shipping?sslmode=disable'
go run ./cmd/mcp          # Streamable HTTP on :8090 (MCP_ADDR), also GET /healthz
```

Its two tools and the load resource are described on [MCP tools](/docs/mcp/tools).

## First calls

The service has no route that creates a package or a load: both appear when upstream Kafka events arrive (a `PackageManifested` plans the load and expects the package, see [Integration](/docs/ecosystem/integration)). The REST surface starts after that. Every POST needs an `Idempotency-Key` header (the spec asks for 16 to 128 characters); a request without one is `400 idempotency-key-required`.

Read the loads of a site (identifiers below are the ones the BDD feature uses):

```bash
curl -s 'http://localhost:8080/loads?site_id=SITE-1&state=PLANNED&limit=50'
curl -s 'http://localhost:8080/loads/LOAD_ID'
curl -s 'http://localhost:8080/loads/LOAD_ID/packages?limit=50'
```

Scan a package into staging, then onto the load through the load's door (the door comes from the dock appointment, so a load without an appointment answers `409 wrong-load`):

```bash
curl -s -X POST http://localhost:8080/packages/pkg-1029/stagings \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: stage-pkg-1029-20261106-req-01' \
  -d '{"staging_location":"STG-D04-A"}'

curl -s -X POST http://localhost:8080/packages/pkg-1029/loadings \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: load-pkg-1029-20261106-req-01' \
  -d '{"load_id":"LOAD_ID","door_code":"D04"}'
```

Close the load. `short_ships` is optional; every entry needs a package id and a reason, and any package that is neither loaded nor short-shipped answers `409 packages-outstanding`:

```bash
curl -s -X POST http://localhost:8080/loads/LOAD_ID/closures \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: close-LOAD_ID-20261106-req-01' \
  -d '{"short_ships":[]}'
```

There is no REST route that ship-confirms a load: the confirmation follows the trailer's physical departure (`TrailerDeparted` from yard-management, [ADR 0002](/docs/adr/0002-ship-confirm-follows-the-physical-departure)). After it, the bill of lading is readable:

```bash
curl -s http://localhost:8080/bills-of-lading/BOL_NUMBER
curl -s 'http://localhost:8080/reports/load-adherence?site_id=SITE-1&operating_date=2026-11-06'
```

Flag packages that missed a passed cutoff (what a scheduler runs; `cutoff` is optional RFC 3339, default now):

```bash
curl -s -X POST http://localhost:8080/missed-load-sweeps \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: sweep-SITE-1-20261106-1600-01' \
  -d '{"site_id":"SITE-1"}'
```

The service is **built on `develop`, not deployed**: the Helm chart exists in `charts/outbound-shipping/`, but there is no entry in warehouse-infra (no `apps.yaml` entry, no helm values, no pinned image), so nothing is deployed and no Kong route exists for it yet (see [Runbook](/docs/operations/runbook#helm-chart)). Until then the calls above go straight to the process.

## What to read next

- The full route list and error catalogue: [API overview](/docs/api-reference).
- How the pieces fit: [Architecture](/docs/overview/architecture).
- Something does not start or does not move: [Troubleshooting](/docs/operations/troubleshooting).
