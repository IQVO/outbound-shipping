package main

import (
	"context"
	"errors"
	"testing"
)

// scriptedPurge is a store double whose PurgeExpired plays back scripted
// batches and records every limit it was called with.
type scriptedPurge struct {
	batches []int // results to return, in order; exhausted = 0
	err     error // returned with the FIRST batch when set
	calls   []int
}

func (s *scriptedPurge) PurgeExpired(_ context.Context, limit int) (int, error) {
	s.calls = append(s.calls, limit)
	if s.err != nil && len(s.calls) == 1 {
		return 0, s.err
	}
	if len(s.batches) == 0 {
		return 0, nil
	}
	n := s.batches[0]
	s.batches = s.batches[1:]
	return n, nil
}

// A pass repeats the batch until one comes back short: three batches
// 500, 500, 3 then stop.
func TestPurgeExpiredKeysDrainsUntilAShortBatch(t *testing.T) {
	s := &scriptedPurge{batches: []int{500, 500, 3}}
	total, err := purgeExpiredKeys(context.Background(), s, 500)
	if err != nil || total != 1003 {
		t.Fatalf("purge = %d, %v, want 1003", total, err)
	}
	if len(s.calls) != 3 {
		t.Fatalf("the store was called %d times, want 3 (the short batch ends the pass)", len(s.calls))
	}
}

// A failed batch ends the pass with the rows it already deleted; the next
// tick retries (the purge is idempotent).
func TestPurgeExpiredKeysStopsOnFailureAndKeepsTheTotal(t *testing.T) {
	boom := errors.New("database is gone")
	s := &scriptedPurge{batches: []int{500, 500}, err: boom}
	total, err := purgeExpiredKeys(context.Background(), s, 500)
	if !errors.Is(err, boom) || total != 0 {
		t.Fatalf("purge = %d, %v, want 0 and the error", total, err)
	}
}

// A cancelled context stops the drain between batches.
func TestPurgeExpiredKeysStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scriptedPurge{batches: []int{500, 500, 500}}
	seen := 0
	s.calls = nil
	total := 0
	for ctx.Err() == nil {
		n, err := s.PurgeExpired(ctx, 500)
		total += n
		seen++
		if err != nil || n < 500 {
			break
		}
		if seen == 2 {
			cancel()
		}
	}
	if total != 1000 || seen != 2 {
		t.Fatalf("drained %d rows in %d calls, want the loop to stop with the context", total, seen)
	}
	// The real loop honours the context the same way: a cancelled ctx ends it
	// even with an endless backlog.
	cancel()
	if n, err := purgeExpiredKeys(ctx, s, 500); err != nil || n != 0 {
		t.Fatalf("a cancelled drain = %d, %v, want 0 and no call", n, err)
	}
	if len(s.calls) != seen {
		t.Fatalf("the cancelled drain still called the store: %v", s.calls)
	}
}
