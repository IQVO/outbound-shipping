//go:build integration

package main

// The idempotency-key retention (ADR 0012, addendum 2026-11-22): a stored
// answer must stop answering once it is older than ports.IdempotencyTTL, and
// the dead rows must be purged. These tests run against a real Postgres
// (internal/testing/pgtest) with the real Idempotency-Key middleware over the
// Postgres store, and -- for the replay-expiry case -- the real router with
// the real StagePackage command: an expired key EXECUTES again instead of
// answering, the purge deletes only expired rows in bounded batches, and a
// purge racing a request that holds a key row neither blocks nor breaks it.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/http"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/postgres"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/system"
	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

// retentionRig is one fresh Postgres plus a counting 200 handler behind the
// REAL Idempotency-Key middleware (the Postgres store and the service's unit
// of work), so a test can tell an execution from a replay and speak to the
// table directly (seed, age, count). The pgx pool stays inside the
// constructor: this file names no pgx type (the testcontainers rule,
// TestPostgresIntegrationTestsUseTestcontainers).
type retentionRig struct {
	t     *testing.T
	store ports.IdempotencyStore

	router http.Handler
	calls  atomic.Int32

	exec       func(query string, args ...any)
	scanCount  func(query string, args ...any) int
	keyRows    func() []string
	withHeldTx func(key string, while func() error) error
}

func newRetentionRig(t *testing.T) *retentionRig {
	t.Helper()
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	idem := inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, system.Clock{})
	r := &retentionRig{
		t:     t,
		store: postgres.NewIdempotencyStore(pool),
	}
	r.router = idem.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	r.exec = func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("exec %s: %v", query, err)
		}
	}
	r.scanCount = func(query string, args ...any) int {
		t.Helper()
		var n int
		pgtest.Scan(t, pool, query, args, &n)
		return n
	}
	r.keyRows = func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT key FROM idempotency_keys ORDER BY key`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var keys []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			keys = append(keys, k)
		}
		return keys
	}
	// withHeldTx stands for a request holding the key row inside its unit of
	// work: the store's upsert over the expired row runs in an OPEN
	// transaction (the row lock lives until the commit) while the caller's
	// function runs, then commits.
	r.withHeldTx = func(key string, while func() error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (key, fingerprint, status, content_type, body, created_at)
			VALUES ($1, 'fp2', 200, 'application/json', '\x7b7d', now())
			ON CONFLICT (key) DO UPDATE SET fingerprint = 'fp2', status = 200, content_type = 'application/json', body = '\x7b7d', created_at = now()
			WHERE idempotency_keys.created_at <= now() - $2::interval`, key, ports.IdempotencyTTL.String()); err != nil {
			t.Fatal(err)
		}
		if err := while(); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	return r
}

func (r *retentionRig) post(path, key string) answer {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"n":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	r.router.ServeHTTP(rec, req)
	return answer{
		status:   rec.Code,
		body:     rec.Body.String(),
		replayed: rec.Header().Get(inboundhttp.IdempotencyReplayedHeader) == "true",
	}
}

// seedKey stores a key row that was created age ago.
func (r *retentionRig) seedKey(key string, age time.Duration) {
	r.t.Helper()
	r.exec(`INSERT INTO idempotency_keys (key, fingerprint, status, content_type, body, created_at)
		VALUES ($1, 'fp', 200, 'application/json', '\x7b7d', now() - $2::interval)`, key, age.String())
}

// ageKey moves an existing key row age into the past.
func (r *retentionRig) ageKey(key string, age time.Duration) {
	r.t.Helper()
	r.exec(`UPDATE idempotency_keys SET created_at = now() - $2::interval WHERE key = $1`, key, age.String())
}

