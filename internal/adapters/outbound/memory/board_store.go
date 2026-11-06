package memory

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// BoardStore is the in-memory LoadBoardView projection: the write side the
// projector folds into and the read side the report and the MCP tools query.
// It mirrors the Postgres projection's tables.
type BoardStore struct {
	rows boardRows
}

// NewBoardStore constructs an empty BoardStore.
func NewBoardStore() *BoardStore { return &BoardStore{} }

// ApplyLoadPlanned implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyLoadPlanned(_ context.Context, load shared.LoadID, site shared.SiteID, cpt shared.CptID, date, method string, cutoff time.Time) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.upsert(load, func(r *repository.LoadBoardRow) {
		r.Site, r.Cpt = site, cpt
		r.OperatingDate = outboundload.OperatingDate(date)
		r.ShipMethod = shared.ShipMethod(method)
		r.Cutoff = cutoff
	})
	return nil
}

// ApplyCount implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyCount(_ context.Context, load shared.LoadID, staged, loaded int64) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.upsert(load, func(r *repository.LoadBoardRow) {
		r.StagedCount += staged
		r.LoadedCount += loaded
	})
	return nil
}

// ApplyMissed implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyMissed(_ context.Context, load shared.LoadID, miss int64) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.upsert(load, func(r *repository.LoadBoardRow) { r.PackagesMissed += miss })
	return nil
}

// ApplyClosed implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyClosed(_ context.Context, load shared.LoadID, closedAt, cutoff time.Time) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.upsert(load, func(r *repository.LoadBoardRow) {
		r.ClosedAt = closedAt
		r.ClosedOnTime = !closedAt.After(cutoff)
	})
	return nil
}

// ApplyConfirmed implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyConfirmed(_ context.Context, load shared.LoadID, at time.Time) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.upsert(load, func(r *repository.LoadBoardRow) { r.ConfirmedAt = at })
	return nil
}

// ApplyCancelled implements usecases.BoardStoreWriter.
func (s *BoardStore) ApplyCancelled(_ context.Context, load shared.LoadID, at time.Time) error {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	s.rows.upsert(load, func(r *repository.LoadBoardRow) { r.CancelledAt = at })
	return nil
}

// Report implements ports.BoardStore: adherence rows grouped by
// (site, operating date, cpt), sorted.
func (s *BoardStore) Report(_ context.Context, site shared.SiteID, date outboundload.OperatingDate) ([]repository.AdherenceRow, error) {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	groups := map[string]*repository.AdherenceRow{}
	for _, id := range s.rows.order {
		r := s.rows.rows[id]
		if site != "" && r.Site != site {
			continue
		}
		if date != "" && r.OperatingDate != date {
			continue
		}
		key := string(r.Site) + "\x00" + string(r.OperatingDate) + "\x00" + string(r.Cpt)
		g := groups[key]
		if g == nil {
			g = &repository.AdherenceRow{Site: r.Site, Cpt: r.Cpt, OperatingDate: r.OperatingDate}
			groups[key] = g
		}
		g.LoadsTotal++
		if !r.ClosedAt.IsZero() {
			g.LoadsClosed++
		}
		g.PackagesMissed += r.PackagesMissed
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]repository.AdherenceRow, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		if g.LoadsTotal > 0 {
			g.Adherence = float64(g.LoadsClosed) / float64(g.LoadsTotal)
		}
		out = append(out, *g)
	}
	return out, nil
}

// RowsAtRisk implements ports.BoardStore: rows not closed/confirmed whose
// cutoff is within horizon of now, ordered by cutoff, with the outstanding
// package counts folded from the stage/load funnel.
func (s *BoardStore) RowsAtRisk(_ context.Context, site shared.SiteID, now time.Time, horizon time.Duration, limit int) ([]repository.LoadBoardRow, error) {
	s.rows.mu.Lock()
	defer s.rows.mu.Unlock()
	var out []repository.LoadBoardRow
	for _, id := range s.rows.order {
		r := s.rows.rows[id]
		if site != "" && r.Site != site {
			continue
		}
		if !r.ClosedAt.IsZero() || !r.CancelledAt.IsZero() {
			continue
		}
		until := r.Cutoff.Sub(now)
		if until > horizon || r.Cutoff.Before(now) {
			continue
		}
		out = append(out, *r)
		if len(out) == limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cutoff.Before(out[j].Cutoff) })
	return out, nil
}
