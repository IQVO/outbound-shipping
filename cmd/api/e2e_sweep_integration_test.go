//go:build integration

package main

// The missed-load half of the documented surface, split out of the happy-path
// journey file only to keep both under the file budget: the same harness, the
// same doc citations (docs/docs/ddd/use-cases.md SweepMissedLoads,
// docs/docs/api-reference/events.md PackageMissedLoad,
// docs/docs/api-reference/rest/run-missed-load-sweeps.api.mdx,
// docs/docs/api-reference/rest/get-load-adherence-report.api.mdx).

import (
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/testing/kafkatest"
	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

// TestE2EMissedLoadSweepJourney walks the documented missed-load path: a
// package still EXPECTED on an open load whose cutoff has passed is flagged
// by the sweep exactly once (SweepMissedLoads), the package raises
// PackageMissedLoad with its order correlation and cutoff, the load is left
// for a supervisor, and the board counts the miss.
func TestE2EMissedLoadSweepJourney(t *testing.T) {
	// This boot replays the shared topics' history into its own fresh
	// database (a brand-new consumer group), so read only what arrives from
	// here on.
	offset := kafkatest.EndOffset(t, topicIntegration)
	env, group := apiEnv(t, pgtest.URL(t))
	svc := bootAPI(t, env, group)

	yesterday09 := time.Now().UTC().AddDate(0, 0, -1).Truncate(24 * time.Hour).Add(9 * time.Hour)
	cptStr := yesterday09.Format(time.RFC3339)
	opDate := yesterday09.Format("2006-01-02")

	kafkatest.Produce(t, "warehouse.process-path-management.events",
		fixtureEvent("it-cpt-m", "/warehouse/process-path-management", typCptChanged, "SITE-2",
			`{"site_id":"SITE-2","timezone":"UTC","cutoffs":[`+
				`{"cpt_id":"cpt-0900","local_time":"09:00","days_of_week":[],"ship_method":"STDOUT-1DAY"}]}`))
	// This boot's cpt group also replays the first journey's schedule
	// event, so its own SITE-2 copy is the SECOND commit on that topic.
	svc.waitCommitted(t, "cpt", "warehouse.process-path-management.events", 2)
	kafkatest.Produce(t, "warehouse.fulfillment.events",
		fixtureEvent("it-man-m", "/warehouse/fulfillment-execution", typManifested, "pkg-e2e-m",
			fmt.Sprintf(`{"package_id":"pkg-e2e-m","order_ref":"W-3001","site_id":"SITE-2",`+
				`"source_order_id":"ord-e2e-m","cpt":%q,"weight_g":700}`, cptStr)))

	var loadID string
	eventually(t, 60*time.Second, "the manifested package to plan the SITE-2 load", func() bool {
		r := svc.call(t, "GET", "/loads?site_id=SITE-2", "", "")
		if r.status != 200 {
			return false
		}
		items, _ := r.json(t)["items"].([]any)
		for _, it := range items {
			if l, _ := it.(map[string]any); l["state"] == "PLANNED" {
				loadID, _ = l["id"].(string)
			}
		}
		return loadID != ""
	})

	// The sweep flags the package once; a re-run flags nothing and
	// publishes nothing (SweepMissedLoads invariants).
	if r := svc.call(t, "POST", "/missed-load-sweeps", "sweep-1", `{"site_id":"SITE-2"}`); r.status != 200 ||
		r.json(t)["flagged"].(float64) != 1 {
		t.Fatalf("sweep = %d %s", r.status, r.body)
	}
	if r := svc.call(t, "POST", "/missed-load-sweeps", "sweep-2", `{"site_id":"SITE-2"}`); r.status != 200 ||
		r.json(t)["flagged"].(float64) != 0 {
		t.Fatalf("re-run sweep = %d %s", r.status, r.body)
	}

	// The load is left for a supervisor: still open, never cancelled.
	if r := svc.call(t, "GET", "/loads/"+loadID, "", ""); r.status != 200 || r.json(t)["state"] != "PLANNED" {
		t.Fatalf("the swept load = %d %s", r.status, r.body)
	}

	// PackageMissedLoad carries the package's cutoff and correlation
	// (events.md), deduped by id.
	msgs := awaitEvents(t, topicIntegration, offset, 90*time.Second, func(msgs []kafkatest.Message) bool {
		for _, e := range dedupeEvents(t, msgs, "events") {
			if e.typ == ceTypePrefix+"shippingpackage.PackageMissedLoad" && e.data["package_id"] == "pkg-e2e-m" {
				return true
			}
		}
		return false
	})
	var missed *cloudEvent
	for _, e := range dedupeEvents(t, msgs, "events") {
		if e.typ == ceTypePrefix+"shippingpackage.PackageMissedLoad" && e.data["package_id"] == "pkg-e2e-m" {
			m := e
			missed = &m
		}
	}
	if missed == nil {
		t.Fatal("no PackageMissedLoad for pkg-e2e-m")
	}
	if missed.subject != "pkg-e2e-m" || missed.data["load_id"] != loadID || missed.data["source_order_id"] != "ord-e2e-m" ||
		missed.data["cutoff"] != cptStr || missed.data["site_id"] != "SITE-2" {
		t.Fatalf("PackageMissedLoad = %v", missed.data)
	}

	// The board counts the miss (the projector's PackageMissedLoad fold).
	eventuallyDetail(t, 60*time.Second, "the board to count the miss", func() (bool, string) {
		r := svc.call(t, "GET", "/reports/load-adherence?site_id=SITE-2&operating_date="+opDate, "", "")
		if r.status != 200 {
			return false, "adherence report = " + r.body
		}
		for _, row := range r.json(t)["rows"].([]any) {
			m, _ := row.(map[string]any)
			if m["cpt_id"] == "cpt-0900" {
				return m["loads_total"].(float64) == 1 && m["loads_closed"].(float64) == 0 &&
						m["packages_missed"].(float64) == 1 && m["adherence"].(float64) == 0,
					"the cpt-0900 adherence row = " + r.body
			}
		}
		return false, "no cpt-0900 row in " + r.body
	})
}
