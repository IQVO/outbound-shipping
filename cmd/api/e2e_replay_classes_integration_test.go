//go:build integration

package main

// The replay classes (ADR 0012) over the REAL router, the real Idempotency-Key
// middleware and a real Postgres (internal/testing/pgtest): a refusal that a retry
// could pass leaves NO row under its key, so the retry under the same key executes
// again, is stored once it succeeds and is then replayed; a refusal that is a function
// of the request alone is stored and replayed. The first handler error is the
// optimistic-lock conflict (a concurrent writer bumps the package's version between
// the load and the save, exactly what a second operator does), the second is a fact the
// request names that is not known yet (the package of a scan that arrives before its
// PackageManifested event).

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// bumpOnce is the package repo with one concurrent writer: the first Get after arm()
// is followed by a committed bump of the row's version on another connection, so the
// command's own Save finds a newer version and answers ErrConcurrentModification.
type bumpOnce struct {
	ports.PackageRepo
	bump  func(id shared.PackageID)
	armed atomic.Bool
}

func (b *bumpOnce) Get(ctx context.Context, id shared.PackageID) (*shippingpackage.ShippingPackage, error) {
	p, err := b.PackageRepo.Get(ctx, id)
	if err == nil && b.armed.CompareAndSwap(true, false) {
		b.bump(id)
	}
	return p, err
}

// replayRig is the stage and sweep commands behind the real Idempotency-Key middleware
// over one fresh Postgres database.
type replayRig struct {
	t      *testing.T
	url    string
	count  func(query string, args ...any) int
	seed   func(id string)
	arm    func()
	router http.Handler
}

func newReplayRig(t *testing.T) *replayRig {
	t.Helper()
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	pkgs := postgres.NewPackageRepo(pool)
	copies := postgres.NewCopies(pool)
	sink := events.NewSink(postgres.NewOutboxRepo(pool))
	clock := system.Clock{}

	bump := &bumpOnce{PackageRepo: pkgs, bump: func(id shared.PackageID) {
		if _, err := pool.Exec(ctx, `UPDATE shipping_packages SET version = version + 1 WHERE package_id = $1`, string(id)); err != nil {
			t.Errorf("bump the version of %s: %v", id, err)
		}
	}}
	idem := inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), uow, clock)
	srv := &inboundhttp.Server{
		Stage:       &usecases.StagePackage{UoW: uow, Pkgs: bump, Layout: copies, Sink: sink, Clock: clock},
		Sweep:       &usecases.SweepMissedLoads{UoW: uow, Pkgs: pkgs, Sink: sink, Clock: clock},
		Idempotency: idem.Middleware,
	}
	if err := copies.SaveZone(ctx, repository.Zone{ZoneID: "zone-stg", SiteCode: "SITE-1", ZoneCode: "STG"}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
	if err := copies.SaveLocation(ctx, repository.Location{Code: "STG-D04-A", ZoneID: "zone-stg", Role: repository.RoleStaging}); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	return &replayRig{
		t: t,
		count: func(query string, args ...any) int {
			var n int
			pgtest.Scan(t, pool, query, args, &n)
			return n
		},
		seed: func(id string) {
			p, err := shippingpackage.Expect(shippingpackage.ExpectInput{
				PackageID: shared.PackageID(id), Site: "SITE-1", At: time.Now(),
			})
			if err != nil {
				t.Fatalf("expect %s: %v", id, err)
			}
			if err := pkgs.Save(ctx, p, 0); err != nil {
				t.Fatalf("save %s: %v", id, err)
			}
		},
		arm:    func() { bump.armed.Store(true) },
		router: inboundhttp.NewRouter(srv, nil, ""),
	}
}

