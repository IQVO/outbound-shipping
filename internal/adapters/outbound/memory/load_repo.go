package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// LoadRepo is the in-memory ports.LoadRepo, mirroring the Postgres adapter's
// semantics: version-guarded saves, ErrConcurrentModification on a lost
// race, and the one-open-load partial unique index (ErrDuplicateOpenLoad)
// enforced on every insert and update.
type LoadRepo struct {
	mu    sync.Mutex
	rows  map[shared.LoadID]*storedLoad
	order []shared.LoadID // insertion order; the listing order reverses it
}

type storedLoad struct {
	load      *outboundload.OutboundLoad
	createdAt time.Time
}

// NewLoadRepo constructs an empty LoadRepo.
func NewLoadRepo() *LoadRepo { return &LoadRepo{rows: map[shared.LoadID]*storedLoad{}} }

func openState(s outboundload.State) bool {
	return s == outboundload.StatePlanned || s == outboundload.StateLoading
}

// openKeyOccupied reports whether an open load OTHER than skip already holds
// key (the partial unique index).
func (r *LoadRepo) openKeyOccupied(key outboundload.LoadKey, skip shared.LoadID) bool {
	for id, row := range r.rows {
		if id == skip {
			continue
		}
		if openState(row.load.State()) && row.load.Key() == key {
			return true
		}
	}
	return false
}

// Get implements ports.LoadRepo.
func (r *LoadRepo) Get(_ context.Context, id shared.LoadID) (*outboundload.OutboundLoad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[id]
	if !ok {
		return nil, repository.ErrLoadNotFound
	}
	return rehydrateCopy(row.load)
}

// Save implements ports.LoadRepo.
func (r *LoadRepo) Save(_ context.Context, l *outboundload.OutboundLoad, loadedVersion int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := l.ID()
	row, exists := r.rows[id]
	if loadedVersion == 0 {
		if exists {
			return repository.ErrConcurrentModification
		}
		if openState(l.State()) && r.openKeyOccupied(l.Key(), id) {
			return repository.ErrDuplicateOpenLoad
		}
		fresh, err := rehydrateFrom(l)
		if err != nil {
			return err
		}
		r.rows[id] = &storedLoad{load: fresh, createdAt: time.Now().UTC()}
		r.order = append(r.order, id)
		return nil
	}
	if !exists || row.load.Version() != loadedVersion {
		return repository.ErrConcurrentModification
	}
	if openState(l.State()) && r.openKeyOccupied(l.Key(), id) {
		return repository.ErrDuplicateOpenLoad
	}
	fresh, err := rehydrateFrom(l)
	if err != nil {
		return err
	}
	row.load = fresh
	return nil
}

// FindOpenByKey implements ports.LoadRepo.
func (r *LoadRepo) FindOpenByKey(_ context.Context, key outboundload.LoadKey) (*outboundload.OutboundLoad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.order {
		row := r.rows[id]
		if openState(row.load.State()) && row.load.Key() == key {
			return rehydrateCopy(row.load)
		}
	}
	return nil, repository.ErrLoadNotFound
}

// OpenLoadKeys implements ports.LoadRepo.
func (r *LoadRepo) OpenLoadKeys(_ context.Context, site shared.SiteID) ([]outboundload.LoadKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []outboundload.LoadKey
	for _, id := range r.order {
		row := r.rows[id]
		if openState(row.load.State()) && row.load.SiteID() == site {
			out = append(out, row.load.Key())
		}
	}
	return out, nil
}

// ListOpenByCpt implements ports.LoadRepo.
func (r *LoadRepo) ListOpenByCpt(_ context.Context, site shared.SiteID, cpt shared.CptID) ([]*outboundload.OutboundLoad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*outboundload.OutboundLoad
	for _, id := range r.order {
		row := r.rows[id]
		if openState(row.load.State()) && row.load.SiteID() == site && row.load.CptID() == cpt {
			l, err := rehydrateCopy(row.load)
			if err != nil {
				return nil, err
			}
			out = append(out, l)
		}
	}
	return out, nil
}

