package kafka

// E3 CloudEvents envelope invariants, on the pinned golden example of
// EVERY type the five consumers dispatch on plus the own self-pinned
// catalogue: specversion 1.0, id, source, subject, time and
// datacontenttype application/json present, and the dataschema EQUAL to
// the one the producer's own spec declares for that type (resolved from
// the spec, never built from a formula — analytics twins and enum-style
// consts exist in the fleet).

import (
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"
)

func envelopeCases() []subscription {
	cases := []subscription{
		{fulfillmentPin, FulfillmentTopic, []string{TypePackageManifested, TypePackageDiverted}},
		{cptPin, CptTopic, []string{TypeCPTScheduleChanged}},
		{layoutPin, LayoutTopic, []string{TypeZoneRegistered, TypeLocationSlotRegistered}},
		{dockPin, dockIntegrationChannel, []string{TypeDockAppointmentBooked}},
		{yardPin, yardChannelKey, []string{TypeTrailerDocked, TypeDoorWorkCompleted, TypeTrailerDeparted}},
		{selfPin, selfAnalyticsChannel, selfAnalyticsTypes()},
	}
	return cases
}

// TestCloudEventsEnvelopeInvariants asserts the golden envelopes carry the
// fleet-mandatory attributes and the producer's own dataschema.
func TestCloudEventsEnvelopeInvariants(t *testing.T) {
	for _, s := range envelopeCases() {
		doc := loadPin(t, s.pin)
		for _, eventType := range s.types {
			t.Run(shortName(eventType), func(t *testing.T) {
				msg := doc.messageOn(t, s.pin, s.channel, eventType)
				ev := goldenEvent(t, s.pin, eventType, msg)
				assertEnvelope(t, ev, eventType)
				_, wantSchema := doc.resolve(msg.Payload)
				if wantSchema == "" {
					t.Fatalf("%s: %s declares no dataschema const to assert against", s.pin, eventType)
				}
				if got := ev.DataSchema(); got != wantSchema {
					t.Errorf("dataschema %q, the spec declares %q", got, wantSchema)
				}
			})
		}
	}
}

// assertEnvelope checks the attributes every consumed CloudEvent must
// carry (ADR 0005), independent of the payload.
func assertEnvelope(t *testing.T, ev ce.Event, eventType string) {
	t.Helper()
	if got := ev.SpecVersion(); got != ce.CloudEventsVersionV1 {
		t.Errorf("%s: specversion %q, want 1.0", eventType, got)
	}
	if ev.ID() == "" {
		t.Errorf("%s: no id (the dedupe key)", eventType)
	}
	if ev.Source() == "" {
		t.Errorf("%s: no source", eventType)
	}
	if ev.Subject() == "" {
		t.Errorf("%s: no subject (the Kafka key)", eventType)
	}
	if ev.Time().IsZero() {
		t.Errorf("%s: no time", eventType)
	}
	if got := ev.DataContentType(); got != "application/json" {
		t.Errorf("%s: datacontenttype %q, want application/json", eventType, got)
	}
}
