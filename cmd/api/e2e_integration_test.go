//go:build integration

package main

// The end-to-end proof that the shipped cmd/api is really wired: the test
// boots the REAL run() over testcontainers Postgres and Kafka with nothing
// but the documented environment, drives it ONLY through the documented
// REST surface and the consumed Kafka topics, and asserts through REST reads,
// the published CloudEvents on warehouse.outbound-shipping.events and the
// projector-fed report. Every step cites the doc that states it:
//
//   - docs/docs/ddd/use-cases.md (the seventeen use cases, their triggers,
//     invariants and raised events),
//   - docs/docs/api-reference/rest/*.api.mdx (routes, bodies, status codes),
//   - docs/docs/api-reference/events.md (the CloudEvents envelope, the 13
//     published types with payloads, the consumed types with fields read),
//   - docs/docs/ecosystem/integration.md (the six consumers, the outbox
//     relay, at-least-once with a stable id),
//   - docs/adr/0002 (ship-confirm follows the departure),
//   - docs/adr/0003 (PackageManifested carries the order correlation),
//   - docs/adr/0005 (CloudEvents 1.0 envelope and type catalogue).
//
// The missed-load sweep journey lives in e2e_sweep_integration_test.go, on
// the same harness (e2e_harness_test.go).

import (
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/outbound-shipping/internal/testing/kafkatest"
	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

// The consumed types, byte-identical to the producers' pinned contracts
// (docs/docs/api-reference/events.md, "Consumed events").
const (
	typManifested = "com.warehouse.wes.fulfillment-execution.package.PackageManifested"
	typCptChanged = "com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged"
	typZone       = "com.warehouse.wms.facility-layout.zone.ZoneRegistered"
	typLocation   = "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"
	typAppt       = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked"
	typDocked     = "com.warehouse.wms.yard-management.trailer.TrailerDocked"
	typDeparted   = "com.warehouse.wms.yard-management.trailer.TrailerDeparted"
)

// TestE2EShipConfirmJourney walks the documented happy path of one load:
// manifests plan it, an appointment names its door, a docked trailer starts
// loading, the dock scans stage and load the packages (with one unload and
// re-load), the supervisor closes it, the departure with a seal confirms the
// shipment and issues the bill of lading, the supervisor voids the document,
// and everything the use cases raised leaves through the outbox relay as
// CloudEvents on the integration topic (with its analytics twin).
func TestE2EShipConfirmJourney(t *testing.T) {
	env, group := apiEnv(t, pgtest.URL(t))
	svc := bootAPI(t, env, group)

	tomorrow16 := time.Now().UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour).Add(16 * time.Hour)
	cptStr := tomorrow16.Format(time.RFC3339)
	opDate := tomorrow16.Format("2006-01-02")

	// The local copies the consumers keep: the site's CPT schedule
	// (ApplyCptSchedule) and the staging location (ApplyLayout, role
	// "Staging" — repository.RoleStaging).
	kafkatest.Produce(t, "warehouse.process-path-management.events",
		fixtureEvent("it-cpt-1", "/warehouse/process-path-management", typCptChanged, "SITE-1",
			`{"site_id":"SITE-1","timezone":"UTC","cutoffs":[`+
				`{"cpt_id":"cpt-1600","local_time":"16:00","days_of_week":[],"ship_method":"STDOUT-2DAY"}]}`))
	kafkatest.Produce(t, "warehouse.facility.events",
		fixtureEvent("it-zone-1", "/warehouse/facility-layout", typZone, "zone-1",
			`{"zoneId":"zone-1","siteCode":"SITE-1","zoneCode":"Z-OUT"}`),
		fixtureEvent("it-loc-1", "/warehouse/facility-layout", typLocation, "STG-01",
			`{"locationCode":"STG-01","zoneId":"zone-1","role":"Staging"}`))

	// The local copies must be IN before the manifests: a manifest whose
	// site has no schedule copy yet is ignored (use-cases.md, ExpectPackage
	// "Ignored when"), so wait for both groups' commits.
	svc.waitCommitted(t, "cpt", "warehouse.process-path-management.events", 1)
	svc.waitCommitted(t, "layout", "warehouse.facility.events", 2)

	// Two manifests of the same cutoff key share one load (ExpectPackage);
	// each carries its order correlation (ADR 0003).
	for i, ord := range []string{"ord-e2e-1", "ord-e2e-2"} {
		kafkatest.Produce(t, "warehouse.fulfillment.events",
			fixtureEvent(fmt.Sprintf("it-man-%d", i), "/warehouse/fulfillment-execution", typManifested, "pkg-e2e-"+fmt.Sprint(i+1),
				fmt.Sprintf(`{"package_id":"pkg-e2e-%d","order_ref":"W-200%d","site_id":"SITE-1",`+
					`"source_order_id":%q,"cpt":%q,"weight_g":%d}`, i+1, i+1, ord, cptStr, 1200-i*400)))
	}

	// The manifest plans the load (LoadPlanned; the second one joins it).
	var loadID string
	eventually(t, 60*time.Second, "the manifested packages to plan the load", func() bool {
		r := svc.call(t, "GET", "/loads?site_id=SITE-1", "", "")
		if r.status != 200 {
			return false
		}
		items, _ := r.json(t)["items"].([]any)
		for _, it := range items {
			l, _ := it.(map[string]any)
			if l["state"] == "PLANNED" {
				loadID, _ = l["id"].(string)
			}
		}
		return loadID != ""
	})
	if r := svc.call(t, "GET", "/loads/"+loadID, "", ""); r.status != 200 {
		t.Fatalf("GET /loads/%s = %d %s", loadID, r.status, r.body)
	} else if l := r.json(t); l["cpt_id"] != "cpt-1600" || l["ship_method"] != "STDOUT-2DAY" || l["cutoff"] != cptStr {
		t.Fatalf("the planned load's cutoff key = %v", l)
	}

	// An OUTBOUND appointment naming the cpt assigns the door and keeps the
	// carrier for the bill of lading (BookAppointment).
	kafkatest.Produce(t, "warehouse.dock-scheduling.events",
		fixtureEvent("it-appt-1", "/warehouse/dock-scheduling", typAppt, "appt-e2e-1",
			fmt.Sprintf(`{"appointment_id":"appt-e2e-1","site_id":"SITE-1","direction":"OUTBOUND",`+
				`"door_code":"D-12","carrier":"Northern Freight","carrier_scac":"NFRT","cpt_id":"cpt-1600",`+
				`"window_start":%q,"window_end":%q,"version":1}`, cptStr, cptStr)))
	eventually(t, 30*time.Second, "the appointment to assign the door", func() bool {
		r := svc.call(t, "GET", "/loads/"+loadID, "", "")
		return r.status == 200 && r.json(t)["door_code"] == "D-12"
	})

	// The trailer docking at that door attaches it and starts loading
	// (DockTrailer: there is no REST route to start loading).
	kafkatest.Produce(t, "warehouse.yard-management.events",
		fixtureEvent("it-dock-1", "/warehouse/yard-management", typDocked, "trl-e2e-1",
			fmt.Sprintf(`{"trailer_id":"trl-e2e-1","site_id":"SITE-1","door_code":"D-12","docked_at":%q}`,
				time.Now().UTC().Format(time.RFC3339))))
	eventually(t, 30*time.Second, "the docked trailer to start loading", func() bool {
		r := svc.call(t, "GET", "/loads/"+loadID, "", "")
		return r.status == 200 && r.json(t)["state"] == "LOADING" && r.json(t)["trailer_id"] == "trl-e2e-1"
	})

	// Every POST is behind the Idempotency-Key middleware (AGENTS.md item 9,
	// use-cases.md error catalogue): a missing key is 400, a reused key with
	// a different request is 409, both as RFC 7807 problem documents.
	if r := svc.call(t, "POST", "/packages/pkg-e2e-1/stagings", "", `{"staging_location":"STG-01"}`); r.status != 400 {
		t.Fatalf("staging without an Idempotency-Key = %d %s, want 400", r.status, r.body)
	} else if r.json(t)["code"] != "idempotency-key-required" {
		t.Fatalf("problem code = %v", r.json(t)["code"])
	}
	// Stage both packages at the layout's Staging location; the response
	// carries the ADR 0003 order correlation.
	for i := 1; i <= 2; i++ {
		r := svc.call(t, "POST", fmt.Sprintf("/packages/pkg-e2e-%d/stagings", i), fmt.Sprintf("stage-%d", i), `{"staging_location":"STG-01"}`)
		if r.status != 200 {
			t.Fatalf("stage pkg-e2e-%d = %d %s", i, r.status, r.body)
		}
		p := r.json(t)
		if p["state"] != "STAGED" || p["staging_location"] != "STG-01" || p["load_id"] != loadID {
			t.Fatalf("staged pkg-e2e-%d = %v", i, p)
		}
		if v, _ := p["version"].(float64); v != 2 {
			t.Fatalf("staged pkg-e2e-%d version = %v, want 2 (manifest 1, stage 2)", i, p["version"])
		}
	}
	// A replayed key re-stages nothing: 200 from the stored execution,
	// flagged by the documented header; the same key with a DIFFERENT body
	// is the 409 reuse refusal.
	if r := svc.call(t, "POST", "/packages/pkg-e2e-1/stagings", "stage-1", `{"staging_location":"STG-01"}`); r.status != 200 ||
		r.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replayed staging = %d (replayed=%q) %s", r.status, r.header.Get("Idempotency-Replayed"), r.body)
	}
	if r := svc.call(t, "POST", "/packages/pkg-e2e-1/stagings", "stage-1", `{"staging_location":"STG-02"}`); r.status != 409 ||
		r.json(t)["code"] != "idempotency-key-reused" {
		t.Fatalf("a reused key with a different body = %d %s, want 409", r.status, r.body)
	}

	// Loading goes only through the load's own door: another door is the
	// documented 409 wrong-load.
	if r := svc.call(t, "POST", "/packages/pkg-e2e-1/loadings", "load-wrong", `{"load_id":"`+loadID+`","door_code":"D-99"}`); r.status != 409 ||
		r.json(t)["code"] != "wrong-load" {
		t.Fatalf("loading through a wrong door = %d %s, want 409 wrong-load", r.status, r.body)
	}

	loadBody := `{"load_id":"` + loadID + `","door_code":"D-12"}`
	steps := []struct {
		key, path, body, wantState string
	}{
		{"load-1", "/packages/pkg-e2e-1/loadings", loadBody, "LOADED"},                  // pkg 1: staged -> loaded
		{"load-2a", "/packages/pkg-e2e-2/loadings", loadBody, "LOADED"},                 // pkg 2: staged -> loaded
		{"unload-2", "/packages/pkg-e2e-2/unloadings", `{"reason":"rescan"}`, "STAGED"}, // taken back off
		{"load-2b", "/packages/pkg-e2e-2/loadings", loadBody, "LOADED"},                 // pkg 2: loaded again
	}
	for _, st := range steps {
		if r := svc.call(t, "POST", st.path, st.key, st.body); r.status != 200 || r.json(t)["state"] != st.wantState {
			t.Fatalf("%s = %d %s, want %s", st.key, r.status, r.body, st.wantState)
		}
	}

	// Close: every package is loaded, so the closure succeeds with both
	// counts (CloseLoad).
	if r := svc.call(t, "POST", "/loads/"+loadID+"/closures", "close-1", `{}`); r.status != 200 || r.json(t)["state"] != "CLOSED" {
		t.Fatalf("close = %d %s", r.status, r.body)
	}

	// The departure of the docked trailer with the yard's seal confirms the
	// shipment (ADR 0002): the load goes SHIP_CONFIRMED, every loaded
	// package SHIPPED, one bill of lading issued.
	kafkatest.Produce(t, "warehouse.yard-management.events",
		fixtureEvent("it-dep-1", "/warehouse/yard-management", typDeparted, "trl-e2e-1",
			fmt.Sprintf(`{"trailer_id":"trl-e2e-1","site_id":"SITE-1","departed_at":%q,"seal_number":"SEAL-0042"}`,
				time.Now().UTC().Format(time.RFC3339))))
	eventually(t, 60*time.Second, "the departure to confirm the shipment", func() bool {
		r := svc.call(t, "GET", "/loads/"+loadID, "", "")
		return r.status == 200 && r.json(t)["state"] == "SHIP_CONFIRMED"
	})
	if r := svc.call(t, "GET", "/loads/"+loadID+"/packages?state=SHIPPED", "", ""); r.status != 200 {
		t.Fatalf("load packages = %d %s", r.status, r.body)
	} else if items := r.json(t)["items"].([]any); len(items) != 2 {
		t.Fatalf("shipped packages = %v", r.json(t))
	}

	// The bill of lading: carrier and SCAC read from the appointment copy,
	// the seal, the package count and the total weight (OnDeparture). The
	// number is minted at issue, so the test learns it from the event.
	bolProbe := awaitEvents(t, topicIntegration, 0, 60*time.Second, func(msgs []kafkatest.Message) bool {
		for _, e := range dedupeEvents(t, msgs, "events") {
			if e.typ == ceTypePrefix+"billoflading.BillOfLadingIssued" {
				return true
			}
		}
		return false
	})
	var bolNumber string
	for _, e := range dedupeEvents(t, bolProbe, "events") {
		if e.typ == ceTypePrefix+"billoflading.BillOfLadingIssued" {
			bolNumber = e.subject
		}
	}
	if r := svc.call(t, "GET", "/bills-of-lading/"+bolNumber, "", ""); r.status != 200 {
		t.Fatalf("GET bill of lading = %d %s", r.status, r.body)
	} else if b := r.json(t); b["state"] != "ISSUED" || b["load_id"] != loadID || b["carrier"] != "Northern Freight" {
		t.Fatalf("bill of lading = %v", b)
	}
	// Voiding the document does not undo the shipment (VoidBillOfLading).
	if r := svc.call(t, "POST", "/bills-of-lading/"+bolNumber+"/voids", "void-1", `{"reason":"printed twice"}`); r.status != 200 ||
		r.json(t)["state"] != "VOIDED" {
		t.Fatalf("void = %d %s", r.status, r.body)
	}

	// Everything the use cases raised left through the outbox relay (the
	// only path out, AGENTS.md item 3): the exact multiset of the journey
	// up to the confirmation, deduped by CloudEvents id (at-least-once);
	// the void's own event is read after it below.
	wantCounts := map[string]int{
		"outboundload.LoadPlanned": 1, "outboundload.LoadDoorAssigned": 1, "outboundload.LoadingStarted": 1,
		"shippingpackage.PackageStaged": 2, "shippingpackage.PackageLoaded": 3, "shippingpackage.PackageUnloaded": 1,
		"outboundload.LoadClosed": 1, "outboundload.ShipmentConfirmed": 1,
		"billoflading.BillOfLadingIssued": 1,
	}
	msgs := awaitEvents(t, topicIntegration, 0, 120*time.Second, func(msgs []kafkatest.Message) bool {
		counts := typeCounts(dedupeEvents(t, msgs, "events"))
		if len(counts) != len(wantCounts) {
			return false
		}
		for k, n := range wantCounts {
			if counts[k] != n {
				return false
			}
		}
		return true
	})
	evs := dedupeEvents(t, msgs, "events")
	byType := map[string][]cloudEvent{}
	for _, e := range evs {
		name := e.typ[len(ceTypePrefix):]
		byType[name] = append(byType[name], e)
	}

	// ShipmentConfirmed lists every distinct source_order_id, sorted (ADR
	// 0003, events.md rules), with the yard's seal and the trailer.
	sc := byType["outboundload.ShipmentConfirmed"][0]
	if got := sc.data["orders"].([]any); len(got) != 2 || got[0].(map[string]any)["order_id"] != "ord-e2e-1" ||
		got[1].(map[string]any)["order_id"] != "ord-e2e-2" {
		t.Fatalf("ShipmentConfirmed.orders = %v", sc.data["orders"])
	}
	if sc.data["seal_number"] != "SEAL-0042" || sc.data["trailer_id"] != "trl-e2e-1" || sc.data["cpt_id"] != "cpt-1600" {
		t.Fatalf("ShipmentConfirmed = %v", sc.data)
	}
	// LoadClosed counts the loaded packages and the short-ships (events.md).
	lc := byType["outboundload.LoadClosed"][0]
	if lc.data["package_count"].(float64) != 2 || lc.data["short_shipped_count"].(float64) != 0 ||
		lc.data["door_code"] != "D-12" || lc.data["trailer_id"] != "trl-e2e-1" {
		t.Fatalf("LoadClosed = %v", lc.data)
	}
	// The unload's reason travels (PackageUnloaded omits it when empty).
	if ul := byType["shippingpackage.PackageUnloaded"][0]; ul.data["reason"] != "rescan" {
		t.Fatalf("PackageUnloaded = %v", ul.data)
	}

	// The bill of lading event's payload: carrier and SCAC read from the
	// appointment copy, the seal, the package count and the total weight.
	bol := byType["billoflading.BillOfLadingIssued"][0]
	if bol.data["carrier"] != "Northern Freight" || bol.data["carrier_scac"] != "NFRT" ||
		bol.data["package_count"].(float64) != 2 || bol.data["total_weight_g"].(float64) != 2000 ||
		bol.data["seal_number"] != "SEAL-0042" || bol.subject != bolNumber {
		t.Fatalf("BillOfLadingIssued = %v", bol.data)
	}
	// The void's own event, read from where the first read stopped: the
	// documented payload carries the supervisor's reason.
	voided := awaitEvents(t, topicIntegration, int64(len(msgs)), 60*time.Second,
		func(msgs []kafkatest.Message) bool {
			for _, e := range dedupeEvents(t, msgs, "events") {
				if e.typ == ceTypePrefix+"billoflading.BillOfLadingVoided" {
					return true
				}
			}
			return false
		})
	for _, e := range dedupeEvents(t, voided, "events") {
		if e.typ == ceTypePrefix+"billoflading.BillOfLadingVoided" {
			if e.subject != bolNumber || e.data["reason"] != "printed twice" || e.data["load_id"] != loadID {
				t.Fatalf("BillOfLadingVoided = %v", e.data)
			}
		}
	}

	// The analytics twin carries the same id, type, subject and key, only
	// the dataschema's stream segment differs (events.md, Topics).
	twin := awaitEvents(t, topicAnalytics, 0, 60*time.Second, func(msgs []kafkatest.Message) bool {
		return len(msgs) >= 1
	})
	av := decodeCloudEvent(t, twin[0], "analytics")
	found := false
	for _, e := range evs {
		if e.id == av.id {
			found = true
			if e.typ != av.typ || e.subject != av.subject || e.dataschema == av.dataschema {
				t.Fatalf("the analytics twin of %s differs beyond dataschema", av.typ)
			}
		}
	}
	if !found {
		t.Fatalf("no integration twin of analytics event %s (%s)", av.id, av.typ)
	}

	// The consumer projection: this service's own analytics consumer folds
	// the journey into load_board, which the adherence report reads
	// (ProjectBoard; AdherenceReport = loads_closed / loads_total).
	eventuallyDetail(t, 60*time.Second, "the board to fold the journey", func() (bool, string) {
		r := svc.call(t, "GET", "/reports/load-adherence?site_id=SITE-1&operating_date="+opDate, "", "")
		if r.status != 200 {
			return false, "adherence report = " + r.body
		}
		for _, row := range r.json(t)["rows"].([]any) {
			m, _ := row.(map[string]any)
			if m["cpt_id"] == "cpt-1600" {
				return m["loads_total"].(float64) == 1 && m["loads_closed"].(float64) == 1 &&
						m["packages_missed"].(float64) == 0 && m["adherence"].(float64) == 1,
					"the cpt-1600 adherence row = " + r.body
			}
		}
		return false, "no cpt-1600 row in " + r.body
	})
}
