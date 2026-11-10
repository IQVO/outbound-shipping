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

// The yard types this service acts on are validated against a COPY of
// yard-management's merged AsyncAPI (contracts/yard-management.asyncapi.yaml,
// kept equal to the producer's develop by scripts/harness/pinned_contract_gate.py).
// Two things are proven, because both were once wrong:
//  1. the type is published on the INTEGRATION channel (DoorWorkCompleted and
//     TrailerDeparted were analytics-only, so ship-confirm could never fire);
//  2. the producer's own example payload decodes into the struct the consumer
//     reads, with every field the consumer acts on present.

const yardPin = "../../../../contracts/yard-management.asyncapi.yaml"

// yardIntegrationChannel is the channel key of warehouse.yard-management.events.
const yardIntegrationChannel = "yardManagementEvents"

type asyncAPI struct {
	Channels   map[string]struct{ Messages map[string]refNode } `yaml:"channels"`
	Components struct {
		Messages map[string]pinnedMessage `yaml:"messages"`
	} `yaml:"components"`
}

type refNode struct {
	Ref string `yaml:"$ref"`
}

type pinnedMessage struct {
	Payload struct {
		AllOf []struct {
			Properties struct {
				Type struct {
					Const string `yaml:"const"`
				} `yaml:"type"`
			} `yaml:"properties"`
		} `yaml:"allOf"`
	} `yaml:"payload"`
	Examples []struct {
		Payload map[string]any `yaml:"payload"`
	} `yaml:"examples"`
}

func (m pinnedMessage) typeConst() string {
	for _, part := range m.Payload.AllOf {
		if c := part.Properties.Type.Const; c != "" {
			return c
		}
	}
	return ""
}

// onIntegrationChannel returns the pinned message of the given CloudEvents
// type declared on the integration channel, if any.
func onIntegrationChannel(t *testing.T, spec asyncAPI, eventType string) (pinnedMessage, bool) {
	t.Helper()
	channel, ok := spec.Channels[yardIntegrationChannel]
	if !ok {
		t.Fatalf("pinned contract has no %q channel", yardIntegrationChannel)
	}
	for _, ref := range channel.Messages {
		name := ref.Ref[strings.LastIndex(ref.Ref, "/")+1:]
		if m, found := spec.Components.Messages[name]; found && m.typeConst() == eventType {
			return m, true
		}
	}
	return pinnedMessage{}, false
}

type consumedType struct {
	eventType string
	decode    func(ce.Event) error
	required  []string
}

func loadYardPin(t *testing.T) asyncAPI {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(yardPin))
	if err != nil {
		t.Fatalf("read the pinned yard contract: %v", err)
	}
	var spec asyncAPI
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse the pinned yard contract: %v", err)
	}
	return spec
}

// exampleEvent returns the first example payload of msg as a CloudEvent.
func exampleEvent(t *testing.T, msg pinnedMessage, eventType string) ce.Event {
	t.Helper()
	if len(msg.Examples) == 0 {
		t.Fatalf("%s has no example payload in the pinned contract", eventType)
	}
	body, err := json.Marshal(msg.Examples[0].Payload)
	if err != nil {
		t.Fatalf("marshal example: %v", err)
	}
	var ev ce.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("the producer's example is not a CloudEvent: %v", err)
	}
	if ev.Type() != eventType {
		t.Fatalf("example type %q, want %q", ev.Type(), eventType)
	}
	return ev
}

func checkConsumedType(t *testing.T, spec asyncAPI, c consumedType) {
	t.Helper()
	msg, ok := onIntegrationChannel(t, spec, c.eventType)
	if !ok {
		t.Fatalf("%s is not published on the %s channel (warehouse.yard-management.events): this service would never receive it", c.eventType, yardIntegrationChannel)
	}
	ev := exampleEvent(t, msg, c.eventType)
	if err := c.decode(ev); err != nil {
		t.Fatalf("decode the producer's example: %v", err)
	}
	var data map[string]any
	if err := ev.DataAs(&data); err != nil {
		t.Fatalf("example data: %v", err)
	}
	for _, field := range c.required {
		if _, present := data[field]; !present {
			t.Errorf("the producer's example carries no %q, which this service acts on (keys %v)", field, keysOf(data))
		}
	}
}

