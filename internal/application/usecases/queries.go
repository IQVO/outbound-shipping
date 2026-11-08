package usecases

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// Queries are the REST reads (GET /loads, GET /loads/{id},
// GET /loads/{id}/packages, GET /bills-of-lading/{n},
// GET /reports/load-adherence) plus the MCP reads. All read-only.
type Queries struct {
	Loads ports.LoadRepo
	Pkgs  ports.PackageRepo
	Bols  ports.BolRepo
	Board ports.BoardStore
	Clock ports.Clock
}

// DefaultPageLimit and MaxPageLimit are the fleet pagination bounds (R7).
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ParseLimit validates a limit query parameter ("" = default).
func ParseLimit(s string) (int, error) {
	if s == "" {
		return DefaultPageLimit, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxPageLimit {
		return 0, fmt.Errorf("%w: limit must be 1 to 200", ErrInvalidListQuery)
	}
	return n, nil
}

// GetLoad returns one load.
func (uc *Queries) GetLoad(ctx context.Context, id shared.LoadID) (*outboundload.OutboundLoad, error) {
	return uc.Loads.Get(ctx, id)
}

// ListLoads returns one page of loads after cursor ("" = first page).
func (uc *Queries) ListLoads(ctx context.Context, filter repository.LoadFilter, cursor string, limit int) ([]*outboundload.OutboundLoad, string, error) {
	if limit < 1 {
		limit = DefaultPageLimit
	}
	page, err := uc.Loads.List(ctx, filter, shared.LoadID(cursor), limit)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(page) == limit {
		next = string(page[len(page)-1].ID())
	}
	return page, next, nil
}

// GetPackage returns one package.
func (uc *Queries) GetPackage(ctx context.Context, id shared.PackageID) (*shippingpackage.ShippingPackage, error) {
	return uc.Pkgs.Get(ctx, id)
}

// ListLoadPackages returns one page of the load's packages after cursor.
func (uc *Queries) ListLoadPackages(ctx context.Context, load shared.LoadID, filter repository.PackageFilter, cursor string, limit int) ([]*shippingpackage.ShippingPackage, string, error) {
	if _, err := uc.Loads.Get(ctx, load); err != nil {
		return nil, "", err
	}
	if limit < 1 {
		limit = DefaultPageLimit
	}
	page, err := uc.Pkgs.ListByLoad(ctx, load, filter, shared.PackageID(cursor), limit)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(page) == limit {
		next = string(page[len(page)-1].ID())
	}
	return page, next, nil
}

// GetBillOfLading returns one document.
func (uc *Queries) GetBillOfLading(ctx context.Context, n shared.BolNumber) (*billoflading.BillOfLading, error) {
	return uc.Bols.Get(ctx, n)
}

// AdherenceReport returns the projector-fed load-adherence rows of the
// scope, with the report's generation instant.
func (uc *Queries) AdherenceReport(ctx context.Context, site shared.SiteID, date outboundload.OperatingDate) ([]repository.AdherenceRow, time.Time, error) {
	if date != "" && !date.Valid() {
		return nil, time.Time{}, fmt.Errorf("%w: operating_date must be YYYY-MM-DD", ErrInvalidListQuery)
	}
	rows, err := uc.Board.Report(ctx, site, date)
	if err != nil {
		return nil, time.Time{}, err
	}
	return rows, uc.Clock.Now(), nil
}

// LoadsAtRisk returns the still-open loads whose cutoff is within horizon
// hours of now, with their outstanding package counts (the loads-at-risk MCP
// read, L1 authority).
func (uc *Queries) LoadsAtRisk(ctx context.Context, site shared.SiteID, horizonHours int, limit int) ([]repository.LoadBoardRow, error) {
	h := defaultRiskHorizon
	if horizonHours > 0 {
		h = time.Duration(horizonHours) * time.Hour
	}
	if limit < 1 {
		limit = DefaultPageLimit
	}
	return uc.Board.RowsAtRisk(ctx, site, uc.Clock.Now(), h, limit)
}

// MissedLoads returns the site's packages that raised PackageMissedLoad,
// newest first (the missed-loads MCP read, L1).
func (uc *Queries) MissedLoads(ctx context.Context, site shared.SiteID, limit int) ([]*shippingpackage.ShippingPackage, error) {
	if limit < 1 {
		limit = DefaultPageLimit
	}
	return uc.Pkgs.ListMissed(ctx, site, limit)
}

// defaultRiskHorizon is the loads-at-risk default window.
const defaultRiskHorizon = 4 * time.Hour
