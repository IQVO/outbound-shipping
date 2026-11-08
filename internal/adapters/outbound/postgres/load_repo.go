package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// openLoadStates is the WHERE of every open-load query and of the partial
// unique index uq_open_load_per_cutoff.
const openLoadStates = "('PLANNED','LOADING')"

// uqOpenLoadConstraint is the partial unique index the one-open-load race
// backstop fires (PLAN wave 5).
const uqOpenLoadConstraint = "uq_open_load_per_cutoff"

// isUniqueViolation reports whether err is a 23505 on the named constraint.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// LoadRepo is the pgxpool-backed ports.LoadRepo.
type LoadRepo struct {
	pool *pgxpool.Pool
}

// NewLoadRepo constructs a LoadRepo over pool.
func NewLoadRepo(pool *pgxpool.Pool) *LoadRepo { return &LoadRepo{pool: pool} }

// Get implements ports.LoadRepo.
func (r *LoadRepo) Get(ctx context.Context, id shared.LoadID) (*outboundload.OutboundLoad, error) {
	return scanLoad(queryFor(ctx, r.pool).QueryRow(ctx, loadCols+`
		FROM outbound_loads WHERE load_id = $1`, id))
}

const loadCols = `
	SELECT load_id, site_id, cpt_id, operating_date, ship_method, cutoff,
	       state, door_code, trailer_id, appointment_id, short_ships,
	       package_count, version`

func scanLoad(row pgx.Row) (*outboundload.OutboundLoad, error) {
	var (
		p         outboundload.RehydrateParams
		shortJSON []byte
	)
	if err := row.Scan(&p.ID, &p.Site, &p.Cpt, &p.OperatingDate, &p.ShipMethod,
		&p.Cutoff, &p.State, &p.Door, &p.Trailer, &p.Appointment,
		&shortJSON, &p.PackageCount, &p.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrLoadNotFound
		}
		return nil, fmt.Errorf("scan outbound load: %w", err)
	}
	if err := json.Unmarshal(shortJSON, &p.ShortShips); err != nil {
		return nil, fmt.Errorf("decode short ships of %s: %w", p.ID, err)
	}
	return outboundload.Rehydrate(p)
}

