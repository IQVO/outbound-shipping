package http_test

// The repo-specific half of the replay-class test (the template next to it is verbatim).
//
// The store design here is a PORT: the key row is written by the middleware through
// ports.IdempotencyStore, a 2xx inside the unit of work, a 4xx after the whole unit was
// rolled back (idempotency.go serveFresh / finishRejected). This wiring therefore needs
// no Postgres: the in-memory store and unit of work the BDD suite runs on, and one
// snapshotting probe that stands for the aggregate and outbox rows a command commits.
// The same classes over a real Postgres run in cmd/api
// (e2e_replay_classes_integration_test.go).

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/http"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
)

// writeProbe is the observable write of a command: tagged rows that a rolled-back unit of
// work takes back (memory.Snapshotter), exactly like the memory repositories.
type writeProbe struct {
	mu   sync.Mutex
	rows map[string]int
}

func (p *writeProbe) Snapshot() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	saved := make(map[string]int, len(p.rows))
	for k, v := range p.rows {
		saved[k] = v
	}
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.rows = saved
	}
}

func (p *writeProbe) add(tag string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rows[tag]++
}

func (p *writeProbe) count(tag string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rows[tag]
}

type replayClock struct{}

func (replayClock) Now() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) }

func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	// The store shares the middleware's fixed clock: an answer stamped by it
	// stays inside the TTL (ports.IdempotencyTTL), like the real pair.
	store := memory.NewIdempotencyStoreWithClock(replayClock{})
	probe := &writeProbe{rows: map[string]int{}}
	uow := memory.NewUnitOfWork(probe)
	return replayOps{
		Catalogue: func() []problemEntry {
			var out []problemEntry
			for _, p := range inboundhttp.ProblemTypes() {
				out = append(out, problemEntry{Slug: p.Slug, Status: p.Status, Class: p.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler {
			return inboundhttp.NewIdempotency(store, uow, replayClock{}).Middleware(next)
		},
		Refuse: func(w http.ResponseWriter, r *http.Request, e problemEntry) {
			inboundhttp.Refuse(w, r, e.Slug)
		},
		// A write through the request's context joins the unit of work the middleware
		// opened, the way a use case's own UoW.Do does.
		Write: func(ctx context.Context, tag string) error {
			return uow.Do(ctx, func(context.Context) error {
				probe.add(tag)
				return nil
			})
		},
		Committed: func(_ *testing.T, tag string) int { return probe.count(tag) },
		KeyRows: func(t *testing.T, key string) int {
			t.Helper()
			_, found, err := store.Lookup(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			if found {
				return 1
			}
			return 0
		},
	}
}
