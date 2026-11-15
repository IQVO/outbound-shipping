package kafka

// E2 for process-path-management and facility-layout: the pinned golden
// examples decode through the real decoder into the structs the consumers
// read, and apply through the REAL consumers into the durable local
// copies. process-path-management documents its own topic under publish:
// (app-perspective, the fleet's flip is not uniform) — E1 in
// contract_pins_test.go already reads the union, this file only decodes.

import (
	"context"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// TestCptGoldenEventDecodesAndApplies: the pinned CPTScheduleChanged
// example carries no version (a real snapshot never did); it must decode,
// apply through the real cpt consumer, and store a storable version
// (cptVersion) instead of being refused.
func TestCptGoldenEventDecodesAndApplies(t *testing.T) {
	rig := newContractRig(t)
	ev := pinnedEvent(t, cptPin, CptTopic, TypeCPTScheduleChanged)
	d, err := decodeData[cptScheduleChangedData](ev)
	if err != nil {
		t.Fatalf("decode the pinned golden: %v", err)
	}
	if d.SiteID == "" || d.Timezone == "" || len(d.Cutoffs) == 0 {
		t.Fatalf("the golden example leaves the fields the consumer acts on empty: %+v", d)
	}
	if d.Version != 0 {
		t.Fatalf("the pinned golden carries version %d; the contract has none (the producer example backfilled one)", d.Version)
	}

	if err := dispatch(t, rig.cpt, "cpt", CptTopic, rawEvent(t, nil, ev)); err != nil {
		t.Fatalf("dispatch the golden through the real cpt consumer: %v", err)
	}
	schedule, err := rig.copies.Get(context.Background(), shared.SiteID(d.SiteID))
	if err != nil || schedule.SiteID == "" {
		t.Fatalf("the golden schedule was not stored: %+v, %v", schedule, err)
	}
	if len(schedule.Cutoffs) != len(d.Cutoffs) || schedule.Version != cptVersion(d.Version) {
		t.Fatalf("the stored copy lost the golden's facts: %+v", schedule)
	}
}

// TestLayoutGoldenEventsDecodeAndApply: both facility-layout types this
// service acts on decode from the pinned examples (camelCase payloads)
// and apply through the real layout consumer: the zone lands and the
// location is placeable, joined with its zone.
func TestLayoutGoldenEventsDecodeAndApply(t *testing.T) {
	rig := newContractRig(t)
	const eventID = "a1b2c3d4-1111-4aaa-8bbb-ccc1111d2222"

	zone := pinnedEvent(t, layoutPin, LayoutTopic, TypeZoneRegistered)
	zd, err := decodeData[zoneRegisteredData](zone)
	if err != nil {
		t.Fatalf("decode the ZoneRegistered golden: %v", err)
	}
	if zd.ZoneID == "" || zd.SiteCode == "" || zd.ZoneCode == "" {
		t.Fatalf("the golden leaves the fields the consumer acts on empty: %+v", zd)
	}
	if err := dispatch(t, rig.layout, "layout", LayoutTopic, rawEvent(t, withID(eventID), zone)); err != nil {
		t.Fatalf("dispatch the ZoneRegistered golden: %v", err)
	}

	slot := pinnedEvent(t, layoutPin, LayoutTopic, TypeLocationSlotRegistered)
	ld, err := decodeData[locationSlotRegisteredData](slot)
	if err != nil {
		t.Fatalf("decode the LocationSlotRegistered golden: %v", err)
	}
	if ld.LocationCode == "" || ld.ZoneID == "" {
		t.Fatalf("the golden leaves the fields the consumer acts on empty: %+v", ld)
	}
	const slotEventID = "a1b2c3d4-3333-4aaa-8bbb-ccc3333d4444"
	if err := dispatch(t, rig.layout, "layout", LayoutTopic, rawEvent(t, withID(slotEventID), slot)); err != nil {
		t.Fatalf("dispatch the LocationSlotRegistered golden: %v", err)
	}

	// The pinned examples name one zone family (WH1-STOR-AMB) and a slot of
	// another (WH1-YARD-NORTH): assert against the slot's OWN zone to stay
	// honest with whatever the producer's first example carries.
	place, ok, err := rig.copies.Place(context.Background(), ld.LocationCode)
	if err != nil || !ok {
		t.Fatalf("the golden location was never stored: ok=%v %v", ok, err)
	}
	if place.Code != ld.LocationCode || place.ZoneID != ld.ZoneID {
		t.Fatalf("the stored place lost the golden's facts: %+v", place)
	}
}

// TestCptGoldenReplayIsDeduped: the SAME golden bytes applied twice keep
// the copy-set size unchanged (the inbox claims the CloudEvents id).
func TestCptGoldenReplayIsDeduped(t *testing.T) {
	rig := newContractRig(t)
	ev := pinnedEvent(t, cptPin, CptTopic, TypeCPTScheduleChanged)
	const eventID = "1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a"
	raw := rawEvent(t, withID(eventID), ev)
	for attempt := 1; attempt <= 2; attempt++ {
		if err := dispatch(t, rig.cpt, "cpt", CptTopic, raw); err != nil {
			t.Fatalf("dispatch attempt %d: %v", attempt, err)
		}
	}
	d, err := decodeData[cptScheduleChangedData](ev)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := rig.copies.Get(context.Background(), shared.SiteID(d.SiteID))
	if err != nil || len(schedule.Cutoffs) != len(d.Cutoffs) {
		t.Fatalf("the replay changed the stored copy: %+v, %v", schedule, err)
	}
}
