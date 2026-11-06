package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// PackageRepo is the in-memory ports.PackageRepo with the Postgres adapter's
// version-guard semantics. ListSweepable joins each package's load cutoff
// through Loads (the Postgres adapter joins in SQL).
type PackageRepo struct {
	mu    sync.Mutex
	rows  map[shared.PackageID]*pkgRow
	Loads *LoadRepo
}

type pkgRow struct {
	pkg     *shippingpackage.ShippingPackage
	created time.Time
	updated time.Time
}

// NewPackageRepo constructs an empty PackageRepo over loads (nil is allowed
// when no sweep query runs).
func NewPackageRepo(loads *LoadRepo) *PackageRepo {
	return &PackageRepo{rows: map[shared.PackageID]*pkgRow{}, Loads: loads}
}

// Get implements ports.PackageRepo.
func (r *PackageRepo) Get(_ context.Context, id shared.PackageID) (*shippingpackage.ShippingPackage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok {
		return nil, repository.ErrPackageNotFound
	}
	return pkgCopy(row.pkg)
}

// Save implements ports.PackageRepo.
func (r *PackageRepo) Save(_ context.Context, p *shippingpackage.ShippingPackage, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, exists := r.rows[p.ID()]
	if loadedVersion == 0 {
		if exists {
			return repository.ErrConcurrentModification
		}
		fresh, err := pkgCopy(p)
		if err != nil {
			return err
		}
		r.rows[p.ID()] = &pkgRow{pkg: fresh, created: time.Now().UTC(), updated: time.Now().UTC()}
		return nil
	}
	if !exists || row.pkg.Version() != loadedVersion {
		return repository.ErrConcurrentModification
	}
	fresh, err := pkgCopy(p)
	if err != nil {
		return err
	}
	row.pkg = fresh
	row.updated = time.Now().UTC()
	return nil
}

// ListByLoad implements ports.PackageRepo: ordered by package id, keyset
// paged after `after`.
func (r *PackageRepo) ListByLoad(_ context.Context, load shared.LoadID, filter repository.PackageFilter, after shared.PackageID, limit int) ([]*shippingpackage.ShippingPackage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]shared.PackageID, 0, len(r.rows))
	for id, row := range r.rows {
		if row.pkg.LoadID() == load {
			ids = append(ids, id)
		}
	}
	sortIDs(ids)
	out := make([]*shippingpackage.ShippingPackage, 0, limit)
	started := after == ""
	for _, id := range ids {
		if !started {
			if id == after {
				started = true
			}
			continue
		}
		p := r.rows[id].pkg
		if filter.State != "" && string(p.State()) != filter.State {
			continue
		}
		fresh, err := pkgCopy(p)
		if err != nil {
			return nil, err
		}
		out = append(out, fresh)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// ListSweepable implements ports.PackageRepo: the site's EXPECTED or STAGED
// packages with their load's cutoff at or before at. The load cutoffs come
// from the same snapshot's loads (wired by Sweep join), so the repo also
// holds a LoadRepo reference set by SetLoads.
func (r *PackageRepo) ListSweepable(ctx context.Context, site shared.SiteID, at time.Time) ([]repository.Sweepable, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []repository.Sweepable
	ids := make([]shared.PackageID, 0, len(r.rows))
	for id, row := range r.rows {
		if row.pkg.SiteID() == site {
			ids = append(ids, id)
		}
	}
	sortIDs(ids)
	for _, id := range ids {
		p := r.rows[id].pkg
		if p.State() != shippingpackage.StateExpected && p.State() != shippingpackage.StateStaged {
			continue
		}
		if r.Loads == nil {
			continue
		}
		load, err := r.Loads.Get(ctx, p.LoadID())
		if err != nil {
			continue
		}
		if load.Cutoff().After(at) || !openState(load.State()) {
			continue
		}
		fresh, err := pkgCopy(p)
		if err != nil {
			return nil, err
		}
		out = append(out, repository.Sweepable{Package: fresh, Cutoff: load.Cutoff()})
	}
	return out, nil
}

// ListMissed implements ports.PackageRepo: flagged packages newest first by
// update time.
func (r *PackageRepo) ListMissed(_ context.Context, site shared.SiteID, limit int) ([]*shippingpackage.ShippingPackage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*pkgRow
	for _, row := range r.rows {
		if row.pkg.SiteID() == site && row.pkg.MissedFlagged() {
			rows = append(rows, row)
		}
	}
	for i := len(rows) - 1; i > 0; i-- {
		for j := 0; j < i; j++ {
			if rows[j].updated.Before(rows[j+1].updated) {
				rows[j], rows[j+1] = rows[j+1], rows[j]
			}
		}
	}
	out := make([]*shippingpackage.ShippingPackage, 0, limit)
	for _, row := range rows {
		if len(out) == limit {
			break
		}
		fresh, err := pkgCopy(row.pkg)
		if err != nil {
			return nil, err
		}
		out = append(out, fresh)
	}
	return out, nil
}

// pkgCopy rebuilds a package through Rehydrate so callers never share
// mutable state with the store.
func pkgCopy(p *shippingpackage.ShippingPackage) (*shippingpackage.ShippingPackage, error) {
	return shippingpackage.Rehydrate(shippingpackage.RehydrateParams{
		ID: p.ID(), Site: p.SiteID(), SourceOrderID: p.SourceOrderID(),
		WorkUnitRef: p.WorkUnitRef(), WeightG: p.WeightG(),
		LoadID: p.LoadID(), StagingLoc: p.StagingLocation(),
		State: p.State(), MissedFlagged: p.MissedFlagged(),
		Version: p.Version(),
	})
}

// sortIDs sorts a package id slice ascending (byte order, the "C" collation).
func sortIDs(ids []shared.PackageID) {
	for i := len(ids) - 1; i > 0; i-- {
		for j := 0; j < i; j++ {
			if ids[j] > ids[j+1] {
				ids[j], ids[j+1] = ids[j+1], ids[j]
			}
		}
	}
}
