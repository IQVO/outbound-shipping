package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
)

// IdempotencyStore is the in-memory ports.IdempotencyStore, with the same
// retention semantics as the Postgres store: a stored answer older than
// ports.IdempotencyTTL is a miss on Lookup and is replaced by the next Save of
// its key (ADR 0012, addendum 2026-11-22).
type IdempotencyStore struct {
	mu    sync.Mutex
	rows  map[string]repository.IdempotencyRecord
	clock ports.Clock // optional; nil = the system clock (tests inject one)
}

// NewIdempotencyStore constructs an empty IdempotencyStore on the system
// clock.
func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{rows: map[string]repository.IdempotencyRecord{}}
}

// NewIdempotencyStoreWithClock constructs an empty IdempotencyStore whose
// "now" is clock, so a test ages rows without sleeping.
func NewIdempotencyStoreWithClock(clock ports.Clock) *IdempotencyStore {
	return &IdempotencyStore{rows: map[string]repository.IdempotencyRecord{}, clock: clock}
}

// now is the store's notion of the present.
func (s *IdempotencyStore) now() time.Time {
	if s.clock != nil {
		return s.clock.Now()
	}
	return time.Now()
}

// live reports whether rec is still inside the TTL at the given now.
func live(rec repository.IdempotencyRecord, now time.Time) bool {
	return now.Sub(rec.CreatedAt) <= ports.IdempotencyTTL
}

// Lookup implements ports.IdempotencyStore: the stored answer while it is
// live; an expired row is a miss, so the key executes again.
func (s *IdempotencyStore) Lookup(_ context.Context, key string) (repository.IdempotencyRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.rows[key]
	if !ok || !live(rec, s.now()) {
		return repository.IdempotencyRecord{}, false, nil
	}
	return rec, true, nil
}

// Save implements ports.IdempotencyStore; the first writer of a live key
// wins, and a Save over an expired row replaces it.
func (s *IdempotencyStore) Save(_ context.Context, rec repository.IdempotencyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, exists := s.rows[rec.Key]; !exists || !live(old, s.now()) {
		s.rows[rec.Key] = rec
	}
	return nil
}

// PurgeExpired implements ports.IdempotencyStore: up to limit expired rows
// deleted, oldest first (like the Postgres purge's ORDER BY created_at), and
// how many went. It deletes only rows the read guard already ignores.
func (s *IdempotencyStore) PurgeExpired(_ context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	expired := make([]repository.IdempotencyRecord, 0, len(s.rows))
	for _, rec := range s.rows {
		if !live(rec, now) {
			expired = append(expired, rec)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].CreatedAt.Before(expired[j].CreatedAt) })
	if len(expired) > limit {
		expired = expired[:limit]
	}
	for _, rec := range expired {
		delete(s.rows, rec.Key)
	}
	return len(expired), nil
}
