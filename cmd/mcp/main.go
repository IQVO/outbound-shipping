// Command mcp is the composition root for the outbound-shipping MCP server:
// it wires the Postgres repositories to the SAME read use cases the REST
// adapter serves (Queries) and those to the inbound MCP adapter, then serves
// MCP over Streamable HTTP (and nothing else: no stdio, no SSE). It is a
// second, independent deployable alongside cmd/api.
//
// The surface is READ-ONLY (fleet decision; loads_at_risk and missed_loads
// are L1 read tools): no write use case is wired here at all, so this binary
// can never insert into the outbox (it builds no event sink, no unit of work
// and no outbox repository). It never starts the outbox relay, never runs
// the local-copy consumers and never dials Kafka: those run in cmd/api only.
// Like cmd/api it has no in-memory mode: without DATABASE_URL it refuses to
// boot. There is NO auth of any kind (fleet-wide revert 2026-09-11); access
// control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	DATABASE_URL              REQUIRED. Postgres DSN
//	MIGRATIONS_DATABASE_URL   direct DSN for the boot migrations (default DATABASE_URL)
//	MCP_ADDR                  listen address (default :8090)
//	LOG_LEVEL                 debug | info | warn (or warning) | error (default info)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	inboundmcp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/mcp"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/postgres"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/system"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// errNoDatabase is the boot error of a missing DATABASE_URL.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger(os.Getenv("LOG_LEVEL"))
	slog.SetDefault(logger)

	databaseURL := os.Getenv("DATABASE_URL")
	deps, closeAdapters, err := buildDeps(databaseURL)
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              addrOr(os.Getenv("MCP_ADDR")),
		Handler:           newRouter(inboundmcp.Handler(inboundmcp.NewServer(deps))),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return serveMCP(logger, srv)
}

// buildDeps migrates and connects Postgres and wires the READ use cases over
// it. The returned close func releases the pool. An empty databaseURL is a
// boot error: there is no fallback runtime. Like every sibling's cmd/mcp
// this binary runs the idempotent embedded migrations on start, so it can
// boot against a fresh database before cmd/api has; golang-migrate's advisory
// lock makes concurrent starts safe. The migrations DSN honours
// MIGRATIONS_DATABASE_URL (DDL needs a direct session connection).
func buildDeps(databaseURL string) (inboundmcp.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inboundmcp.Deps{}, noop, errNoDatabase
	}
	if err := postgres.RunMigrations(postgres.MigrationsURL(os.Getenv)); err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(context.Background(), databaseURL)
	if err != nil {
		return inboundmcp.Deps{}, noop, err
	}
	queries := &usecases.Queries{
		Loads: postgres.NewLoadRepo(pool),
		Pkgs:  postgres.NewPackageRepo(pool),
		Bols:  postgres.NewBolRepo(pool),
		Board: postgres.NewBoardStore(pool),
		Clock: system.Clock{},
	}
	return inboundmcp.Deps{Queries: queries}, pool.Close, nil
}

// addrOr defaults the listen address.
func addrOr(addr string) string {
	if addr == "" {
		return ":8090"
	}
	return addr
}

// newLogger maps LOG_LEVEL onto slog levels; unknown values mean info.
func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