// keyCount is how many rows idempotency_keys holds for key.
func (r *retentionRig) keyCount(key string) int {
	r.t.Helper()
	return r.scanCount(`SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

// The purge deletes the expired rows and keeps the live ones; a second purge
// finds nothing.
func TestRetentionPurgeDeletesExpiredKeysAndKeepsLiveOnes(t *testing.T) {
	r := newRetentionRig(t)
	r.seedKey("expired-a", ports.IdempotencyTTL+time.Hour)
	r.seedKey("expired-b", 48*time.Hour)
	r.seedKey("live-a", time.Hour)
	r.seedKey("live-b", ports.IdempotencyTTL-time.Hour)

	n, err := r.store.PurgeExpired(context.Background(), 100)
	if err != nil || n != 2 {
		t.Fatalf("purge = %d, %v, want 2 expired rows deleted", n, err)
	}
	if got := r.keyRows(); len(got) != 2 || got[0] != "live-a" || got[1] != "live-b" {
		t.Fatalf("rows left = %v, want only the live ones", got)
	}
	if n, err := r.store.PurgeExpired(context.Background(), 100); err != nil || n != 0 {
		t.Fatalf("a second purge = %d, %v, want 0: nothing live is ever deleted", n, err)
	}
}

// One purge call deletes at most one batch; repeated calls drain the backlog.
func TestRetentionPurgeDeletesAtMostOneBatchPerCall(t *testing.T) {
	r := newRetentionRig(t)
	for i := range 5 {
		r.seedKey(fmt.Sprintf("old-%d", i), 30*time.Hour)
	}
	r.seedKey("live", time.Minute)

	if n, err := r.store.PurgeExpired(context.Background(), 2); err != nil || n != 2 {
		t.Fatalf("first batch = %d, %v, want 2", n, err)
	}
	var total int
	for {
		n, err := r.store.PurgeExpired(context.Background(), 2)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		total += n
	}
	if got := r.keyRows(); total != 3 || len(got) != 1 || got[0] != "live" {
		t.Fatalf("drained %d more rows, left %v, want 3 more and only the live key", total, got)
	}
}

// A key whose stored answer is older than the TTL EXECUTES again (the age
// guard on the replay read): a fresh answer is stored in place of the expired
// row, and only that one replays afterwards.
func TestRetentionExpiredKeyExecutesInsteadOfReplaying(t *testing.T) {
	r := newRetentionRig(t)
	const path, key = "/retention/probe", "probe-expired-req-01"

	first := r.post(path, key)
	if first.status != http.StatusOK || first.replayed {
		t.Fatalf("the first request = %d replayed=%v, want a fresh 200", first.status, first.replayed)
	}
	r.ageKey(key, ports.IdempotencyTTL+time.Minute)

	again := r.post(path, key)
	if again.status != http.StatusOK || again.replayed || again.body != first.body {
		t.Fatalf("a replay past the TTL = %d replayed=%v, want the request to EXECUTE again (a fresh 200, no replay header)", again.status, again.replayed)
	}
	if got := r.calls.Load(); got != 2 {
		t.Fatalf("the handler ran %d times, want 2: an expired answer must never be served", got)
	}
	if got := r.keyCount(key); got != 1 {
		t.Fatalf("key rows after the re-execution = %d, want 1: the fresh answer replaced the expired row", got)
	}

	live := r.post(path, key)
	if live.status != http.StatusOK || !live.replayed {
		t.Fatalf("a replay of the now-live answer = %d replayed=%v, want the stored 200 replayed", live.status, live.replayed)
	}
	if got := r.calls.Load(); got != 2 {
		t.Fatalf("the handler ran %d times after the live replay, want 2", got)
	}
}

// The same expiry through the real router and the real StagePackage command:
// staging again at the same location is a domain no-op 200, so a re-executed
// expired key answers a fresh 200 and only then replays.
func TestRetentionExpiredKeyReExecutesThroughTheRealRouter(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	pkgs := postgres.NewPackageRepo(pool)
	copies := postgres.NewCopies(pool)
	sink := events.NewSink(postgres.NewOutboxRepo(pool))
	clock := system.Clock{}
	if err := copies.SaveZone(ctx, repository.Zone{ZoneID: "zone-stg", SiteCode: "SITE-1", ZoneCode: "STG"}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if err := copies.SaveLocation(ctx, repository.Location{Code: "STG-D04-A", ZoneID: "zone-stg", Role: repository.RoleStaging}); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	p, err := shippingpackage.Expect(shippingpackage.ExpectInput{PackageID: shared.PackageID("pkg-ret-1"), Site: "SITE-1", At: time.Now()})
	if err != nil {
		t.Fatalf("expect pkg-ret-1: %v", err)
	}
	if err := pkgs.Save(ctx, p, 0); err != nil {
		t.Fatalf("save pkg-ret-1: %v", err)
	}
	// Age the stored answer inside THIS test's database (pgtest.NewPool hands
	// every call its own): one hour past the TTL.
	agePastTTL := func(key string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - $2::interval WHERE key = $1`, key, (ports.IdempotencyTTL + time.Minute).String()); err != nil {
			t.Fatalf("age %s: %v", key, err)
		}
	}
	srv := &inboundhttp.Server{
		Stage:       &usecases.StagePackage{UoW: uow, Pkgs: pkgs, Layout: copies, Sink: sink, Clock: clock},
		Idempotency: inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, clock).Middleware,
	}
	router := inboundhttp.NewRouter(srv, nil, "")
	post := func(key string) answer {
		req := httptest.NewRequest(http.MethodPost, "/packages/pkg-ret-1/stagings", strings.NewReader(`{"staging_location":"STG-D04-A"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return answer{status: rec.Code, body: rec.Body.String(), replayed: rec.Header().Get(inboundhttp.IdempotencyReplayedHeader) == "true"}
	}

	const key = "stage-pkg-ret-1-req-01"
	first := post(key)
	if first.status != http.StatusOK || first.replayed {
		t.Fatalf("the first stage = %d replayed=%v %s, want a fresh 200", first.status, first.replayed, first.body)
	}
	agePastTTL(key)
	again := post(key)
	if again.status != http.StatusOK || again.replayed {
		t.Fatalf("a stage replay past the TTL = %d replayed=%v, want a fresh 200 (the key executed again)", again.status, again.replayed)
	}
	third := post(key)
	if third.status != http.StatusOK || !third.replayed {
		t.Fatalf("the next replay = %d replayed=%v, want the stored 200 replayed", third.status, third.replayed)
	}
}

// A request holds the key row inside its unit of work (the store's upsert over
// the expired row, the row lock held until the commit): the purge must skip
// the row, never wait for it, and never delete the refreshed row.
func TestRetentionPurgeNeverDeletesARowARequestHoldsAndNeverWaitsForIt(t *testing.T) {
	r := newRetentionRig(t)
	r.seedKey("held", 30*time.Hour)

	err := r.withHeldTx("held", func() error {
		start := time.Now()
		n, err := r.store.PurgeExpired(context.Background(), 100)
		if err != nil || n != 0 {
			return fmt.Errorf("purge while the request holds the row = %d, %v, want 0 and no wait", n, err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			return fmt.Errorf("the purge waited %s on the row the request holds: it must skip it", elapsed)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.store.PurgeExpired(context.Background(), 100); err != nil || n != 0 {
		t.Fatalf("purge after the request committed = %d, %v: the row was refreshed, it is live", n, err)
	}
	if got := r.keyRows(); len(got) != 1 || got[0] != "held" {
		t.Fatalf("rows = %v, want the refreshed key", got)
	}
}

// The real middleware on keys whose rows have expired, while the purge runs at
// the same time: an expired row never answers (every response is the probe's,
// never the seeded empty answer), every request ends 200, and each key keeps
// exactly one live row. The handler may run once per concurrent duplicate --
// this store has no key lock (ADR 0012: two requests with one new key both
// run) -- but never because an expired answer was served.
func TestRetentionPurgeRacingARequestOnAnExpiredKeyKeepsTheRequestCorrect(t *testing.T) {
	r := newRetentionRig(t)
	const rounds = 25
	for i := range rounds {
		key := fmt.Sprintf("race-%d", i)
		r.seedKey(key, 30*time.Hour)
		var wg sync.WaitGroup
		got := make([]answer, 3)
		for j := range got {
			wg.Add(1)
			go func() { defer wg.Done(); got[j] = r.post("/retention/probe", key) }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.store.PurgeExpired(context.Background(), 100) }()
		wg.Wait()
		for _, a := range got {
			if a.status != http.StatusOK {
				t.Fatalf("round %d: a request racing the purge answered %d, want 200", i, a.status)
			}
			// The seeded expired answer decodes to an EMPTY stored answer:
			// any response that is not the probe's own body served it.
			if a.body != `{"ok":true}` {
				t.Fatalf("round %d: the response %q is not the probe's answer: the expired stored answer was served", i, a.body)
			}
		}
		if n := r.keyCount(key); n != 1 {
			t.Fatalf("round %d: %d key rows left, want one live row", i, n)
		}
	}
	if got := r.calls.Load(); got < rounds || got > 3*rounds {
		t.Fatalf("the handler ran %d times for %d keys: once per request that executed, never from a replay", got, rounds)
	}
	if again := r.post("/retention/probe", "race-0"); !again.replayed {
		t.Fatalf("a repeat of a stored key replays: %d %v", again.status, again.replayed)
	}
}
