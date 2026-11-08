// Package mcp is the inbound Model Context Protocol adapter: it exposes
// outbound-shipping to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built
// on the official MCP Go SDK and served over Streamable HTTP only.
//
// The surface is READ-ONLY (fleet decision; the model's fleet_changes row
// for warehouse-ops-agent): loads_at_risk and missed_loads read the board
// and the package store; every mutating operation (stagings, loadings,
// unloadings, closures, reopenings, cancellations, BOL voids, sweeps) stays
// on REST. governance_test.go fails the build on any write-verb tool name.
// This package depends inward on the application layer only -- never on an
// outbound adapter or the REST adapter -- and nothing else may depend on it
// (internal/architecture's TestMCPAdapterDependencyRule). There is no auth
// of any kind (fleet-wide revert 2026-09-11; TestNoAuthMiddlewareReintroduced).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "outbound-shipping-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Outbound shipping of the warehouse (read-only): see the loads whose cutoff is at risk with " +
	"loads_at_risk (per site and cutoff: the open loads closing within the horizon, with their staged, loaded and " +
	"missed package counts), and the packages that raised PackageMissedLoad with missed_loads. The board is the " +
	"projector-fed local copy, so it may lag the OLTP store by a few seconds. Nothing here changes data: staging, " +
	"loading, closing, reopening or cancelling a load, voiding a bill of lading and sweeping missed loads are REST " +
	"operations of outbound-shipping."

// NewServer builds the MCP server for this bounded context with every tool,
// resource and prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	// Stateless (Agentic Standard v2, AS-4): no Mcp-Session-Id is issued.
	// Every tool is a single request over the read models.
	return mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return server
	}, nil)
}
