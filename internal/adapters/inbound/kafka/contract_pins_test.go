package kafka

// E1 for the five event producers this service consumes plus its own
// self-pin: every topic the readers actually subscribe to (the topic
// constants in this package's consumer sources, NOT the spec's operations)
// exists as a channel in the pinned producer AsyncAPI, and every type the
// consumer dispatches on is documented there with the same CloudEvents
// `type`. The fleet's pinned specs are channel-perspective for some
// producers and app-perspective for others (process-path-management
// documents its own topic under publish:), so a channel's messages are
// collected from subscribe:, publish: AND a direct messages map — the
// union is the topic's catalogue either way.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"gopkg.in/yaml.v3"
)

// pin paths under contracts/ (byte-copies, see contracts/pinned.json).
const (
	fulfillmentPin = "../../../../contracts/fulfillment-execution.asyncapi.yaml"
	cptPin         = "../../../../contracts/process-path-management.asyncapi.yaml"
	layoutPin      = "../../../../contracts/facility-layout.asyncapi.yaml"
	selfPin        = "../../../../contracts/outbound-shipping.asyncapi.yaml"

	selfEventsChannel    = "warehouse.outbound-shipping.events"
	selfAnalyticsChannel = "warehouse.outbound-shipping.analytics"
	// yardChannelKey: yard-management's merged AsyncAPI keys the channel by
	// name, not by the topic literal.
	yardChannelKey = "yardManagementEvents"
)

// pinConst reads a schema const or a single-value enum (dock-scheduling
// uses enums).
type pinConst struct {
	Const string   `yaml:"const"`
	Enum  []string `yaml:"enum"`
}

func (c pinConst) value() string {
	if c.Const != "" {
		return c.Const
	}
	if len(c.Enum) == 1 {
		return c.Enum[0]
	}
	return ""
}

// pinBody is a payload or schema body: an allOf with inline consts, a $ref
// to a components schema that carries them, or both.
type pinBody struct {
	Ref   string        `yaml:"$ref"`
	AllOf []pinBodyPart `yaml:"allOf"`
}

type pinBodyPart struct {
	Ref        string `yaml:"$ref"`
	Properties struct {
		Type       pinConst `yaml:"type"`
		DataSchema pinConst `yaml:"dataschema"`
	} `yaml:"properties"`
}

type pinMessage struct {
	Name        string       `yaml:"name"`
	Payload     pinBody      `yaml:"payload"`
	Examples    []pinExample `yaml:"examples"`
	ContentType string       `yaml:"contentType"`
}

type pinExample struct {
	Name    string         `yaml:"name"`
	Payload map[string]any `yaml:"payload"`
}

type pinOperation struct {
	Message struct {
		OneOf []struct {
			Ref string `yaml:"$ref"`
		} `yaml:"oneOf"`
	} `yaml:"message"`
}

type pinChannel struct {
	Subscribe *pinOperation `yaml:"subscribe"`
	Publish   *pinOperation `yaml:"publish"`
	Messages  map[string]struct {
		Ref string `yaml:"$ref"`
	} `yaml:"messages"`
}

// pinDoc is the shape of every fleet asyncapi pin this repo carries.
type pinDoc struct {
	Channels   map[string]pinChannel `yaml:"channels"`
	Components struct {
		Messages map[string]pinMessage `yaml:"messages"`
		Schemas  map[string]pinBody    `yaml:"schemas"`
	} `yaml:"components"`
}

func loadPin(t *testing.T, path string) pinDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read the pinned contract %s: %v", path, err)
	}
	var doc pinDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the pinned contract %s: %v", path, err)
	}
	return doc
}

// refName returns the segment after the last "/" of a JSON pointer.
func refName(ref string) string {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '/' {
			return ref[i+1:]
		}
	}
	return ref
}

// channelMessages returns the component-message names a channel declares,
// across every layout the fleet pins (subscribe/publish oneOf and a direct
// messages map).
func (d pinDoc) channelMessages(channel string) []string {
	ch, ok := d.Channels[channel]
	if !ok {
		return nil
	}
	var names []string
	for _, op := range []*pinOperation{ch.Subscribe, ch.Publish} {
		if op == nil {
			continue
		}
		for _, ref := range op.Message.OneOf {
			names = append(names, refName(ref.Ref))
		}
	}
	for name := range ch.Messages {
		names = append(names, name)
	}
	return names
}

