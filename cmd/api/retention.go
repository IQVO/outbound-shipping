package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
)

// The idempotency-key retention (ADR 0012, addendum 2026-11-22): every
// idempotencyPurgeInterval the purge deletes idempotency_keys rows older than
// ports.IdempotencyTTL, at most idempotencyPurgeBatch per statement so one
// pass never holds a long lock, however far behind it is. Expiry itself is
// enforced at read (the store's Lookup age guard), so a late or failed purge
// changes no answer; it only stops the table from growing. Like the sibling
// services, the interval and the batch are fixed housekeeping constants, not
// configuration: the age is ports.IdempotencyTTL, the rest is not a knob.
const (
	idempotencyPurgeInterval = time.Hour
	idempotencyPurgeBatch    = 500
)

// idempotencyPurger is the part of ports.IdempotencyStore the retention
// loop needs; both stores satisfy it structurally.
type idempotencyPurger interface {
	PurgeExpired(ctx context.Context, limit int) (int, error)
}

// startIdempotencyPurge starts the retention purge worker: one pass at once,
// then one every interval, until stopped. It is a worker like the consumers
// and the outbox relay: always on, stopped and awaited at shutdown (before
// the relay: it owns no Kafka resource and writes nothing the relay drains).
func startIdempotencyPurge(store idempotencyPurger, logger *slog.Logger) *worker {
	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "idempotency-purge", done: make(chan struct{}), stop: stop}
	go func() {
		defer close(w.done)
		logger.Info("idempotency purge running",
			"interval", idempotencyPurgeInterval, "ttl", ports.IdempotencyTTL, "batch", idempotencyPurgeBatch)
		t := time.NewTicker(idempotencyPurgeInterval)
		defer t.Stop()
		for {
			switch n, err := purgeExpiredKeys(ctx, store, idempotencyPurgeBatch); {
			case err != nil && ctx.Err() == nil:
				logger.ErrorContext(ctx, "idempotency purge failed", "deleted", n, "error", err)
			case n > 0:
				logger.InfoContext(ctx, "idempotency purge done", "deleted", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return w
}

// purgeExpiredKeys deletes expired rows batch by batch until a batch comes
// back short (nothing expired is left), a batch fails, or ctx ends; it returns
// how many rows went. A failed pass is retried at the next tick: the purge is
// idempotent and expiry is enforced at read, so a failure costs only disk.
func purgeExpiredKeys(ctx context.Context, store idempotencyPurger, batch int) (int, error) {
	total := 0
	for ctx.Err() == nil {
		n, err := store.PurgeExpired(ctx, batch)
		total += n
		if err != nil || n < batch {
			return total, err
		}
	}
	return total, nil
}
