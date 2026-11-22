package memory

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
)

// fakeClock is a fixed ports.Clock a test moves by hand.
type fakeClock struct{ at time.Time }

func (c *fakeClock) Now() time.Time { return c.at }

// retentionStore is a store on the fake clock plus a helper to save a record
// created at a given time.
func retentionStore(t *testing.T) (*IdempotencyStore, *fakeClock) {
	t.Helper()
	c := &fakeClock{at: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	return NewIdempotencyStoreWithClock(c), c
}

func savedAt(key string, age time.Duration, now time.Time) repository.IdempotencyRecord {
	return repository.IdempotencyRecord{Key: key, Fingerprint: "fp-" + key, Status: 200, CreatedAt: now.Add(-age)}
}

// A stored answer older than the TTL is a miss; one inside it is a hit.
func TestLookupIgnoresExpiredAnswers(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	for _, rec := range []repository.IdempotencyRecord{
		savedAt("expired", ports.IdempotencyTTL+time.Minute, clock.at),
		savedAt("boundary", ports.IdempotencyTTL, clock.at),
		savedAt("live", ports.IdempotencyTTL-time.Minute, clock.at),
	} {
		if err := s.Save(ctx, rec); err != nil {
			t.Fatalf("save %s: %v", rec.Key, err)
		}
	}
	if _, found, _ := s.Lookup(ctx, "expired"); found {
		t.Fatal("an answer older than the TTL answered a lookup: it must be a miss")
	}
	if _, found, _ := s.Lookup(ctx, "boundary"); !found {
		t.Fatal("an answer exactly as old as the TTL must still answer (expiry is strictly past it)")
	}
	if rec, found, _ := s.Lookup(ctx, "live"); !found || rec.Fingerprint != "fp-live" {
		t.Fatalf("a live answer = %v, %v, want a hit", rec, found)
	}
	// The expired row is still there until the purge deletes it; only the
	// read ignores it (the Postgres store behaves the same).
	s.mu.Lock()
	_, expiredRow := s.rows["expired"]
	s.mu.Unlock()
	if !expiredRow {
		t.Fatal("the expired row vanished without a purge: Lookup must not delete")
	}
}

// A Save over an expired row replaces it; over a live row it changes nothing.
func TestSaveReplacesOnlyExpiredAnswers(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, savedAt("k", 30*time.Hour, clock.at)); err != nil {
		t.Fatal(err)
	}

	fresh := savedAt("k", 0, clock.at)
	fresh.Fingerprint = "fp-fresh"
	if err := s.Save(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if rec, found, _ := s.Lookup(ctx, "k"); !found || rec.Fingerprint != "fp-fresh" {
		t.Fatalf("after re-saving an expired key = %v, %v, want the fresh answer", rec, found)
	}

	later := savedAt("k", time.Hour, clock.at)
	later.Fingerprint = "fp-later"
	if err := s.Save(ctx, later); err != nil {
		t.Fatal(err)
	}
	if rec, _, _ := s.Lookup(ctx, "k"); rec.Fingerprint != "fp-fresh" {
		t.Fatalf("a Save over a LIVE row replaced it (%q): the first live writer wins", rec.Fingerprint)
	}
}
