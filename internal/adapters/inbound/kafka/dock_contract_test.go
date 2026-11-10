package kafka

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"gopkg.in/yaml.v3"
)

// dock-scheduling publishes the appointment this service plans a load from.
// Its merged AsyncAPI (AsyncAPI 2.6, not the 3.0 layout of the yard contract) is
// pinned at contracts/dock-scheduling.asyncapi.yaml and kept equal to the
// producer's develop by scripts/harness/pinned_contract_gate.py. Proven here:
//  1. DockAppointmentBooked is published on the INTEGRATION channel
//     (warehouse.dock-scheduling.events), not only on the analytics twin;
//  2. the producer's own example decodes into the struct the consumer reads,
//     and carries every field the consumer acts on (cpt_id is nullable by
//     contract: the key must be present, its value may be null).

const (
	dockPin                = "../../../../contracts/dock-scheduling.asyncapi.yaml"
	dockIntegrationChannel = "warehouse.dock-scheduling.events"
)

type dockSpec struct {
	Channels map[string]struct {
		Subscribe struct {
			Message struct {
				OneOf []refNode `yaml:"oneOf"`
			} `yaml:"message"`
		} `yaml:"subscribe"`
	} `yaml:"channels"`
	Components struct {
		Messages map[string]struct {
			Examples []struct {
				Payload map[string]any `yaml:"payload"`
			} `yaml:"examples"`
		} `yaml:"messages"`
	} `yaml:"components"`
}

func loadDockPin(t *testing.T) dockSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(dockPin))
	if err != nil {
		t.Fatalf("read the pinned dock-scheduling contract: %v", err)
	}
	var spec dockSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse the pinned dock-scheduling contract: %v", err)
	}
	return spec
}

// publishedOnIntegration reports whether the named component message is one of
// the integration channel's messages.
func (s dockSpec) publishedOnIntegration(name string) bool {
	for _, ref := range s.Channels[dockIntegrationChannel].Subscribe.Message.OneOf {
		if ref.Ref[strings.LastIndex(ref.Ref, "/")+1:] == name {
			return true
		}
	}
	return false
}

func TestDockAppointmentBookedMatchesTheProducersIntegrationContract(t *testing.T) {
	spec := loadDockPin(t)
	const name = "DockAppointmentBooked"
	if !spec.publishedOnIntegration(name) {
		t.Fatalf("%s is not published on %s: this service would never receive it", name, dockIntegrationChannel)
	}
	msg := spec.Components.Messages[name]
	if len(msg.Examples) == 0 {
		t.Fatalf("%s has no example payload in the pinned contract", name)
	}
	var ev ce.Event
	body := mustJSON(t, msg.Examples[0].Payload)
	if err := ev.UnmarshalJSON(body); err != nil {
		t.Fatalf("the producer's example is not a CloudEvent: %v", err)
	}
	if ev.Type() != TypeDockAppointmentBooked {
		t.Fatalf("example type %q, consumer acts on %q", ev.Type(), TypeDockAppointmentBooked)
	}
	d, err := decodeData[dockAppointmentBookedData](ev)
	if err != nil {
		t.Fatalf("decode the producer's example: %v", err)
	}
	if missing := emptyActedOnFields(d); len(missing) > 0 {
		t.Errorf("the producer's example leaves %v empty, this service acts on them: %+v", missing, d)
	}
	var data map[string]any
	if err := ev.DataAs(&data); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"appointment_id", "site_id", "direction", "door_code", "window_start", "window_end", "cpt_id", "carrier_scac", "version"} {
		if _, ok := data[field]; !ok {
			t.Errorf("the producer's example carries no %q (keys %v)", field, keysOf(data))
		}
	}
}

// The check itself must discriminate: a message that only the analytics twin
// declares is not published on the integration channel.
func TestDockPublishedOnIntegrationIgnoresTheAnalyticsTwin(t *testing.T) {
	const doc = `
channels:
  warehouse.dock-scheduling.events:
    subscribe: {message: {oneOf: [{$ref: '#/components/messages/Booked'}]}}
  warehouse.dock-scheduling.analytics:
    subscribe: {message: {oneOf: [{$ref: '#/components/messages/BookedAnalytics'}, {$ref: '#/components/messages/CheckedIn'}]}}
`
	var spec dockSpec
	if err := yaml.Unmarshal([]byte(doc), &spec); err != nil {
		t.Fatal(err)
	}
	if !spec.publishedOnIntegration("Booked") {
		t.Error("an integration message must be found")
	}
	if spec.publishedOnIntegration("CheckedIn") || spec.publishedOnIntegration("BookedAnalytics") {
		t.Error("an analytics-only message must not count as published on the integration channel")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// emptyActedOnFields names the fields the consumer acts on that decoded empty.
func emptyActedOnFields(d dockAppointmentBookedData) []string {
	var missing []string
	for name, empty := range map[string]bool{
		"appointment_id": d.AppointmentID == "", "site_id": d.SiteID == "", "direction": d.Direction == "",
		"door_code": d.DoorCode == "", "window_start": d.WindowStart == "", "window_end": d.WindowEnd == "",
		"version": d.Version < 1,
	} {
		if empty {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}
