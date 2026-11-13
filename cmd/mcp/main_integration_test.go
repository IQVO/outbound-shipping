//go:build integration

package main

// The cmd/mcp integration test: the real buildDeps (migrations included) over
// testcontainers Postgres, the real MCP server, driven over the SDK's
// in-memory transports the way customer-ordering's cmd/mcp test does.
//
// What it proves, per docs/docs/mcp/tools.md:
//   - buildDeps boots on a FRESH database (the binary runs the idempotent
//     embedded migrations itself, cmd/mcp/main.go buildDeps),
//   - the tools read exactly what the application layer wrote: the seed drives
//     the SAME use cases cmd/api wires (ExpectPackage plans the load the way
//     the fulfillment consumer does, SweepMissedLoads flags the miss the way
//     POST /missed-load-sweeps does, ProjectBoard folds the board the way the
//     analytics consumer does — cmd/api/wire.go),
//   - loads_at_risk and missed_loads return the documented rows
//     (docs/docs/mcp/tools.md, Outputs),
//   - the resource warehouse://outbound-shipping/loads/{id} answers as
//     GET /loads/{id} does,
//   - a blank site_id is the documented invalid-site-id error, returned as an
//     isError tool result (never a protocol failure),
//   - the read-only surface adds no outbox row: "cmd/mcp builds no event sink,
//     no unit of work and no outbox repository, so it cannot write".

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	// The database this test uses is booted by internal/testing/pgtest; the
	// blank import keeps the testcontainers rule visible to the package
	// (TestPostgresIntegrationTestsUseTestcontainers).
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	inboundmcp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/mcp"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/postgres"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/system"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

func TestMCPReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.EmptyURL(t) // the binary migrates it itself
	ctx := context.Background()

	// run() reads DATABASE_URL from the environment and hands it to
	// buildDeps; the migrations DSN comes from the environment too
	// (postgres.MigrationsURL honours MIGRATIONS_DATABASE_URL, default
	// DATABASE_URL), so the test sets the variable exactly as documented.
	t.Setenv("DATABASE_URL", url)

	seed := seedOverTheApplicationsOwnUseCases(t, url)
	before := seed.outboxRows(t)

	deps, closeAdapters, err := buildDeps(url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeAdapters)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "outbound-shipping-itest", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v text=%q", name, err, textOf(res))
		}
		m, _ := res.StructuredContent.(map[string]any)
		return m
	}

	// loads_at_risk: the open load whose cutoff falls within the horizon,
	// ordered by cutoff, with the board's counts (tools.md, Output).
	rows := call("loads_at_risk", map[string]any{"site_id": "SITE-9"})["rows"].([]any)
	var atRisk map[string]any
	for _, r := range rows {
		if m, _ := r.(map[string]any); m["cpt_id"] == "cpt-atrisk" {
			atRisk = m
		}
	}
	if atRisk == nil {
		t.Fatalf("loads_at_risk rows = %v", rows)
	}
	if atRisk["load_id"] != seed.atRiskLoad || atRisk["staged_count"].(float64) != 0 ||
		atRisk["loaded_count"].(float64) != 0 || atRisk["packages_missed"].(float64) != 0 {
		t.Fatalf("the at-risk row = %v", atRisk)
	}
	if m := atRisk["minutes_left"].(float64); m <= 0 || m > 240 {
		t.Fatalf("minutes_left = %v, want within the 4h horizon", atRisk["minutes_left"])
	}

	// missed_loads: the packages that raised PackageMissedLoad, newest
	// first, with the ADR 0003 correlation.
	mrows := call("missed_loads", map[string]any{"site_id": "SITE-9"})["rows"].([]any)
	if len(mrows) != 1 {
		t.Fatalf("missed_loads rows = %v", mrows)
	}
	missed, _ := mrows[0].(map[string]any)
	if missed["package_id"] != "pkg-mcp-missed" || missed["load_id"] != seed.missedLoad ||
		missed["source_order_id"] != "ord-mcp-2" || missed["state"] == "" {
		t.Fatalf("the missed row = %v", missed)
	}

	// The resource: one load as GET /loads/{id} returns it.
	res, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "warehouse://outbound-shipping/loads/" + seed.atRiskLoad})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	var load map[string]any
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &load); err != nil {
		t.Fatalf("resource body: %v", err)
	}
	if load["id"] != seed.atRiskLoad || load["state"] != "PLANNED" || load["cpt_id"] != "cpt-atrisk" ||
		load["ship_method"] != "STDOUT-2DAY" || load["version"].(float64) < 1 {
		t.Fatalf("the load resource = %v", load)
	}

	// A blank site is the documented stable error code, as an isError tool
	// result carrying the error text — never a protocol failure. (An omitted
	// site_id never reaches the handler: the tool's input schema requires
	// the property, so the blank string is the documented failing input.)
	bad, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "missed_loads", Arguments: map[string]any{"site_id": ""}})
	if err != nil {
		t.Fatalf("a tool error must not fail the protocol: %v", err)
	}
	if !bad.IsError || !strings.Contains(textOf(bad), "invalid-site-id") {
		raw, _ := json.Marshal(bad.Content) // never %v: Content is a slice of interface pointers
		t.Fatalf("blank site_id result: isError=%v content=%s", bad.IsError, raw)
	}

	if after := seed.outboxRows(t); after != before {
		t.Fatalf("outbox rows %d -> %d: the read-only MCP surface wrote an event", before, after)
	}
}

