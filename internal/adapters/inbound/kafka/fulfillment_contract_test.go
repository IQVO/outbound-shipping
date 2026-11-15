package kafka

// E2 for fulfillment-execution: every documented PackageManifested /
// PackageDiverted example in the pinned contract decodes through the real
// decoder into the struct the consumer reads, and the correlated golden
// example applied through the REAL consumers (cpt first: a manifest can
// only join a load whose CPT schedule copy exists) plans exactly one load,
// id-deduped on replay. The four correlation fields are additive
// (ADR 0003): the minimal example omits them, the correlated one carries
// them, both decode.

import (
	"context"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
)

func TestFulfillmentExamplesDecode(t *testing.T) {
	doc := loadPin(t, fulfillmentPin)
	for _, eventType := range []string{TypePackageManifested, TypePackageDiverted} {
		msg := doc.messageOn(t, fulfillmentPin, FulfillmentTopic, eventType)
		if len(msg.Examples) == 0 {
			t.Fatalf("%s has no example payload in the pinned contract", eventType)
		}
		for _, ex := range msg.Examples {
			t.Run(ex.Name, func(t *testing.T) { checkFulfillmentExample(t, eventType, ex) })
		}
	}
}

// checkFulfillmentExample decodes one documented example through the
// struct the consumer reads and asserts the keys it acts on.
func checkFulfillmentExample(t *testing.T, eventType string, ex pinExample) {
	t.Helper()
	ev := eventFromPayload(t, fulfillmentPin, eventType, ex.Payload)
	if eventType != TypePackageManifested {
		if _, err := decodeData[packageDivertedData](ev); err != nil {
			t.Fatalf("decode %s: %v", ex.Name, err)
		}
		return
	}
	d, err := decodeData[packageManifestedData](ev)
	if err != nil {
		t.Fatalf("decode %s: %v", ex.Name, err)
	}
	if d.PackageID == "" || d.OrderRef == "" {
		t.Errorf("%s leaves the keys the consumer acts on empty: %+v", ex.Name, d)
	}
}

// correlatedManifestEvent returns the PackageManifested golden example that
// carries the additive correlation fields (site_id, cpt, weight_g,
// source_order_id) — the shape ADR 0003 planned loads from — and asserts
// the correlated fields travel together.
func correlatedManifestEvent(t *testing.T, doc pinDoc) (string, map[string]any) {
	t.Helper()
	msg := doc.messageOn(t, fulfillmentPin, FulfillmentTopic, TypePackageManifested)
	for _, ex := range msg.Examples {
		data, _ := ex.Payload["data"].(map[string]any)
		if data == nil {
			t.Fatalf("%s: example carries no data object", ex.Name)
		}
		if site, _ := data["site_id"].(string); site != "" {
			for _, field := range []string{"cpt", "weight_g", "source_order_id"} {
				if _, ok := data[field]; !ok {
					t.Errorf("%s: carries site_id but no %s (keys %v)", ex.Name, field, keysOf(data))
				}
			}
			return ex.Name, ex.Payload
		}
	}
	t.Fatal("no PackageManifested example carries site_id; the correlated shape is undocumented")
	return "", nil
}

// correlatedManifestPayload returns the correlated golden's payload as a
// CloudEvent (see correlatedManifestEvent for the selection rules).
func correlatedManifestPayload(t *testing.T) ce.Event {
	t.Helper()
	doc := loadPin(t, fulfillmentPin)
	_, payload := correlatedManifestEvent(t, doc)
	return eventFromPayload(t, fulfillmentPin, TypePackageManifested, payload)
}

// TestFulfillmentGoldenEventPlansExactlyOneLoad: the correlated golden
// example through the REAL consumers. The CPT schedule copy is seeded by
// an ADAPTED clone of process-path-management's own golden (only the site
// renamed to the manifest's WH1 and a FRESH CloudEvents id: the manifest's
// 18:00Z cpt is exactly the golden schedule's 15:00 Sao_Paulo cutoff): a
// manifest without a schedule copy is ignored by design, and the pinned
// examples name different sites (sp1 vs WH1).
func TestFulfillmentGoldenEventPlansExactlyOneLoad(t *testing.T) {
	rig := newContractRig(t)
	seedScheduleForTheManifest(t, rig)

	doc := loadPin(t, fulfillmentPin)
	name, payload := correlatedManifestEvent(t, doc)
	const eventID = "6c2d3b4a-8e5f-4a6b-9c7d-2b4e6f8a0c1d"
	ev := eventFromPayload(t, fulfillmentPin, TypePackageManifested, payload)
	raw := rawEvent(t, withID(eventID), ev)
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic, raw); err != nil {
		t.Fatalf("dispatch the correlated golden PackageManifested (%s): %v", name, err)
	}
	want := analyticsTypePrefix + "outboundload.LoadPlanned"
	if got := rig.rec.CountByType(want); got != 1 {
		t.Fatalf("the golden manifestation planned %d loads, want exactly 1", got)
	}
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic, raw); err != nil {
		t.Fatalf("replay the golden PackageManifested: %v", err)
	}
	if got := rig.rec.CountByType(want); got != 1 {
		t.Fatalf("the replay left %d planned loads, want the set size unchanged at 1", got)
	}
	if !rig.processed.Has(usecases.FulfillmentConsumer, eventID) {
		t.Fatalf("event id %q was never claimed by the fulfillment inbox", eventID)
	}
}