// resolve follows one body's inline consts, then its $ref (one hop into
// components.schemas), and returns the type and dataschema consts.
func (d pinDoc) resolve(b pinBody) (eventType, dataschema string) {
	for _, part := range b.AllOf {
		eventType = firstNonEmpty(eventType, part.Properties.Type.value())
		dataschema = firstNonEmpty(dataschema, part.Properties.DataSchema.value())
		if part.Ref != "" {
			if schema, ok := d.Components.Schemas[refName(part.Ref)]; ok {
				t2, s2 := d.resolve(schema)
				eventType = firstNonEmpty(eventType, t2)
				dataschema = firstNonEmpty(dataschema, s2)
			}
		}
	}
	if b.Ref != "" {
		if schema, ok := d.Components.Schemas[refName(b.Ref)]; ok {
			t2, s2 := d.resolve(schema)
			eventType = firstNonEmpty(eventType, t2)
			dataschema = firstNonEmpty(dataschema, s2)
		}
	}
	return eventType, dataschema
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// messageOn returns the pinned message documented on channel whose
// CloudEvents type is eventType, and whether one exists. The lookup keys
// the message by its documented type, never by component name, so a
// producer renaming a component cannot silently pass.
func (d pinDoc) messageOn(t *testing.T, path, channel, eventType string) pinMessage {
	t.Helper()
	found, amb := 0, pinMessage{}
	for _, name := range d.channelMessages(channel) {
		msg, ok := d.Components.Messages[name]
		if !ok {
			t.Fatalf("%s: channel %s references unknown message %q", path, channel, name)
		}
		if documented, _ := d.resolve(msg.Payload); documented == eventType {
			found, amb = found+1, msg
		}
	}
	if found != 1 {
		t.Fatalf("%s: %s is documented %d times on %s, want exactly once", path, eventType, found, channel)
	}
	return amb
}

// goldenEvent unmarshals msg's first example into a CloudEvent and asserts
// its type: the spec's own example IS the golden fixture (provenance 1).
func goldenEvent(t *testing.T, path, eventType string, msg pinMessage) ce.Event {
	t.Helper()
	if len(msg.Examples) == 0 {
		t.Fatalf("%s: %s has no example payload in the pinned contract", path, eventType)
	}
	return eventFromPayload(t, path, eventType, msg.Examples[0].Payload)
}

// eventFromPayload unmarshals one documented example payload into a
// CloudEvent and asserts its type matches the consumer's constant.
func eventFromPayload(t *testing.T, path, eventType string, payload map[string]any) ce.Event {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("%s: marshal a %s example: %v", path, eventType, err)
	}
	var ev ce.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("%s: %s's example is not a CloudEvent: %v", path, eventType, err)
	}
	if ev.Type() != eventType {
		t.Fatalf("%s: example type %q, consumer dispatches on %q", path, ev.Type(), eventType)
	}
	return ev
}

// pinnedEvent loads the golden example of one consumed type straight from a
// pin: the helper every archetype test below starts from.
func pinnedEvent(t *testing.T, path, channel, eventType string) ce.Event {
	t.Helper()
	doc := loadPin(t, path)
	return goldenEvent(t, path, eventType, doc.messageOn(t, path, channel, eventType))
}

type subscription struct {
	pin     string
	channel string
	types   []string
}

// TestPinnedAsyncAPICoversSubscriptions (E1): the table IS the reader
// config — every topic constant this package declares, with the types its
// handler map dispatches on, must be covered by the pinned producer spec.
func TestPinnedAsyncAPICoversSubscriptions(t *testing.T) {
	for _, s := range []subscription{
		{fulfillmentPin, FulfillmentTopic, []string{TypePackageManifested, TypePackageDiverted}},
		{cptPin, CptTopic, []string{TypeCPTScheduleChanged}},
		{layoutPin, LayoutTopic, []string{TypeZoneRegistered, TypeLocationSlotRegistered}},
		{dockPin, dockIntegrationChannel, []string{TypeDockAppointmentBooked}},
		{yardPin, yardChannelKey, []string{TypeTrailerDocked, TypeDoorWorkCompleted, TypeTrailerDeparted}},
		{selfPin, selfAnalyticsChannel, selfAnalyticsTypes()},
	} {
		for _, eventType := range s.types {
			t.Run(shortName(eventType), func(t *testing.T) {
				doc := loadPin(t, s.pin)
				doc.messageOn(t, s.pin, s.channel, eventType)
			})
		}
	}
}

// TestChannelMessagesReadsEveryLayout keeps the union lookup itself
// honest: a producer may document its own topic under subscribe:
// (channel-perspective) or publish: (app-perspective) or key messages
// directly; all three must resolve.
func TestChannelMessagesReadsEveryLayout(t *testing.T) {
	const doc = `
channels:
  topic:
    publish: {message: {oneOf: [{$ref: '#/components/messages/A'}]}}
    messages: {B: {$ref: '#/components/messages/B'}}
  other:
    subscribe: {message: {oneOf: [{$ref: '#/components/messages/C'}]}}
`
	var d pinDoc
	if err := yaml.Unmarshal([]byte(doc), &d); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"A", "B"} {
		if got := d.channelMessages("topic"); !contains(got, want) {
			t.Errorf("channelMessages(topic) = %v, want %s among them", got, want)
		}
	}
	if got := d.channelMessages("other"); !contains(got, "C") {
		t.Errorf("channelMessages(other) = %v, want C", got)
	}
	if got := d.channelMessages("missing"); got != nil {
		t.Errorf("channelMessages(missing) = %v, want nil", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func shortName(eventType string) string {
	for i := len(eventType) - 1; i >= 0; i-- {
		if eventType[i] == '.' {
			return eventType[i+1:]
		}
	}
	return eventType
}

// selfAnalyticsTypes lists the own types the self-pin documents on the
// analytics channel (selfpin_contract_test.go checks the full relation
// with the projector's grid both ways).
func selfAnalyticsTypes() []string {
	entityOf := map[string]string{
		"LoadPlanned": "outboundload", "LoadDoorAssigned": "outboundload",
		"LoadingStarted": "outboundload", "LoadClosed": "outboundload",
		"LoadReopened": "outboundload", "LoadCancelled": "outboundload",
		"ShipmentConfirmed": "outboundload",
		"PackageStaged":     "shippingpackage", "PackageLoaded": "shippingpackage",
		"PackageUnloaded": "shippingpackage", "PackageMissedLoad": "shippingpackage",
		"BillOfLadingIssued": "billoflading", "BillOfLadingVoided": "billoflading",
	}
	handled := AnalyticsHandlers(ProjectorUseCases{}, discardLogger())
	types := make([]string, 0, len(entityOf))
	for name, entity := range entityOf {
		eventType := analyticsTypePrefix + entity + "." + name
		if _, ok := handled[eventType]; ok {
			types = append(types, eventType)
		}
	}
	return types
}
