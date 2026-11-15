package kafka

// E4 tolerance, against the repo's OWN documented policy (consumer.go,
// deadletter.go): unknown `data` keys must not break the decode or the
// dispatch; a type the consumer has no handler for is skipped (committed
// past, nothing claimed); a message that is not a CloudEvent is skipped
// with a WARN; a handled type whose payload can never be applied is
// POISON and dead-letters (DeadLetterInvalid), never a silent drop.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// withExtraDataKey injects an unknown data attribute into a payload.
func withExtraDataKey(payload map[string]any) map[string]any {
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		payload["data"] = data
	}
	data["producer_future_field"] = true
	return payload
}

// withExtraID composes a data mutation under a fresh id (the id is the
// idempotency key: every perturbed clone gets its own).
func withExtraID(mutate func(map[string]any) map[string]any, id string) func(map[string]any) map[string]any {
	return func(payload map[string]any) map[string]any {
		return withID(id)(mutate(payload))
	}
}

// TestUnknownDataKeysAreToleratedAtDispatch: the pinned goldens of the
// three newly pinned producers, each with an extra unknown data key,
// apply through the REAL consumers and leave the durable effect.
func TestUnknownDataKeysAreToleratedAtDispatch(t *testing.T) {
	rig := newContractRig(t)
	seedScheduleForTheManifest(t, rig)

	const manifestID = "b1c2d3e4-1234-4aaa-9bbb-abc1234def01"
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic,
		rawEvent(t, withExtraID(withExtraDataKey, manifestID), correlatedManifestPayload(t))); err != nil {
		t.Fatalf("a manifest with an unknown data key must apply, got %v", err)
	}
	if got := rig.rec.CountByType(analyticsTypePrefix + "outboundload.LoadPlanned"); got != 1 {
		t.Fatalf("the perturbed manifest planned %d loads, want 1", got)
	}

	const cptID = "b1c2d3e4-5678-4aaa-9bbb-abc5678def02"
	cpt := pinnedEvent(t, cptPin, CptTopic, TypeCPTScheduleChanged)
	if err := dispatch(t, rig.cpt, "cpt", CptTopic,
		rawEvent(t, withExtraID(withExtraDataKey, cptID), cpt)); err != nil {
		t.Fatalf("a CPTScheduleChanged with an unknown data key must apply, got %v", err)
	}
	if s, err := rig.copies.Get(context.Background(), shared.SiteID("sp1")); err != nil || s.SiteID != "sp1" {
		t.Fatalf("the perturbed schedule was not stored: %+v %v", s, err)
	}

	const slotID = "b1c2d3e4-9abc-4aaa-9bbb-abc9abcdef03"
	slot := pinnedEvent(t, layoutPin, LayoutTopic, TypeLocationSlotRegistered)
	if err := dispatch(t, rig.layout, "layout", LayoutTopic,
		rawEvent(t, withExtraID(withExtraDataKey, slotID), slot)); err != nil {
		t.Fatalf("a LocationSlotRegistered with an unknown data key must apply, got %v", err)
	}
	if p, ok, err := rig.copies.Place(context.Background(), locationCodeOf(t, slot)); err != nil || !ok {
		t.Fatalf("the perturbed location was not stored: ok=%v %v", ok, err)
	} else if p.Role == "" {
		t.Fatalf("the perturbed location lost its role: %+v", p)
	}

	const boardID = "b1c2d3e4-def0-4aaa-9bbb-abcdef012304"
	planned := pinnedEvent(t, selfPin, selfAnalyticsChannel, analyticsTypePrefix+"outboundload.LoadPlanned")
	if err := dispatch(t, rig.analytics, "analytics", AnalyticsTopic,
		rawEvent(t, withExtraID(withExtraDataKey, boardID), planned)); err != nil {
		t.Fatalf("an own analytics event with an unknown data key must fold, got %v", err)
	}
	if !rig.processed.Has(usecases.ProjectorConsumer, boardID) {
		t.Fatal("the perturbed own event was never claimed by the projector inbox")
	}
}