// TestFulfillmentDivertedGoldenEventIsTolerated: the diverted golden
// example for a package nothing manifested is a no-op (OutcomeIgnored, not
// a poison), and after its package's manifestation a diverted clone with
// a FRESH id applies.
func TestFulfillmentDivertedGoldenEventIsTolerated(t *testing.T) {
	rig := newContractRig(t)
	seedScheduleForTheManifest(t, rig)
	const (
		firstDivertID  = "7d3e4c5b-9f6a-4b7c-8d8e-3c5f7a9b1d2e"
		manifestedID   = "8e4f5d6c-0a7b-4c8d-9e9f-4d6a8b1c2e3f"
		secondDivertID = "9f5a6e7d-1b8c-4d9e-8f0a-5e7b9c2d3f4a"
	)
	diverted := pinnedEvent(t, fulfillmentPin, FulfillmentTopic, TypePackageDiverted)
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic,
		rawEvent(t, withID(firstDivertID), diverted)); err != nil {
		t.Fatalf("an unmanifested diversion must be a no-op, got %v", err)
	}

	manifested := correlatedManifestPayload(t)
	toDivertedPackage := func(p map[string]any) map[string]any {
		p = withID(manifestedID)(p)
		data, _ := p["data"].(map[string]any)
		data["package_id"] = "p1"
		return p
	}
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic,
		rawEvent(t, toDivertedPackage, manifested)); err != nil {
		t.Fatalf("manifest the diverted example's package: %v", err)
	}
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic,
		rawEvent(t, withID(secondDivertID), diverted)); err != nil {
		t.Fatalf("divert the manifested package: %v", err)
	}
	pkg, err := rig.pkgs.Get(context.Background(), shared.PackageID("p1"))
	if err != nil || pkg == nil {
		t.Fatalf("the diverted package must exist after both events: %v", err)
	}
	for _, id := range []string{firstDivertID, manifestedID, secondDivertID} {
		if !rig.processed.Has(usecases.FulfillmentConsumer, id) {
			t.Errorf("event id %q was never claimed by the fulfillment inbox", id)
		}
	}
}

// seedScheduleForTheManifest feeds the cpt consumer the ADAPTED golden
// CPTScheduleChanged clone described above and asserts the copy exists
// (existence on the returned schedule, never on err == nil alone).
func seedScheduleForTheManifest(t *testing.T, rig *contractRig) {
	t.Helper()
	const seedID = "5b1c2d3e-7a4b-4c5d-8e6f-9a1b3c5d7e9f"
	ev := pinnedEvent(t, cptPin, CptTopic, TypeCPTScheduleChanged)
	adapt := func(p map[string]any) map[string]any {
		p = withID(seedID)(p)
		p["subject"] = "WH1"
		data, _ := p["data"].(map[string]any)
		data["site_id"] = "WH1"
		return p
	}
	if err := dispatch(t, rig.cpt, "cpt", CptTopic, rawEvent(t, adapt, ev)); err != nil {
		t.Fatalf("seed the WH1 CPT schedule copy through the real cpt consumer: %v", err)
	}
	schedule, err := rig.copies.Get(context.Background(), shared.SiteID("WH1"))
	if err != nil || schedule.SiteID != "WH1" || len(schedule.Cutoffs) != 1 {
		t.Fatalf("the WH1 schedule copy was not stored: %+v, %v", schedule, err)
	}
	if schedule.Cutoffs[0].LocalTime != "15:00" || schedule.Version != 1 {
		t.Fatalf("the stored copy lost the golden cutoff: %+v", schedule)
	}
	if !rig.processed.Has(usecases.CptConsumer, seedID) {
		t.Fatal("the seed event was never claimed by the cpt inbox")
	}
}
