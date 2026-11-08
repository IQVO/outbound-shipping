package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// authorityMetaKey is the tools/list _meta key carrying a tool's level; the
// fleet rule is agent-authority (.claude/rules/fleet/agent-authority.md).
const (
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
)

// authorityMeta is the Meta a tool registers to declare its authority level.
func authorityMeta(level string) mcp.Meta { return mcp.Meta{authorityMetaKey: level} }

// Deps are the read use cases the MCP surface is wired to (cmd/mcp builds
// them over the Postgres adapters).
type Deps struct {
	Queries *usecases.Queries
}

// --- tool inputs and views ---------------------------------------------------

// loadsAtRiskInput narrows loads_at_risk. Every optional field carries
// omitempty (the fleet rule pinned by TestMCPOptionalToolInputsCarryOmitempty).
type loadsAtRiskInput struct {
	// SiteID narrows to one site; empty means every site.
	SiteID string `json:"site_id,omitempty"`
	// HorizonHours is how far ahead a cutoff counts as at risk; default 4.
	HorizonHours int `json:"horizon_hours,omitempty"`
	// Limit caps the rows; default 50, max 200.
	Limit int `json:"limit,omitempty"`
}

// missedLoadsInput narrows missed_loads.
type missedLoadsInput struct {
	// SiteID narrows to one site; required.
	SiteID string `json:"site_id"`
	// Limit caps the rows; default 50, max 200.
	Limit int `json:"limit,omitempty"`
}

type loadAtRiskView struct {
	LoadID         string  `json:"load_id"`
	SiteID         string  `json:"site_id"`
	CptID          string  `json:"cpt_id"`
	OperatingDate  string  `json:"operating_date"`
	ShipMethod     string  `json:"ship_method"`
	Cutoff         string  `json:"cutoff"`
	MinutesLeft    float64 `json:"minutes_left"`
	StagedCount    int64   `json:"staged_count"`
	LoadedCount    int64   `json:"loaded_count"`
	PackagesMissed int64   `json:"packages_missed"`
}

type missedLoadView struct {
	PackageID     string `json:"package_id"`
	LoadID        string `json:"load_id"`
	SiteID        string `json:"site_id"`
	SourceOrderID string `json:"source_order_id,omitempty"`
	State         string `json:"state"`
}

type loadsAtRiskView struct {
	Rows []loadAtRiskView `json:"rows"`
}

type missedLoadsView struct {
	Rows []missedLoadView `json:"rows"`
}

// --- handlers ----------------------------------------------------------------

func (d Deps) loadsAtRisk(ctx context.Context, in loadsAtRiskInput) (loadsAtRiskView, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = usecases.DefaultPageLimit
	}
	if limit > usecases.MaxPageLimit {
		return loadsAtRiskView{}, errInvalidLimit
	}
	rows, err := d.Queries.LoadsAtRisk(ctx, shared.SiteID(in.SiteID), in.HorizonHours, limit)
	if err != nil {
		return loadsAtRiskView{}, mapError(err)
	}
	out := loadsAtRiskView{Rows: make([]loadAtRiskView, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, loadAtRiskView{
			LoadID: string(r.LoadID), SiteID: string(r.Site), CptID: string(r.Cpt),
			OperatingDate: string(r.OperatingDate), ShipMethod: string(r.ShipMethod),
			Cutoff:      r.Cutoff.UTC().Format(time.RFC3339),
			MinutesLeft: time.Until(r.Cutoff).Minutes(),
			StagedCount: r.StagedCount, LoadedCount: r.LoadedCount,
			PackagesMissed: r.PackagesMissed,
		})
	}
	return out, nil
}

func (d Deps) missedLoads(ctx context.Context, in missedLoadsInput) (missedLoadsView, error) {
	if in.SiteID == "" {
		return missedLoadsView{}, errInvalidSiteID
	}
	limit := in.Limit
	if limit <= 0 {
		limit = usecases.DefaultPageLimit
	}
	if limit > usecases.MaxPageLimit {
		return missedLoadsView{}, errInvalidLimit
	}
	pkgs, err := d.Queries.MissedLoads(ctx, shared.SiteID(in.SiteID), limit)
	if err != nil {
		return missedLoadsView{}, mapError(err)
	}
	out := missedLoadsView{Rows: make([]missedLoadView, 0, len(pkgs))}
	for _, p := range pkgs {
		out.Rows = append(out.Rows, missedLoadView{
			PackageID: string(p.ID()), LoadID: string(p.LoadID()),
			SiteID: string(p.SiteID()), SourceOrderID: p.SourceOrderID(),
			State: string(p.State()),
		})
	}
	return out, nil
}

// --- registry ----------------------------------------------------------------

// registerTools adds the two read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; governance_test.go pins the set
// and fails the build on any write-verb name.
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false

	addTool(server, &mcp.Tool{
		Name: "loads_at_risk",
		Description: "The open outbound loads whose cutoff falls within the horizon (default 4 hours), ordered by " +
			"cutoff: per load the site, cpt, operating date, ship method, cutoff instant, minutes left, and the " +
			"staged, loaded and missed package counts of the projector-fed board. Optionally narrowed to one site. " +
			"Read-only; fails with invalid-limit for a limit above 200.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.loadsAtRisk)

	addTool(server, &mcp.Tool{
		Name: "missed_loads",
		Description: "The packages of a site that raised PackageMissedLoad (a cutoff sweep or a departure while the " +
			"load was still LOADING), newest first: package id, load id, source_order_id when the manifest carried " +
			"one, and the package's state. Read-only; fails with invalid-site-id for a blank site and invalid-limit " +
			"for a limit above 200.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.missedLoads)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// marshalView is kept for the resource body below.
func marshalView(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode view: %w", err)
	}
	return string(b), nil
}

// splitURI splits a warehouse://outbound-shipping/<kind>/<id> URI.
func splitURI(uri, kind string) (string, error) {
	scheme := "warehouse://outbound-shipping/"
	rest, ok := strings.CutPrefix(uri, scheme)
	if !ok {
		return "", fmt.Errorf("resource uri %q must start with %q", uri, scheme)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] != kind || parts[1] == "" {
		return "", fmt.Errorf("resource uri %q must be warehouse://outbound-shipping/%s/{id}", uri, kind)
	}
	return parts[1], nil
}