// locationCodeOf reads the location code out of a decoded golden slot.
func locationCodeOf(t *testing.T, ev ce.Event) string {
	t.Helper()
	d, err := decodeData[locationSlotRegisteredData](ev)
	if err != nil {
		t.Fatalf("decode the slot golden: %v", err)
	}
	if d.LocationCode == "" {
		t.Fatal("the slot golden carries no locationCode")
	}
	return d.LocationCode
}

// TestUnknownDataKeysAreToleratedAtDecode covers the two producers pinned
// before this wave, at the decoder every one of their handlers uses.
func TestUnknownDataKeysAreToleratedAtDecode(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		path, channel, eventType string
	}{
		{"dock", dockPin, dockIntegrationChannel, TypeDockAppointmentBooked},
		{"yard", yardPin, yardChannelKey, TypeTrailerDocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			perturbed := eventFromPayload(t, tc.path, tc.eventType,
				withExtraDataKey(loadPin(t, tc.path).messageOn(t, tc.path, tc.channel, tc.eventType).Examples[0].Payload))
			var ev ce.Event
			body := rawEvent(t, nil, perturbed)
			if err := json.Unmarshal(body, &ev); err != nil {
				t.Fatal(err)
			}
			if _, err := decodeData[map[string]any](ev); err != nil {
				t.Fatalf("the real decoder must tolerate an unknown data key: %v", err)
			}
		})
	}
}

// TestUnknownTypesAreSkipped: a type documented on the consumed channel
// but with no handler here is skipped — nil error, NOTHING claimed.
func TestUnknownTypesAreSkipped(t *testing.T) {
	rig := newContractRig(t)
	const (
		unknown = "com.warehouse.wes.fulfillment-execution.task.TaskCompleted"
		id      = "c1c2c3c4-abcd-4aaa-9bbb-unknown1234"
	)
	raw := []byte(`{"specversion":"1.0","id":"` + id + `","source":"/warehouse/fulfillment-execution",` +
		`"type":"` + unknown + `","subject":"t1","time":"2026-09-30T15:00:00Z",` +
		`"datacontenttype":"application/json","data":{"task_id":"t1"}}`)
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic, raw); err != nil {
		t.Fatalf("an unknown type must be skipped, got %v", err)
	}
	if rig.processed.Has(usecases.FulfillmentConsumer, id) {
		t.Fatal("a skipped unknown type must not claim an inbox slot")
	}
}

// TestNonCloudEventsAreSkipped: bytes that are not a CloudEvent at all are
// skipped with a WARN (never retried, never dead-lettered).
func TestNonCloudEventsAreSkipped(t *testing.T) {
	rig := newContractRig(t)
	if err := dispatch(t, rig.fulfillment, "fulfillment", FulfillmentTopic, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("a non-CloudEvent must be skipped, got %v", err)
	}
	if err := dispatch(t, rig.cpt, "cpt", CptTopic, []byte("not even json")); err != nil {
		t.Fatalf("garbage must be skipped, got %v", err)
	}
}

// recordingDLQ captures the dead-lettered messages.
type recordingDLQ struct{ messages []kafkago.Message }

func (w *recordingDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.messages = append(w.messages, msgs...)
	return nil
}

func (w *recordingDLQ) Close() error { return nil }

// TestUnappliablePayloadsArePoisonNotDrops: a handled type whose payload
// can never be applied (a data object with wrong types) surfaces errPoison
// so the run loop dead-letters it — the repo's DeadLetterInvalid policy.
func TestUnappliablePayloadsArePoisonNotDrops(t *testing.T) {
	rig := newContractRig(t)
	golden := pinnedEvent(t, fulfillmentPin, FulfillmentTopic, TypePackageManifested)
	breakData := func(p map[string]any) map[string]any {
		p = withID("d4d5d6d7-8888-4aaa-9bbb-poison99887")(p)
		p["data"] = map[string]any{"package_id": 123}
		return p
	}
	dlq := &recordingDLQ{}
	c := &Consumer{Name: "fulfillment", Topic: FulfillmentTopic,
		Handlers: rig.fulfillment, Logger: discardLogger(), DLQ: dlq, DeadLetterInvalid: true}
	err := c.HandleMessage(context.Background(), rawEvent(t, breakData, golden))
	if !errors.Is(err, errPoison) {
		t.Fatalf("an unappliable payload must surface errPoison for the DLQ path, got %v", err)
	}
}
