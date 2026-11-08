package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// PackageRepo is the pgxpool-backed ports.PackageRepo.
type PackageRepo struct {
	pool *pgxpool.Pool
}

// NewPackageRepo constructs a PackageRepo over pool.
func NewPackageRepo(pool *pgxpool.Pool) *PackageRepo { return &PackageRepo{pool: pool} }

const pkgCols = `
	SELECT package_id, site_id, source_order_id, work_unit_ref, load_id,
	       staging_location, weight_g, state, missed_flagged, version`

func scanPackage(row pgx.Row) (*shippingpackage.ShippingPackage, error) {
	var p shippingpackage.RehydrateParams
	if err := row.Scan(&p.ID, &p.Site, &p.SourceOrderID, &p.WorkUnitRef,
		&p.LoadID, &p.StagingLoc, &p.WeightG, &p.State, &p.MissedFlagged,
		&p.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrPackageNotFound
		}
		return nil, fmt.Errorf("scan shipping package: %w", err)
	}
	return shippingpackage.Rehydrate(p)
}

// Get implements ports.PackageRepo.
func (r *PackageRepo) Get(ctx context.Context, id shared.PackageID) (*shippingpackage.ShippingPackage, error) {
	return scanPackage(queryFor(ctx, r.pool).QueryRow(ctx, pkgCols+`
		FROM shipping_packages WHERE package_id = $1`, id))
}

// Save implements ports.PackageRepo.
func (r *PackageRepo) Save(ctx context.Context, p *shippingpackage.ShippingPackage, loadedVersion int64) error {
	var (
		tag pgconn.CommandTag
		err error
	)
	if loadedVersion == 0 {
		tag, err = queryFor(ctx, r.pool).Exec(ctx, `
			INSERT INTO shipping_packages
			    (package_id, site_id, source_order_id, work_unit_ref, load_id,
			     staging_location, weight_g, state, missed_flagged, version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			p.ID(), p.SiteID(), p.SourceOrderID(), p.WorkUnitRef(), p.LoadID(),
			p.StagingLocation(), p.WeightG(), p.State(), p.MissedFlagged(), p.Version())
	} else {
		tag, err = queryFor(ctx, r.pool).Exec(ctx, `
			UPDATE shipping_packages SET
			    site_id=$2, source_order_id=$3, work_unit_ref=$4, load_id=$5,
			    staging_location=$6, weight_g=$7, state=$8, missed_flagged=$9,
			    version=$10, updated_at=now()
			WHERE package_id=$1 AND version=$11`,
			p.ID(), p.SiteID(), p.SourceOrderID(), p.WorkUnitRef(), p.LoadID(),
			p.StagingLocation(), p.WeightG(), p.State(), p.MissedFlagged(),
			p.Version(), loadedVersion)
	}
	if err != nil {
		return fmt.Errorf("save shipping package %s: %w", p.ID(), err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: shipping package %s (loaded version %d)",
			repository.ErrConcurrentModification, p.ID(), loadedVersion)
	}
	return nil
}

// ListByLoad implements ports.PackageRepo: package-id order, keyset-paged.
func (r *PackageRepo) ListByLoad(ctx context.Context, load shared.LoadID, filter repository.PackageFilter, after shared.PackageID, limit int) ([]*shippingpackage.ShippingPackage, error) {
	where := `WHERE load_id = $1`
	args := []any{load, limit}
	if filter.State != "" {
		args = append(args, filter.State)
		where += fmt.Sprintf(" AND state = $%d", len(args))
	}
	if after != "" {
		args = append(args, after)
		where += fmt.Sprintf(" AND package_id > $%d", len(args))
	}
	rows, err := queryFor(ctx, r.pool).Query(ctx, pkgCols+`
		FROM shipping_packages `+where+`
		ORDER BY package_id ASC LIMIT $2`, args...)
	if err != nil {
		return nil, fmt.Errorf("query load packages: %w", err)
	}
	defer rows.Close()
	return scanPackages(rows)
}

// ListSweepable implements ports.PackageRepo: the site's EXPECTED or STAGED
// packages on still-open loads whose cutoff is at or before at, with the
// load's cutoff, package-id order.
func (r *PackageRepo) ListSweepable(ctx context.Context, site shared.SiteID, at time.Time) ([]repository.Sweepable, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT p.package_id, p.site_id, p.source_order_id, p.work_unit_ref,
		       p.load_id, p.staging_location, p.weight_g, p.state,
		       p.missed_flagged, p.version, l.cutoff
		FROM shipping_packages p
		JOIN outbound_loads l ON l.load_id = p.load_id
		WHERE p.site_id = $1
		  AND p.state IN ('EXPECTED','STAGED')
		  AND l.state IN ('PLANNED','LOADING')
		  AND l.cutoff <= $2
		ORDER BY p.package_id ASC`, site, at.UTC())
	if err != nil {
		return nil, fmt.Errorf("query sweepable packages: %w", err)
	}
	defer rows.Close()
	var out []repository.Sweepable
	for rows.Next() {
		var p shippingpackage.RehydrateParams
		var cutoff time.Time
		if err := rows.Scan(&p.ID, &p.Site, &p.SourceOrderID, &p.WorkUnitRef,
			&p.LoadID, &p.StagingLoc, &p.WeightG, &p.State, &p.MissedFlagged,
			&p.Version, &cutoff); err != nil {
			return nil, fmt.Errorf("scan sweepable package row: %w", err)
		}
		pkg, err := shippingpackage.Rehydrate(p)
		if err != nil {
			return nil, err
		}
		out = append(out, repository.Sweepable{Package: pkg, Cutoff: cutoff.UTC()})
	}
	return out, rows.Err()
}

// ListMissed implements ports.PackageRepo: flagged packages newest first by
// update time.
func (r *PackageRepo) ListMissed(ctx context.Context, site shared.SiteID, limit int) ([]*shippingpackage.ShippingPackage, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, pkgCols+`
		FROM shipping_packages
		WHERE site_id=$1 AND missed_flagged
		ORDER BY updated_at DESC, package_id ASC LIMIT $2`, site, limit)
	if err != nil {
		return nil, fmt.Errorf("query missed packages: %w", err)
	}
	defer rows.Close()
	return scanPackages(rows)
}

func scanPackages(rows pgx.Rows) ([]*shippingpackage.ShippingPackage, error) {
	var out []*shippingpackage.ShippingPackage
	for rows.Next() {
		var p shippingpackage.RehydrateParams
		if err := rows.Scan(&p.ID, &p.Site, &p.SourceOrderID, &p.WorkUnitRef,
			&p.LoadID, &p.StagingLoc, &p.WeightG, &p.State, &p.MissedFlagged,
			&p.Version); err != nil {
			return nil, fmt.Errorf("scan shipping package row: %w", err)
		}
		pkg, err := shippingpackage.Rehydrate(p)
		if err != nil {
			return nil, err
		}
		out = append(out, pkg)
	}
	return out, rows.Err()
}
