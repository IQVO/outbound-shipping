package mcp

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP surface of outbound-shipping is read-only (server.go, tools.go):
// every mutating operation stays on REST. These tests pin that through the
// real tools/list a client sees, so a write tool cannot be added by accident.
// The static authority sensors live in internal/architecture
// (TestEveryMCPToolDeclaresAuthority, TestMCPWriteToolsAreAtLeastL3); this file
// pins the exact tool set, the read-only annotations and the verb vocabulary.

// writeVerbs are the tokens of a tool name that mark a mutation. A tool name
// is split on '_' and each token is compared whole, so "loads_at_risk" (noun
// "loads") is fine while "load_package" or "close_load" fail.
var writeVerbs = map[string]bool{
	"add": true, "assign": true, "book": true, "cancel": true, "close": true,
	"complete": true, "confirm": true, "create": true, "delete": true,
	"depart": true, "dispatch": true, "load": true, "mark": true,
	"publish": true, "put": true, "remove": true, "reopen": true,
	"reschedule": true, "set": true, "ship": true, "stage": true,
	"sweep": true, "unload": true, "update": true, "void": true, "write": true,
}

// wantTools is the complete, expected read-only tool set.
var wantTools = []string{"loads_at_risk", "missed_loads"}

// writeVerbIn returns the write verb found in a tool name, or "".
func writeVerbIn(name string) string {
	for _, tok := range strings.Split(strings.ToLower(name), "_") {
		if writeVerbs[tok] {
			return tok
		}
	}
	return ""
}

// listTools connects an in-memory client to the real server and returns the
// tools/list result. Deps carries no queries: listing never runs a handler.
func listTools(t *testing.T) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	server := NewServer(Deps{})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "governance-test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	return res.Tools
}

// TestGovernance_ToolSetIsPinnedAndHasNoWriteVerb: the registered tools are
// exactly wantTools and none of their names carries a write verb.
func TestGovernance_ToolSetIsPinnedAndHasNoWriteVerb(t *testing.T) {
	var got []string
	for _, tool := range listTools(t) {
		got = append(got, tool.Name)
		if v := writeVerbIn(tool.Name); v != "" {
			t.Errorf("tool %q carries the write verb %q: mutations stay on REST", tool.Name, v)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("tool set = %v, want %v (a new tool needs an ADR and an update of this pin)", got, wantTools)
	}
}

// TestGovernance_EveryToolIsReadOnlyIdempotentClosedWorldL1: each tool is
// annotated read-only, idempotent, closed-world, non-destructive and declares
// authority level L1 in its tools/list _meta.
func TestGovernance_EveryToolIsReadOnlyIdempotentClosedWorldL1(t *testing.T) {
	tools := listTools(t)
	if len(tools) == 0 {
		t.Fatal("tools/list returned no tool")
	}
	for _, tool := range tools {
		a := tool.Annotations
		if a == nil {
			t.Errorf("tool %q has no annotations", tool.Name)
			continue
		}
		if !a.ReadOnlyHint {
			t.Errorf("tool %q is not ReadOnlyHint", tool.Name)
		}
		if !a.IdempotentHint {
			t.Errorf("tool %q is not IdempotentHint", tool.Name)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("tool %q is not closed-world (OpenWorldHint must be false)", tool.Name)
		}
		if a.DestructiveHint != nil && *a.DestructiveHint {
			t.Errorf("tool %q is destructive", tool.Name)
		}
		if lvl, _ := tool.Meta[authorityMetaKey].(string); lvl != authorityL1 {
			t.Errorf("tool %q authority = %q, want %q", tool.Name, lvl, authorityL1)
		}
	}
}

// TestGovernance_WriteVerbDetectorBites proves the verb check fails on write
// names and passes the real ones, so the pin above cannot go vacuous.
func TestGovernance_WriteVerbDetectorBites(t *testing.T) {
	for _, bad := range []string{"close_load", "stage_package", "void_bol", "cancel_load", "sweep_missed_loads", "Reopen_Load", "load_package"} {
		if writeVerbIn(bad) == "" {
			t.Errorf("%q must be refused as a write-verb name", bad)
		}
	}
	for _, good := range wantTools {
		if v := writeVerbIn(good); v != "" {
			t.Errorf("%q must pass, got verb %q", good, v)
		}
	}
}