// textOf extracts a tool result's text.
func textOf(res *sdk.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// seed is what the application layer wrote before the MCP server read it.
type seed struct {
	pool       *pgxpool.Pool
	atRiskLoad string
	missedLoad string
}

// seedOverTheApplicationsOwnUseCases writes through the same use cases cmd/api
// wires (cmd/api/wire.go): a CPT schedule copy, two manifests (one load whose
// cutoff is within the horizon, one whose cutoff has passed), the missed-load
// sweep, and the board folds the projector would apply. This repo's MCP
// surface is read-only by construction, so there is no write path to drive.
func seedOverTheApplicationsOwnUseCases(t *testing.T, url string) *seed {
	t.Helper()
	ctx := context.Background()

	// buildDeps migrates an EMPTY database; seed only after that is proven
	// by building the repositories over the migrated schema once.
	_, closeAdapters, err := buildDeps(url)
	if err != nil {
		t.Fatalf("buildDeps (migration): %v", err)
	}
	closeAdapters()

	pool := pgtest.Open(t, url)
	loads := postgres.NewLoadRepo(pool)
	pkgs := postgres.NewPackageRepo(pool)
	copies := postgres.NewCopies(pool)
	processed := postgres.NewProcessedEventRepo(pool)
	board := postgres.NewBoardStore(pool)
	sink := events.NewSink(postgres.NewOutboxRepo(pool))
	intake := usecases.Intake{UoW: postgres.NewUnitOfWork(pool), Processed: processed}

	now := time.Now().UTC()
	atRisk := now.Add(90 * time.Minute).Truncate(time.Minute)
	passed := now.Add(-26 * time.Hour).Truncate(time.Minute)
	if err := copies.Save(ctx, repository.CptSchedule{
		SiteID: "SITE-9", Timezone: "UTC", Version: 1, Changed: now,
		Cutoffs: []repository.CutoffEntry{
			{CptID: "cpt-atrisk", LocalTime: atRisk.Format("15:04"), ShipMethod: "STDOUT-2DAY"},
			{CptID: "cpt-passed", LocalTime: passed.Format("15:04"), ShipMethod: "STDOUT-1DAY"},
		},
	}); err != nil {
		t.Fatalf("save schedule copy: %v", err)
	}

	expect := &usecases.ExpectPackage{
		Intake: intake, Loads: loads, Packages: pkgs, Copies: copies,
		Sink: sink, IDs: system.RandomIDs{}, Clock: system.Clock{},
	}
	for _, m := range []usecases.Manifest{
		{PackageID: "pkg-mcp-atrisk", SiteID: "SITE-9", SourceOrderID: "ord-mcp-1", OrderRef: "W-9001",
			Cutoff: atRisk, WeightG: 1500, ManifestedAt: now},
		{PackageID: "pkg-mcp-missed", SiteID: "SITE-9", SourceOrderID: "ord-mcp-2", OrderRef: "W-9002",
			Cutoff: passed, WeightG: 900, ManifestedAt: now},
	} {
		if _, err := expect.Handle(ctx, "seed-"+string(m.PackageID), m); err != nil {
			t.Fatalf("expect %s: %v", m.PackageID, err)
		}
	}

	// The sweep flags the passed-cutoff package (POST /missed-load-sweeps's
	// use case).
	sweep := &usecases.SweepMissedLoads{UoW: intake.UoW, Pkgs: pkgs, Sink: sink, Clock: system.Clock{}}
	if flagged, _, err := sweep.Handle(ctx, "SITE-9", time.Time{}); err != nil || flagged != 1 {
		t.Fatalf("sweep flagged %d (%v), want 1", flagged, err)
	}

	// The board folds the analytics consumer would apply.
	page, err := loads.List(ctx, repository.LoadFilter{Site: "SITE-9"}, shared.LoadID(""), 10)
	if err != nil {
		t.Fatalf("list loads: %v", err)
	}
	s := &seed{pool: pool}
	for _, l := range page {
		switch l.CptID() {
		case "cpt-atrisk":
			s.atRiskLoad = string(l.ID())
		case "cpt-passed":
			s.missedLoad = string(l.ID())
		}
	}
	if s.atRiskLoad == "" || s.missedLoad == "" {
		t.Fatalf("the seeded loads = %+v", s)
	}
	project := &usecases.ProjectBoard{Intake: intake, Board: board}
	folds := []usecases.BoardFold{
		{Name: "LoadPlanned", Load: shared.LoadID(s.atRiskLoad), Site: "SITE-9", Cpt: "cpt-atrisk",
			Date: atRisk.Format("2006-01-02"), Method: "STDOUT-2DAY", Cutoff: atRisk, At: now},
		{Name: "LoadPlanned", Load: shared.LoadID(s.missedLoad), Site: "SITE-9", Cpt: "cpt-passed",
			Date: passed.Format("2006-01-02"), Method: "STDOUT-1DAY", Cutoff: passed, At: now},
		{Name: "PackageMissedLoad", Load: shared.LoadID(s.missedLoad), Site: "SITE-9", At: now},
	}
	for i, f := range folds {
		if _, err := project.Handle(ctx, fmt.Sprintf("seed-fold-%d", i), f); err != nil {
			t.Fatalf("fold %s: %v", f.Name, err)
		}
	}
	return s
}

// outboxRows counts the transactional outbox: the read-only proof.
func (s *seed) outboxRows(t *testing.T) int {
	t.Helper()
	return pgtest.Count(t, s.pool, "outbox_events")
}
