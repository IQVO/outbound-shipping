package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// BoardStore is the pgxpool-backed LoadBoardView: the projector's write side
// (usecases.BoardStoreWriter) and the report/at-risk read side
// (ports.BoardStore), over the load_board table the migrations create.
type BoardStore struct {
	pool *pgxpool.Pool
}

// NewBoardStore constructs a BoardStore over pool.
func NewBoardStore(pool *pgxpool.Pool) *BoardStore { return &BoardStore{pool: pool} }

// ApplyLoadPlanned implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyLoadPlanned(ctx context.Context, load shared.LoadID, site shared.SiteID, cpt shared.CptID, date, method string, cutoff time.Time) error {
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		INSERT INTO load_board
		    (load_id, site_id, cpt_id, operating_date, ship_method, cutoff)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (load_id) DO UPDATE SET
		    site_id = EXCLUDED.site_id, cpt_id = EXCLUDED.cpt_id,
		    operating_date = EXCLUDED.operating_date,
		    ship_method = EXCLUDED.ship_method, cutoff = EXCLUDED.cutoff,
		    updated_at = now()`,
		load, site, cpt, date, method, cutoff)
	return wrapBoard(err, "plan", load)
}

// ApplyCount implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyCount(ctx context.Context, load shared.LoadID, staged, loaded int64) error {
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		UPDATE load_board SET
		    staged_count = staged_count + $2,
		    loaded_count = loaded_count + $3,
		    updated_at = now()
		WHERE load_id = $1`, load, staged, loaded)
	return wrapBoard(err, "count", load)
}

// ApplyMissed implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyMissed(ctx context.Context, load shared.LoadID, miss int64) error {
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		UPDATE load_board SET
		    packages_missed = packages_missed + $2, updated_at = now()
		WHERE load_id = $1`, load, miss)
	return wrapBoard(err, "missed", load)
}

// ApplyClosed implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyClosed(ctx context.Context, load shared.LoadID, closedAt, cutoff time.Time) error {
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		UPDATE load_board SET
		    closed_at = $2, closed_on_time = ($2 <= $3), updated_at = now()
		WHERE load_id = $1`, load, closedAt.UTC(), cutoff.UTC())
	return wrapBoard(err, "closed", load)
}

// ApplyConfirmed implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyConfirmed(ctx context.Context, load shared.LoadID, at time.Time) error {
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		UPDATE load_board SET confirmed_at = $2, updated_at = now()
		WHERE load_id = $1`, load, at.UTC())
	return wrapBoard(err, "confirmed", load)
}

// ApplyCancelled implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyCancelled(ctx context.Context, load shared.LoadID, at time.Time) error {
	_, err := queryFor(ctx, s.pool).Exec(ctx, `
		UPDATE load_board SET cancelled_at = $2, updated_at = now()
		WHERE load_id = $1`, load, at.UTC())
	return wrapBoard(err, "cancelled", load)
}

func wrapBoard(err error, what string, load shared.LoadID) error {
	if err != nil {
		return fmt.Errorf("board %s of %s: %w", what, load, err)
	}
	return nil
}

// Report implements ports.BoardStore: the adherence rows of the scope,
// grouped by (site, operating date, cpt).
func (s *BoardStore) Report(ctx context.Context, site shared.SiteID, date outboundload.OperatingDate) ([]repository.AdherenceRow, error) {
	where := "WHERE TRUE"
	args := []any{}
	if site != "" {
		args = append(args, site)
		where += fmt.Sprintf(" AND site_id = $%d", len(args))
	}
	if date != "" {
		args = append(args, date)
		where += fmt.Sprintf(" AND operating_date = $%d", len(args))
	}
	rows, err := queryFor(ctx, s.pool).Query(ctx, `
		SELECT site_id, cpt_id, operating_date,
		       COUNT(*) FILTER (WHERE closed_at IS NOT NULL) AS loads_closed,
		       COUNT(*) AS loads_total,
		       COALESCE(SUM(packages_missed), 0) AS packages_missed
		FROM load_board `+where+`
		GROUP BY site_id, operating_date, cpt_id
		ORDER BY site_id, operating_date, cpt_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("query adherence report: %w", err)
	}
	defer rows.Close()
	var out []repository.AdherenceRow
	for rows.Next() {
		var r repository.AdherenceRow
		if err := rows.Scan(&r.Site, &r.Cpt, &r.OperatingDate, &r.LoadsClosed,
			&r.LoadsTotal, &r.PackagesMissed); err != nil {
			return nil, fmt.Errorf("scan adherence row: %w", err)
		}
		if r.LoadsTotal > 0 {
			r.Adherence = float64(r.LoadsClosed) / float64(r.LoadsTotal)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RowsAtRisk implements ports.BoardStore: the not-closed, not-cancelled
// loads whose cutoff falls within [now, now+horizon], ordered by cutoff,
// with their outstanding package counts (expected minus staged minus loaded,
// floored at 0).
func (s *BoardStore) RowsAtRisk(ctx context.Context, site shared.SiteID, now time.Time, horizon time.Duration, limit int) ([]repository.LoadBoardRow, error) {
	where := "WHERE closed_at IS NULL AND cancelled_at IS NULL AND cutoff >= $2 AND cutoff <= $3"
	args := []any{now.UTC(), now.UTC().Add(horizon), limit}
	if site != "" {
		args = append(args, site)
		where += fmt.Sprintf(" AND site_id = $%d", len(args))
	}
	rows, err := queryFor(ctx, s.pool).Query(ctx, `
		SELECT load_id, site_id, cpt_id, operating_date, ship_method, cutoff,
		       staged_count, loaded_count, packages_missed
		FROM load_board `+where+`
		ORDER BY cutoff ASC LIMIT $1`, args...)
	if err != nil {
		return nil, fmt.Errorf("query loads at risk: %w", err)
	}
	defer rows.Close()
	var out []repository.LoadBoardRow
	for rows.Next() {
		var r repository.LoadBoardRow
		if err := rows.Scan(&r.LoadID, &r.Site, &r.Cpt, &r.OperatingDate,
			&r.ShipMethod, &r.Cutoff, &r.StagedCount, &r.LoadedCount,
			&r.PackagesMissed); err != nil {
			return nil, fmt.Errorf("scan load at risk: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