// ListOpenByDoor implements ports.LoadRepo: PLANNED loads first, then
// LOADING, each by insertion order.
func (r *LoadRepo) ListOpenByDoor(_ context.Context, site shared.SiteID, door shared.DoorCode) ([]*outboundload.OutboundLoad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var planned, loading []*outboundload.OutboundLoad
	for _, id := range r.order {
		row := r.rows[id]
		if !openState(row.load.State()) || row.load.SiteID() != site || row.load.Door() != door {
			continue
		}
		l, err := rehydrateCopy(row.load)
		if err != nil {
			return nil, err
		}
		if row.load.State() == outboundload.StatePlanned {
			planned = append(planned, l)
		} else {
			loading = append(loading, l)
		}
	}
	return append(planned, loading...), nil
}

// FindLatestByTrailer implements ports.LoadRepo: the highest-version load of
// the site the trailer was attached to.
func (r *LoadRepo) FindLatestByTrailer(_ context.Context, site shared.SiteID, trailer shared.TrailerID) (*outboundload.OutboundLoad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *outboundload.OutboundLoad
	for _, row := range r.rows {
		if row.load.SiteID() != site || row.load.Trailer() != trailer {
			continue
		}
		if best == nil || row.load.Version() > best.Version() {
			l, err := rehydrateCopy(row.load)
			if err != nil {
				return nil, err
			}
			best = l
		}
	}
	if best == nil {
		return nil, repository.ErrLoadNotFound
	}
	return best, nil
}

// List implements ports.LoadRepo: newest first (created_at, then id,
// descending), keyset-paged after `after`.
func (r *LoadRepo) List(_ context.Context, filter repository.LoadFilter, after shared.LoadID, limit int) ([]*outboundload.OutboundLoad, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	type entry struct {
		id        shared.LoadID
		createdAt time.Time
		load      *outboundload.OutboundLoad
	}
	var entries []entry
	for _, id := range r.order {
		row := r.rows[id]
		l := row.load
		if filter.Site != "" && l.SiteID() != filter.Site {
			continue
		}
		if filter.State != "" && l.State() != filter.State {
			continue
		}
		if filter.OperatingDate != "" && l.OperatingDate() != filter.OperatingDate {
			continue
		}
		entries = append(entries, entry{id: id, createdAt: row.createdAt, load: l})
	}
	// newest first: created_at desc, then id desc
	for i := len(entries) - 1; i > 0; i-- {
		for j := 0; j < i; j++ {
			a, b := entries[j], entries[j+1]
			if a.createdAt.Before(b.createdAt) || (a.createdAt.Equal(b.createdAt) && a.id < b.id) {
				entries[j], entries[j+1] = entries[j+1], entries[j]
			}
		}
	}
	started := after == ""
	out := make([]*outboundload.OutboundLoad, 0, limit)
	for _, e := range entries {
		if !started {
			if e.id == after {
				started = true
			}
			continue
		}
		l, err := rehydrateCopy(e.load)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// rehydrateCopy rebuilds a load through Rehydrate so callers never share
// mutable state with the store.
func rehydrateCopy(l *outboundload.OutboundLoad) (*outboundload.OutboundLoad, error) {
	return rehydrateFrom(l)
}

// rehydrateFrom snapshots l's public state into a fresh aggregate.
func rehydrateFrom(l *outboundload.OutboundLoad) (*outboundload.OutboundLoad, error) {
	return outboundload.Rehydrate(outboundload.RehydrateParams{
		ID: l.ID(), Site: l.SiteID(), Cpt: l.CptID(),
		OperatingDate: l.OperatingDate(), ShipMethod: l.ShipMethod(),
		Cutoff: l.Cutoff(), State: l.State(), Door: l.Door(),
		Trailer: l.Trailer(), Appointment: l.Appointment(),
		ShortShips:   append([]shared.ShortShip(nil), l.ShortShips()...),
		PackageCount: l.PackageCount(), Version: l.Version(),
	})
}