func TestConsumedYardTypesMatchTheProducersIntegrationContract(t *testing.T) {
	spec := loadYardPin(t)
	for _, c := range []consumedType{
		{TypeTrailerDocked, decodes[trailerDockedData], []string{"trailer_id", "site_id", "door_code", "docked_at"}},
		{TypeDoorWorkCompleted, decodes[doorWorkCompletedData], []string{"trailer_id", "site_id", "door_code", "completed_at"}},
		{TypeTrailerDeparted, decodes[trailerDepartedData], []string{"trailer_id", "site_id", "departed_at", "seal_number"}},
	} {
		t.Run(c.eventType[strings.LastIndex(c.eventType, ".")+1:], func(t *testing.T) { checkConsumedType(t, spec, c) })
	}
}

// decodes returns the consumer's own decoder for T, discarding the value:
// the point is that the producer's example fits the struct.
func decodes[T any](e ce.Event) error {
	_, err := decodeData[T](e)
	return err
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestOnIntegrationChannelRejectsAnalyticsOnlyTypes keeps the check itself
// honest: a type declared only on the analytics channel must not count as
// published on the integration one.
func TestOnIntegrationChannelRejectsAnalyticsOnlyTypes(t *testing.T) {
	const doc = `
channels:
  yardManagementEvents:
    messages:
      Docked: {$ref: '#/components/messages/Docked'}
  yardManagementAnalytics:
    messages:
      DepartedAnalytics: {$ref: '#/components/messages/DepartedAnalytics'}
components:
  messages:
    Docked:
      payload: {allOf: [{}, {properties: {type: {const: t.Docked}}}]}
    DepartedAnalytics:
      payload: {allOf: [{}, {properties: {type: {const: t.Departed}}}]}
`
	var spec asyncAPI
	if err := yaml.Unmarshal([]byte(doc), &spec); err != nil {
		t.Fatal(err)
	}
	if _, ok := onIntegrationChannel(t, spec, "t.Docked"); !ok {
		t.Error("an integration message must be found")
	}
	if _, ok := onIntegrationChannel(t, spec, "t.Departed"); ok {
		t.Error("an analytics-only type must not count as published on the integration channel")
	}
}

// The real CPTScheduleChanged carries no version (process-path-management's
// AsyncAPI example has site_id, timezone and cutoffs only): it must decode and
// get a storable version instead of being dead-lettered.
func TestRealCptScheduleWithoutAVersionIsAccepted(t *testing.T) {
	const payload = `{"specversion":"1.0","id":"1d2e3f4a-5b6c-4d7e-8f9a-0b1c2d3e4f5a","source":"/warehouse/process-path-management",` +
		`"type":"com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged","subject":"sp1","time":"2026-09-13T00:00:00Z",` +
		`"datacontenttype":"application/json","data":{"site_id":"sp1","timezone":"America/Sao_Paulo",` +
		`"cutoffs":[{"cpt_id":"sp1-1500","local_time":"15:00","days_of_week":["Mon","Tue"],"ship_method":"ground","eligible_path_ids":["PICK"]}]}}`
	var ev ce.Event
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		t.Fatal(err)
	}
	d, err := decodeData[cptScheduleChangedData](ev)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.SiteID != "sp1" || len(d.Cutoffs) != 1 || d.Version != 0 {
		t.Fatalf("decoded %+v", d)
	}
	if got := cptVersion(d.Version); got != 1 {
		t.Errorf("version stored for a payload without one = %d, want 1 (the copy table requires >= 1)", got)
	}
	if got := cptVersion(7); got != 7 {
		t.Errorf("a producer-sent version must be kept, got %d", got)
	}
}
