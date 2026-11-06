package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// mimeTypeJSON is the resource body type.
const mimeTypeJSON = "application/json"

// loadTemplate is the one resource this server offers: one load, read
// through the same query use case as GET /loads/{id}.
const loadTemplate = "warehouse://outbound-shipping/loads/{loadId}"

// registerResources adds the load resource (AS-3: tools alone are not an
// agent surface).
func (d Deps) registerResources(server *mcp.Server) {
	mcp.AddResourceTemplate(server, &mcp.ResourceTemplate{
		URITemplate: loadTemplate,
		Name:        "outbound load",
		Description: "One outbound load: its cutoff key (site, cpt, operating date, ship method), cutoff instant, " +
			"state, door, trailer, appointment, short-ships and loaded package count, as the REST GET /loads/{id} " +
			"returns it.",
		MIMEType: mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, err := splitURI(req.Params.URI, "loads")
		if err != nil {
			return nil, err
		}
		load, err := d.Queries.GetLoad(ctx, shared.LoadID(id))
		if err != nil {
			return nil, mapError(err)
		}
		body := map[string]any{
			"id": string(load.ID()), "site_id": string(load.SiteID()),
			"cpt_id": string(load.CptID()), "operating_date": string(load.OperatingDate()),
			"ship_method": string(load.ShipMethod()), "cutoff": load.Cutoff().UTC().Format("2006-01-02T15:04:05Z07:00"),
			"state": string(load.State()), "door_code": string(load.Door()),
			"trailer_id": string(load.Trailer()), "appointment_id": string(load.Appointment()),
			"package_count": load.PackageCount(), "version": load.Version(),
		}
		text, err := marshalView(body)
		if err != nil {
			return nil, fmt.Errorf("encode load %s: %w", id, err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: mimeTypeJSON,
				Text:     text,
			}},
		}, nil
	})
}

// readinessPrompt is the one prompt this server offers (AS-3).
const readinessPrompt = "load-close-readiness"

// registerPrompts adds the close-readiness prompt.
func (d Deps) registerPrompts(server *mcp.Server) {
	mcp.AddPrompt(server, &mcp.Prompt{
		Name: readinessPrompt,
		Description: "Summarise whether one site's loads are ready to close before their cutoff: the loads at risk, " +
			"the packages still outstanding, and what the dock supervisor should do next (load, short-ship with a " +
			"reason, or let the sweep flag the misses).",
		Arguments: []*mcp.PromptArgument{{
			Name:        "site_id",
			Description: "The facility-layout site code to summarise.",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.CallToolRequest, args mcp.PromptArguments) (*mcp.CallToolResult, error) {
		// The prompt only TEXT; the client's model calls the read tools
		// itself (L1 authority end to end).
		site := args["site_id"]
		if site == "" {
			return nil, errInvalidSiteID
		}
		text := "Summarise the outbound loading readiness of site " + site + ". " +
			"Call loads_at_risk with site_id \"" + site + "\" and a horizon of four hours, then missed_loads with " +
			"the same site_id. For every at-risk load report the cutoff, the staged and loaded counts and the " +
			"minutes left; call out every package that already missed. Close with the recommended next action per " +
			"load: finish loading, short-ship named packages with reasons, or let the missed-load sweep flag them. " +
			"Do not propose any write operation through this server: it is read-only."
		return &mcp.CallToolResult{Content: []*mcp.Content{{Type: "text", Text: text}}}, nil
	})
}

// mapError maps a use case error to the MCP tool error text (stable codes).
func mapError(err error) error {
	switch {
	case errors.Is(err, repository.ErrLoadNotFound):
		return fmt.Errorf("load-not-found: %w", err)
	case errors.Is(err, repository.ErrPackageNotFound):
		return fmt.Errorf("package-not-found: %w", err)
	case errors.Is(err, usecases.ErrInvalidListQuery):
		return fmt.Errorf("%w: %w", errInvalidParameter, err)
	default:
		return fmt.Errorf("internal-error: %w", err)
	}
}

// Stable adapter errors (mirrored in the tool descriptions).
var (
	errInvalidSiteID    = errors.New("invalid-site-id: site_id is required")
	errInvalidLimit     = errors.New("invalid-limit: limit must be 1 to 200")
	errInvalidParameter = errors.New("invalid-parameter")
)