// keyRows is how many rows idempotency_keys holds for key.
func (r *replayRig) keyRows(key string) int {
	r.t.Helper()
	return r.count(`SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

type answer struct {
	status   int
	body     string
	replayed bool
}

func (r *replayRig) post(path, key, body string) answer {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	r.router.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return answer{status: rec.Code, body: string(b), replayed: rec.Header().Get(inboundhttp.IdempotencyReplayedHeader) == "true"}
}

const stageBody = `{"staging_location":"STG-D04-A"}`

// A lost optimistic lock is RETRYABLE: nothing committed, the loser's key row is absent,
// the retry under the same key executes (and succeeds), and only then is the answer
// stored and replayed.
func TestConcurrentModificationReleasesTheKeySoTheRetryExecutes(t *testing.T) {
	rig := newReplayRig(t)
	rig.seed("pkg-lock-1")
	rig.arm()
	const path, key = "/packages/pkg-lock-1/stagings", "stage-pkg-lock-1-req-01"

	lost := rig.post(path, key, stageBody)
	if lost.status != http.StatusConflict || !strings.Contains(lost.body, `"code":"concurrent-modification"`) {
		t.Fatalf("the loser = %d %s, want 409 concurrent-modification", lost.status, lost.body)
	}
	if got := rig.keyRows(key); got != 0 {
		t.Fatalf("key rows after the lost lock = %d, want 0: a RETRYABLE refusal is not stored", got)
	}

	retry := rig.post(path, key, stageBody)
	if retry.status != http.StatusOK || retry.replayed {
		t.Fatalf("the retry = %d replayed=%v %s, want a fresh 200 (the key was released)", retry.status, retry.replayed, retry.body)
	}
	if got := rig.keyRows(key); got != 1 {
		t.Fatalf("key rows after the retry = %d, want 1: a 2xx is stored", got)
	}
	again := rig.post(path, key, stageBody)
	if again.status != http.StatusOK || !again.replayed || again.body != retry.body {
		t.Fatalf("the third request = %d replayed=%v, want the stored 200 replayed", again.status, again.replayed)
	}
}

// A fact the request names that is not known yet (the package arrives with its
// PackageManifested event) is RETRYABLE: the same key succeeds once the fact is known,
// and a different body under the released key is a new request, never a key reuse.
func TestAFactNotKnownYetReleasesTheKey(t *testing.T) {
	rig := newReplayRig(t)
	const path, key = "/packages/pkg-late-1/stagings", "stage-pkg-late-1-req-01"

	missing := rig.post(path, key, stageBody)
	if missing.status != http.StatusNotFound || !strings.Contains(missing.body, `"code":"package-not-found"`) {
		t.Fatalf("before the manifest = %d %s, want 404 package-not-found", missing.status, missing.body)
	}
	if got := rig.keyRows(key); got != 0 {
		t.Fatalf("key rows after the 404 = %d, want 0", got)
	}
	other := rig.post(path, key, `{"staging_location":"STG-D04-B"}`)
	if other.status != http.StatusNotFound || strings.Contains(other.body, "idempotency-key-reused") {
		t.Fatalf("another body under the released key = %d %s, want a new request (404 again), not idempotency-key-reused", other.status, other.body)
	}

	rig.seed("pkg-late-1")
	retry := rig.post(path, key, stageBody)
	if retry.status != http.StatusOK || retry.replayed {
		t.Fatalf("the retry after the manifest = %d replayed=%v %s, want a fresh 200", retry.status, retry.replayed, retry.body)
	}
	if got := rig.keyRows(key); got != 1 {
		t.Fatalf("key rows after the success = %d, want 1", got)
	}
}

// A refusal that is a function of the request alone is FINAL: stored once, replayed with
// the header, and the same key with another body is the reuse conflict.
func TestAFinalRefusalIsStoredAndReplayed(t *testing.T) {
	rig := newReplayRig(t)
	const path, key = "/missed-load-sweeps", "sweep-bad-request-req-01"

	first := rig.post(path, key, `{}`)
	if first.status != http.StatusBadRequest || !strings.Contains(first.body, `"code":"bad-request"`) {
		t.Fatalf("a sweep without a site = %d %s, want 400 bad-request", first.status, first.body)
	}
	if got := rig.keyRows(key); got != 1 {
		t.Fatalf("key rows after a FINAL refusal = %d, want 1", got)
	}
	again := rig.post(path, key, `{}`)
	if again.status != first.status || again.body != first.body || !again.replayed {
		t.Fatalf("replay = %d replayed=%v %s, want the stored 400 with Idempotency-Replayed", again.status, again.replayed, again.body)
	}
	reused := rig.post(path, key, `{"site_id":"SITE-1"}`)
	if reused.status != http.StatusConflict || !strings.Contains(reused.body, "idempotency-key-reused") {
		t.Fatalf("another body under a stored key = %d %s, want 409 idempotency-key-reused", reused.status, reused.body)
	}
}
