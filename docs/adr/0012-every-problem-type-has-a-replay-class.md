# ADR 0012: Every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-20). Adopts the fleet rule decided by the owner on 2026-11-18 (standard:
warehouse-harness-template `docs/idempotency-replay-classes.md`). Supersedes the sentence of the
`Idempotency-Key` middleware (`internal/adapters/inbound/http/idempotency.go`, `ports.IdempotencyStore`) and of
`apis/openapi.yaml` that said every 4xx answer is stored and replayed; ADR 0010 item 6 (the key bounds) stands.

## Context

`Idempotency.Middleware` stored the first answer below 500 of every key, errors included, and replayed it. A refusal
that a retry could pass was therefore pinned: a `404 package-not-found` replayed to an operator whose package
arrived a second later with its `PackageManifested` event (the scenario the BDD suite pinned as "a rejected
request replays its rejection"), a `409 concurrent-modification` replayed to a client that did the right thing.

The store is a **port with a unit of work around the handler** (read before this change, same lines before and
after): a new key runs the handler inside one `ports.UnitOfWork.Do` (`serveFresh`). A 2xx is saved with its effects
in that same transaction (`saveAndReread`). **A non-2xx answer returns `errRollback`: the WHOLE unit of work rolls
back**, then `finishRejected` stores a 4xx by a separate `Store.Save`, and a 5xx is never stored. Consequences:

- **OUTCOME cannot exist here.** Every use case runs its work in `UoW.Do`, which joins the transaction the
  middleware opened (`postgres/unit_of_work.go` `Do`: `pgtx.From(ctx)` then `fn(ctx)`; the memory unit of work is
  re-entrant the same way). A refusal therefore commits nothing, ever; the middleware discards the writes of
  `CloseLoad.applyShortShips` (`load_commands.go`, package saves) when `ErrPackagesOutstanding` follows. There is no
  state to remember, so no problem type can be OUTCOME. `TestNoProblemTypeIsAnOutcomeInThisStore` fails if one is
  declared and `TestARefusalNeverCommitsTheWritesOfItsCommand` proves, per problem type, that the writes of a command
  that wrote and then refused are not committed.
- **Releasing a RETRYABLE key is skipping the separate write.** The key row is not written on the 4xx path before
  the refusal, so there is nothing to delete. The same key and body then execute again; a different body under the
  key is a new request, not `idempotency-key-reused`.
- A 5xx is rolled back and not stored (unchanged).
- Two concurrent requests with one new key both run (no lock); the store keeps the first 2xx writer's answer
  (`Save` blocks on the unique index, the re-read compares) (unchanged, pre-existing).

## Decision

Every problem type declares exactly one replay class on its catalogue entry: the `class` field of `problem`
(`internal/adapters/inbound/http/errors.go`). A problem cannot be built without one (`newProblem` is the only
constructor; `TestProblemsAreBuiltByTheConstructorOnly` forbids literals) and a test fails on an unclassified
entry. The catalogue keeps its shape (a slice of `{err, problem}` in match order); the class is a new argument
of each problem, the problems the handlers used to spell inline (`bad-request`, `invalid-parameter`,
`unknown-query-parameter`) became the named values `badRequest`, `invalidParameter`, `unknownQueryParam`, and a
fixed detail is `problem.withDetail(...)`. Wire slugs, titles, statuses and bodies are unchanged.

| Class | Meaning | Middleware |
| --- | --- | --- |
| `FINAL` | a function of the request alone (or of a state that only moves one way for it) | stored and replayed |
| `OUTCOME` | the command committed state, then answered an error that must be remembered | stored and replayed (none here) |
| `RETRYABLE` | nothing committed; the answer depends on state that can change | not stored, the key is released, a retry executes |

`writeProblem` (every problem, `writeError` included) marks the middleware's capture with the class
(`replayMarker`); the middleware stores a 2xx, or a 4xx marked FINAL or OUTCOME, and nothing else. A 5xx and a
panic are never stored; a refusal that reaches the middleware without a class is released like RETRYABLE
(`TestARefusalWithNoClassIsReleased`). The middleware holds no slug list (`TestTheMiddlewareHoldsNoListOfSlugs`)
and knows no status range beyond 2xx (stored with its effects) and 5xx (never stored): every non-2xx answer rolls
back. 2xx, 5xx and FINAL answers are byte-identical to before. When unsure between FINAL and RETRYABLE the class
is RETRYABLE.

**A concurrent duplicate when the first ends RETRYABLE.** There is no key lock: the duplicate runs on its own and
answers its own result, exactly as before; nothing was stored for the first, so nothing is replayed to it. If the
other request stored a 2xx (or a FINAL refusal) for the key meanwhile, the loser answers that stored answer with
`Idempotency-Replayed: true` (one read after the rollback, as `finishRejected` always did), and with another
body it is `409 idempotency-key-reused` (`TestARetryableLoserOfARaceAnswersWhatTheWinnerStored`).

### The classes (held equal to the catalogue by `TestTheADRDeclaresTheClassOfEveryProblemType`)

| Problem type | Class | Status | Reason (code) |
| --- | --- | --- | --- |
| `bad-request` | FINAL | 400 | the request alone: `decodeJSON` and the body bound (`response.go`, `idempotency.go`), the site, cutoff, short-ship, cancel and void reasons (`handlers.go`, `load_commands.go`, domain `ErrCancelReasonRequired`, `ErrVoidReasonRequired`). J2 |
| `invalid-parameter` | FINAL | 400 | a blank path id or a list parameter outside its bounds: the request alone (`handlers.go`) |
| `unknown-query-parameter` | FINAL | 400 | the query alone; GET routes only, no keyed route answers it |
| `idempotency-key-required` | FINAL | 400 | the header alone; answered before the store, never stored |
| `idempotency-key-reused` | FINAL | 409 | the request against the stored fingerprint; it is the answer of a hit, never stored |
| `not-staging-location` | RETRYABLE | 422 | the layout copy is event-fed (`scan_package.go` `Layout.Place`): a location unknown today is a Staging location once `LocationSlotRegistered` arrives (`commands.go` 74, `ErrNotStagingLocation`) |
| `wrong-load` | RETRYABLE | 409 | the package has no load yet (`scan_package.go` 77-79) or another load or door than the scan names (`commands.go` 112-119); the assignment and the door are fed by events |
| `package-on-another-load` | RETRYABLE | 409 | assignment state (`commands.go` 49-51); consumer path only, no keyed route reaches it. J5 |
| `already-shipped` | FINAL | 409 | SHIPPED is terminal for a package (`commands.go` 106-108, 134-136). J3 |
| `package-state` | RETRYABLE | 409 | the aggregate has not reached the state the command needs (`commands.go` 71, 83, 110, 138) |
| `packages-outstanding` | RETRYABLE | 409 | a package still to load (`load_commands.go` `applyShortShips`, domain `CloseLoad`); packages get loaded and short-shipped |
| `trailer-not-docked` | RETRYABLE | 409 | the dock and door facts come from yard-management and dock-scheduling events (`ErrTrailerNotDocked`, `ErrWrongDoor`); two entries, one slug, one class. J4 |
| `load-not-closed` | RETRYABLE | 409 | ship-confirm needs a CLOSED load; consumer path only. J5 |
| `load-confirmed` | FINAL | 409 | SHIP_CONFIRMED never changes (`load.go` `refuseIfConfirmed`, ADR 0002). J3 |
| `duplicate-open-load` | RETRYABLE | 409 | one open load per cutoff key; consumer path only. J5 |
| `load-state` | RETRYABLE | 409 | the load's state moves (`commands.go` 87, 119, 142, 174, 212, 290) |
| `load-has-packages` | RETRYABLE | 409 | packages assigned by events (`load_commands.go` `CancelLoad`); they can be short-shipped |
| `bol-already-issued` | RETRYABLE | 409 | a bill of lading exists for the load; consumer path only. J5 |
| `bol-already-voided` | FINAL | 409 | VOIDED is terminal and a load has one bill (`bol.go` 116-118, `UNIQUE load_id`). J3 |
| `seal-missing` | RETRYABLE | 409 | the seal is a yard-management fact fed by an event; consumer path only. J5 |
| `load-not-found` | RETRYABLE | 404 | loads are planned by events (`expect_package.go`), never by a request. J1 |
| `package-not-found` | RETRYABLE | 404 | packages arrive with `PackageManifested`; also a close naming a package that is not on the load (`load_commands.go` 88). J1 |
| `bol-not-found` | RETRYABLE | 404 | bills of lading are issued on the departure event. J1 |
| `concurrent-modification` | RETRYABLE | 409 | the optimistic lock was lost (`package_repo.go` `Save`, `load_repo.go`, `copies_repo.go`); nothing committed |
| `internal-error` | RETRYABLE | 500 | a 5xx is never stored |
| `dependency-unavailable` | RETRYABLE | 503 | the `/readyz` answer, no keyed route; a 5xx is never stored |

No problem type is OUTCOME (see Context). Keyed routes: the eight POSTs of `apis/openapi.yaml` (stagings,
loadings, unloadings, closures, reopenings, cancellations, voids, missed-load sweeps). MCP is read-only (CLAUDE.md rule 11,
`TestEveryMCPToolDeclaresAuthority`), there is no A2A adapter, and this service sends no deterministic
`Idempotency-Key` to anyone, so no other surface stores answers.

### Judgement calls

- **J1 the three `*-not-found` types are RETRYABLE** (the most consequential: each was replayed for the stored
  window before). This service mints no package, load or bill of lading id through a request: the ids come from
  `PackageManifested`, the cutoff schedule and the departure event, delivered at-least-once, with no ordering between topics
  (`docs/docs/ecosystem/integration.md`). A scan that beats its manifest is the normal case the BDD suite pinned.
  A 404 on the command's own target is FINAL only when the service mints the ids; here the slug names a fact that an
  event may deliver later.
- **J2 `bad-request` FINAL.** Every cause is the request body or path itself. Two reasons are checked after the
  aggregate's state (`CancelLoad` names `load-state` first, `Void` names `bol-already-voided` first, ADR 0010): a
  stored `bad-request` then pins a body that can never succeed, whatever the state becomes, so nothing stale is
  stored. `ErrRequiredField` and `ErrNoTimestamp` are mapped to it but need a server-side zero value, not a client
  input.
- **J3 `already-shipped`, `load-confirmed`, `bol-already-voided` FINAL.** Each answers a terminal state that never
  changes (as checkout `session-closed` and delivery-promise `promise-closed`): a retry could never pass. Flipping
  one to RETRYABLE costs one more execution of a pure refusal.
- **J4 `trailer-not-docked` RETRYABLE** (two causes: no docked trailer yet and a trailer at another door): both are
  yard facts fed by events, so a retry can pass.
- **J5 `package-on-another-load`, `load-not-closed`, `duplicate-open-load`, `bol-already-issued`, `seal-missing`**
  are raised on the consumer path only (`expect_package.go`, `on_departure.go`) and no keyed route answers them
  today. They are classed by the default (state that can change, unsure) RETRYABLE; the class only matters if a
  route ever answers them.
- **J6 the two `idempotency-key-*` types are FINAL** although the middleware answers them itself and never stores
  them: the class records that they are a function of the request.

### What the tests prove

`TestEveryProblemTypeDeclaresAReplayClass` (red before the field existed: the package did not compile),
`TestEveryProblemTypeIsReplayedOrReExecutedByItsClass` and the rest of the fleet template
(`idempotency_replay_classes_test.go`, wired in `idempotency_replay_classes_wiring_test.go` over the in-memory
store and unit of work, Docker-free) run against the real middleware. Over real Postgres and the real router,
`cmd/api/e2e_replay_classes_integration_test.go`: a lost optimistic lock leaves no key row, the retry under the same
key executes, is then stored and replayed; a package that arrives after the scan lets the same key succeed and
another body under the released key is a new request; a `bad-request` is stored and replayed and another body is
`idempotency-key-reused`.

## Consequences

- A refusal marked RETRYABLE is no longer replayed: a retry under the same key executes again, and a different
  body under that key is a new request. `apis/openapi.yaml`, the REST reference, the runbook and the troubleshooting
  table say so. The 4xx that are still stored are the FINAL ones above.
- Callers driven by people surface a refusal; nothing here adds a retry to any caller. A released key means only
  that whoever sent it may send it again.
- **Retention (open point, not fixed here).** `idempotency_keys` has an index on `created_at` but nothing deletes
  its rows and `Lookup` has no age check, so the stated 24-hour window is not enforced (`processed_events` and
  `outbox_events` grow without a purge too, `docs/docs/ddd/entity-relationship.md`). The service has no background
  sweep pattern to extend; a purge plus an age check on replay is a follow-up PR.
- Follow-ups: split `bad-request` and the `*-not-found` types only if a client needs the distinction; the
  concurrent-duplicate behaviour (both run) is pre-existing and unchanged. The inline `detail` strings the
  handlers used to pass inside their problem literals never reached the wire (`writeProblem` takes its detail from
  its argument, which was empty) and were dropped with the literals; the bodies are byte-identical.

## Addendum 2026-11-22: retention — a 24h TTL on replays and a purge for the dead rows

The Decision text above is unchanged. This addendum closes the retention open point of Consequences
("`idempotency_keys` has an index on `created_at` but nothing deletes its rows and `Lookup` has no age check,
so the stated 24-hour window is not enforced"). Until now NO window was enforced at all: a stored answer
replayed forever and the table grew without bound. `processed_events` and `outbox_events` remain unbounded
(out of scope here, unchanged).

### Decision

1. **One TTL constant.** `ports.IdempotencyTTL` (24h, the fleet default) is the single constant the read
   guard, the store write and the purge share. Introducing it is a behaviour change: a stored answer now
   stops answering 24 hours after it was stored; the key then EXECUTES again (the same key and body is a new
   request, never `idempotency-key-reused`, exactly like a RETRYABLE-released key). The OpenAPI
   `Idempotency-Key` description and the REST reference now say the window.
2. **An age guard on the replay read.** `postgres.IdempotencyStore.Lookup` selects
   `created_at > now() - IdempotencyTTL`; an expired row is a miss, so a row can never answer between expiry
   and the sweep. `Save` keeps the first writer of a LIVE key (the `DO UPDATE ... WHERE expired` matches
   nothing while the stored row is live — two concurrent 2xx duplicates still leave one answer, the winner's)
   and replaces a row older than the TTL: a request that re-executes an expired key stores its fresh answer
   in place. The memory double has the same semantics on an injectable clock (`NewIdempotencyStoreWithClock`;
   the doubles share the middleware's clock, like the real pair).
3. **A bounded background purge, never waiting.** `PurgeExpired(ctx, limit)` on `ports.IdempotencyStore`
   (Postgres and memory) deletes `created_at <= now() - IdempotencyTTL` — the exact complement of the read
   guard — oldest first, at most `limit` rows per statement
   (`ORDER BY created_at LIMIT n FOR UPDATE SKIP LOCKED`, no advisory lock). A worker in `cmd/api`
   (`retention.go`, a `worker` like the consumers and the relay, stopped and awaited at shutdown) purges at
   boot and then hourly, batches of 500, repeating a pass until a short batch; a failed pass is logged at
   ERROR and retried at the next tick. The interval and the batch are fixed housekeeping constants, not
   configuration (the age is the TTL; the rest is not a knob).
4. **It cannot race a request.** Under this store family a request stores its 2xx answer INSIDE its unit of
   work (a FINAL 4xx by a separate write), taking the row lock until the commit: the purge's `SKIP LOCKED`
   skips that row, never waits for it, and the outer `DELETE` re-checks the predicate once a row is locked,
   so a row refreshed between pick and delete survives. A row the purge deletes before a request's Save
   merely turns that Save into an INSERT. Expiry is enforced at read regardless: a late or failed purge
   changes no answer, it only costs disk.

### What the tests prove

`cmd/api/idempotency_retention_integration_test.go` (real Postgres, real middleware — and, for replay
expiry, the real router with the real StagePackage command): an expired key executes instead of answering and
its fresh answer then replays; the purge deletes expired rows and keeps live ones; one call deletes at most
one batch; a purge against a row a request holds deletes nothing and does not wait; a purge racing requests
on expired keys never serves an expired answer and leaves one live row per key. Docker-free:
`internal/adapters/outbound/memory/idempotency_retention_test.go` (the double's guard, replace-on-expired
Save, oldest-first bounded purge) and `cmd/api/retention_test.go` (the drain-until-short-batch loop, failure
and cancellation). The red first: the integration file was pushed alone (PR draft, CI integration job red on
the missing `PurgeExpired`/`IdempotencyTTL`), then the fix.
