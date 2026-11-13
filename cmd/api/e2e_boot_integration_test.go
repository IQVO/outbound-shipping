//go:build integration

package main

// TestAPIBootsOnAnEmptyDatabaseAndDrainsCleanly proves the boot contract of
// cmd/api against real infrastructure:
//
//   - the binary itself runs the migrations on an EMPTY database ("migrations
//     run at boot", cmd/api/main.go Environment note; wire() calls
//     postgres.RunMigrations before anything else),
//   - /readyz answers 200 with both dependencies (Postgres, the Kafka TCP
//     dial) up and /healthz answers 200 {"status":"ok"}
//     (internal/adapters/inbound/http/readiness.go; docs/docs/api-reference/
//     rest/get-readiness.api.mdx),
//   - a SIGTERM — what a deployment sends — drains cleanly: run() returns
//     nil within its bounds (readiness flips first, the consumers stop, the
//     outbox relay LAST, then the HTTP listener; cmd/api/main.go and
//     cmd/api/consumers.go runConsumers).
//
// Mirrors checkout's e2e_boot_integration_test.go shape (boot → readyz →
// cancel → clean return), adapted to this repo's run() which owns its own
// signal context.

import (
	"net/http"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

func TestAPIBootsOnAnEmptyDatabaseAndDrainsCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t) // fresh database, NO migrations applied yet
	env, group := apiEnv(t, url)
	svc := bootAPI(t, env, group)

	if r := svc.call(t, "GET", "/healthz", "", ""); r.status != 200 || r.json(t)["status"] != "ok" {
		t.Fatalf("GET /healthz = %d %s", r.status, r.body)
	}
	if r := svc.call(t, "GET", "/readyz", "", ""); r.status != 200 || r.json(t)["status"] != "ready" {
		t.Fatalf("GET /readyz = %d %s", r.status, r.body)
	}

	// The migrations really ran: the runtime's own tables exist and are
	// empty (the transactional outbox and the load table of the aggregate
	// this context owns).
	pool := pgtest.Open(t, url)
	if n := pgtest.Count(t, pool, "outbox_events"); n != 0 {
		t.Fatalf("outbox_events holds %d rows on a fresh boot", n)
	}
	if n := pgtest.Count(t, pool, "outbound_loads"); n != 0 {
		t.Fatalf("outbound_loads holds %d rows on a fresh boot", n)
	}

	if err := svc.stop(); err != nil {
		t.Fatalf("the api did not drain cleanly: %v", err)
	}
	// The listener is gone after the drain (call reports transport errors).
	eventually(t, 10*time.Second, "the listener to close", func() bool {
		resp, err := http.Get(svc.base + "/readyz")
		if err != nil {
			return true
		}
		_ = resp.Body.Close()
		return false
	})
}
