package kafka

// The self-pin: this repo's OWN apis/asyncapi.yaml is pinned at
// contracts/outbound-shipping.asyncapi.yaml (the repo is its own producer
// for its analytics stream, the projector's only feed). The projector's
// handler grid must cover exactly the documented analytics catalogue, and
// every documented own event decodes through the projector's real
// decoder. ShipmentConfirmed on the EVENTS channel is asserted too: it is
// the fleet's ship fact (ADR 0004).

import (
	"context"
	"testing"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// TestSelfPinMatchesTheProjectorGridBothWays: every type documented on the
// analytics channel has a projector handler, and the handler map is
// exactly the cross-product of the documented event names x the three
// entities — a deliberate superset (the fold claims and ignores names an
// entity never raises; analytics_consumer.go documents why), so the wire
// documents 13 pairs while the grid routes 39.
func TestSelfPinMatchesTheProjectorGridBothWays(t *testing.T) {
	doc := loadPin(t, selfPin)
	handled := AnalyticsHandlers(ProjectorUseCases{}, discardLogger())
	entities := []string{"outboundload", "shippingpackage", "billoflading"}

	documented := map[string]bool{}
	for _, name := range doc.channelMessages(selfAnalyticsChannel) {
		msg, ok := doc.Components.Messages[name]
		if !ok {
			t.Fatalf("the analytics channel references unknown message %q", name)
		}
		eventType, _ := doc.resolve(msg.Payload)
		if eventType == "" {
			t.Fatalf("message %q declares no type const", name)
		}
		if _, ok := handled[eventType]; !ok {
			t.Errorf("%s is documented on the analytics channel but the projector does not route it", eventType)
		}
		documented[eventType] = true
	}

	grid := map[string]bool{}
	for eventType := range documented {
		for _, entity := range entities {
			grid[analyticsTypePrefix+entity+"."+shortName(eventType)] = true
		}
	}
	for eventType := range handled {
		if !grid[eventType] {
			t.Errorf("the projector routes %s, which is not in the documented names x entities grid", eventType)
		}
	}
}

// TestSelfPinDocumentsShipmentConfirmedOnTheEventsChannel: the integration
// stream carries the fleet's ship fact, not only the analytics twin.
func TestSelfPinDocumentsShipmentConfirmedOnTheEventsChannel(t *testing.T) {
	doc := loadPin(t, selfPin)
	confirmed := analyticsTypePrefix + "outboundload.ShipmentConfirmed"
	doc.messageOn(t, selfPin, selfEventsChannel, confirmed)
}

// TestEveryDocumentedOwnEventDecodesThroughTheProjector: every example on
// the analytics channel decodes through boardEventData — the one struct
// the projector folds every own type with.
func TestEveryDocumentedOwnEventDecodesThroughTheProjector(t *testing.T) {
	doc := loadPin(t, selfPin)
	for _, name := range doc.channelMessages(selfAnalyticsChannel) {
		msg := doc.Components.Messages[name]
		eventType, _ := doc.resolve(msg.Payload)
		t.Run(name, func(t *testing.T) {
			if len(msg.Examples) == 0 {
				t.Fatalf("%s has no example payload in the self-pin", name)
			}
			ev := eventFromPayload(t, selfPin, eventType, msg.Examples[0].Payload)
			if _, err := decodeData[boardEventData](ev); err != nil {
				t.Fatalf("the projector's decoder rejects the own example: %v", err)
			}
		})
	}
}

// TestOwnGoldenEventFoldsThroughTheRealProjector: the LoadPlanned golden
// through the REAL analytics consumer lands in the board the reports
// read, and its replay is id-deduped.
func TestOwnGoldenEventFoldsThroughTheRealProjector(t *testing.T) {
	rig := newContractRig(t)
	planned := pinnedEvent(t, selfPin, selfAnalyticsChannel, analyticsTypePrefix+"outboundload.LoadPlanned")
	const eventID = "e1f2a3b4-c5d6-4e7f-8a9b-0c1d2e3f4a5b"
	raw := rawEvent(t, withID(eventID), planned)
	for attempt := 1; attempt <= 2; attempt++ {
		if err := dispatch(t, rig.analytics, "analytics", AnalyticsTopic, raw); err != nil {
			t.Fatalf("fold attempt %d: %v", attempt, err)
		}
	}
	if !rig.processed.Has(usecases.ProjectorConsumer, eventID) {
		t.Fatal("the own golden was never claimed by the projector inbox")
	}
	rows, err := rig.board.Report(context.Background(), shared.SiteID("SITE-1"), "2026-11-06")
	if err != nil || len(rows) != 1 {
		t.Fatalf("the own golden folded %d board rows for SITE-1 on 2026-11-06, want exactly 1: %v", len(rows), err)
	}
}
