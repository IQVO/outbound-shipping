package postgres

import (
	"context"
	"fmt"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
)

// purgeExpiredKeysSQL deletes one batch of rows the read guard already
// ignores: `created_at <= now() - ttl` is the exact complement of Lookup's
// `created_at > now() - ttl`, over the same ports.IdempotencyTTL argument.
// The rows are picked `ORDER BY created_at LIMIT n FOR UPDATE SKIP LOCKED`,
// so the purge never waits on a row a request holds -- under this store
// family a request saving its answer inside its unit of work holds the row
// lock until the commit, and the purge skips that row -- and the predicate is
// re-checked by the outer DELETE once a row is locked, so a row refreshed
// between the pick and the delete is never deleted. The statement deletes at
// most limit rows in one short transaction, however far behind the purge is.
const purgeExpiredKeysSQL = `DELETE FROM idempotency_keys WHERE key IN (
	SELECT key FROM idempotency_keys WHERE created_at <= now() - $1::interval
	ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED
) AND created_at <= now() - $1::interval`

// PurgeExpired implements ports.IdempotencyStore: up to limit idempotency_keys
// rows older than ports.IdempotencyTTL deleted, oldest first, and how many
// went (ADR 0012, addendum 2026-11-22). It never takes an advisory lock and
// never waits on a row lock (SKIP LOCKED above).
func (s *IdempotencyStore) PurgeExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	tag, err := queryFor(ctx, s.pool).Exec(ctx, purgeExpiredKeysSQL, ports.IdempotencyTTL.String(), limit)
	if err != nil {
		return 0, fmt.Errorf("purge expired idempotency keys: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