// Save implements ports.LoadRepo. An insert that would leave two open loads
// of one cutoff key fails on uq_open_load_per_cutoff and is returned as
// repository.ErrDuplicateOpenLoad (the race backstop).
func (r *LoadRepo) Save(ctx context.Context, l *outboundload.OutboundLoad, loadedVersion int64) error {
	short, err := json.Marshal(l.ShortShips())
	if err != nil {
		return fmt.Errorf("encode short ships of %s: %w", l.ID(), err)
	}
	var tag pgconn.CommandTag
	if loadedVersion == 0 {
		tag, err = queryFor(ctx, r.pool).Exec(ctx, `
			INSERT INTO outbound_loads
			    (load_id, site_id, cpt_id, operating_date, ship_method, cutoff,
			     state, door_code, trailer_id, appointment_id, short_ships,
			     package_count, version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			l.ID(), l.SiteID(), l.CptID(), l.OperatingDate(), l.ShipMethod(),
			l.Cutoff(), l.State(), l.Door(), l.Trailer(), l.Appointment(),
			short, l.PackageCount(), l.Version())
	} else {
		tag, err = queryFor(ctx, r.pool).Exec(ctx, `
			UPDATE outbound_loads SET
			    site_id=$2, cpt_id=$3, operating_date=$4, ship_method=$5,
			    cutoff=$6, state=$7, door_code=$8, trailer_id=$9,
			    appointment_id=$10, short_ships=$11, package_count=$12,
			    version=$13, updated_at=now()
			WHERE load_id=$1 AND version=$14`,
			l.ID(), l.SiteID(), l.CptID(), l.OperatingDate(), l.ShipMethod(),
			l.Cutoff(), l.State(), l.Door(), l.Trailer(), l.Appointment(),
			short, l.PackageCount(), l.Version(), loadedVersion)
	}
	if err != nil {
		if isUniqueViolation(err, uqOpenLoadConstraint) {
			return fmt.Errorf("%w: %s", repository.ErrDuplicateOpenLoad, l.Key())
		}
		return fmt.Errorf("save outbound load %s: %w", l.ID(), err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: outbound load %s (loaded version %d)",
			repository.ErrConcurrentModification, l.ID(), loadedVersion)
	}
	return nil
}

// FindOpenByKey implements ports.LoadRepo.
func (r *LoadRepo) FindOpenByKey(ctx context.Context, key outboundload.LoadKey) (*outboundload.OutboundLoad, error) {
	return scanLoad(queryFor(ctx, r.pool).QueryRow(ctx, loadCols+`
		FROM outbound_loads
		WHERE site_id=$1 AND cpt_id=$2 AND operating_date=$3 AND ship_method=$4
		  AND state IN `+openLoadStates, key.Site, key.Cpt, key.OperatingDate, key.ShipMethod))
}

// OpenLoadKeys implements ports.LoadRepo.
func (r *LoadRepo) OpenLoadKeys(ctx context.Context, site shared.SiteID) ([]outboundload.LoadKey, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT site_id, cpt_id, operating_date, ship_method
		FROM outbound_loads WHERE site_id=$1 AND state IN `+openLoadStates, site)
	if err != nil {
		return nil, fmt.Errorf("query open load keys: %w", err)
	}
	defer rows.Close()
	var out []outboundload.LoadKey
	for rows.Next() {
		var k outboundload.LoadKey
		if err := rows.Scan(&k.Site, &k.Cpt, &k.OperatingDate, &k.ShipMethod); err != nil {
			return nil, fmt.Errorf("scan open load key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListOpenByCpt implements ports.LoadRepo: cutoff ascending.
func (r *LoadRepo) ListOpenByCpt(ctx context.Context, site shared.SiteID, cpt shared.CptID) ([]*outboundload.OutboundLoad, error) {
	return r.listLoadsWhere(ctx, `WHERE site_id=$1 AND cpt_id=$2 AND state IN `+openLoadStates+`
		ORDER BY cutoff ASC`, site, cpt)
}

// ListOpenByDoor implements ports.LoadRepo: PLANNED first, then cutoff.
func (r *LoadRepo) ListOpenByDoor(ctx context.Context, site shared.SiteID, door shared.DoorCode) ([]*outboundload.OutboundLoad, error) {
	return r.listLoadsWhere(ctx, `WHERE site_id=$1 AND door_code=$2 AND state IN `+openLoadStates+`
		ORDER BY (state = 'PLANNED') DESC, cutoff ASC`, site, door)
}

// FindLatestByTrailer implements ports.LoadRepo: the newest-updated load of
// the site the trailer was attached to.
func (r *LoadRepo) FindLatestByTrailer(ctx context.Context, site shared.SiteID, trailer shared.TrailerID) (*outboundload.OutboundLoad, error) {
	return scanLoad(queryFor(ctx, r.pool).QueryRow(ctx, loadCols+`
		FROM outbound_loads
		WHERE site_id=$1 AND trailer_id=$2
		ORDER BY updated_at DESC, version DESC LIMIT 1`, site, trailer))
}

// List implements ports.LoadRepo: newest first (created_at, id desc),
// keyset-paged after `after`, narrowed by filter.
func (r *LoadRepo) List(ctx context.Context, filter repository.LoadFilter, after shared.LoadID, limit int) ([]*outboundload.OutboundLoad, error) {
	where := "WHERE TRUE"
	args := []any{}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if filter.Site != "" {
		where += " AND site_id = " + arg(filter.Site)
	}
	if filter.State != "" {
		where += " AND state = " + arg(filter.State)
	}
	if filter.OperatingDate != "" {
		where += " AND operating_date = " + arg(filter.OperatingDate)
	}
	if after != "" {
		where += " AND (created_at, load_id) < (SELECT created_at, load_id FROM outbound_loads WHERE load_id = " + arg(string(after)) + ")"
	}
	where += " ORDER BY created_at DESC, load_id DESC LIMIT " + arg(limit)
	return r.listLoadsWhere(ctx, where, args...)
}

func (r *LoadRepo) listLoadsWhere(ctx context.Context, where string, args ...any) ([]*outboundload.OutboundLoad, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, loadCols+`
		FROM outbound_loads `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query outbound loads: %w", err)
	}
	defer rows.Close()
	var out []*outboundload.OutboundLoad
	for rows.Next() {
		var (
			p         outboundload.RehydrateParams
			shortJSON []byte
		)
		if err := rows.Scan(&p.ID, &p.Site, &p.Cpt, &p.OperatingDate, &p.ShipMethod,
			&p.Cutoff, &p.State, &p.Door, &p.Trailer, &p.Appointment,
			&shortJSON, &p.PackageCount, &p.Version); err != nil {
			return nil, fmt.Errorf("scan outbound load row: %w", err)
		}
		if err := json.Unmarshal(shortJSON, &p.ShortShips); err != nil {
			return nil, fmt.Errorf("decode short ships of %s: %w", p.ID, err)
		}
		l, err := outboundload.Rehydrate(p)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
