// Package events is the outbound event publisher: it maps the facts the
// application raises (outbound load, shipping package and bill of lading
// events) to CloudEvents 1.0 outbox messages and hands them to the
// transactional outbox (ADR 0005). Envelopes are built ONLY through
// internal/adapters/kafka/cloudevents; payloads are exactly the ones pinned
// in apis/asyncapi.yaml.
package events

import (
	"fmt"
	"time"

	"github.com/claudioed/outbound-shipping/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/outbound-shipping/internal/application/outbox"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
)

// The two topics this service publishes to (model.yaml conventions). The
// outbox relay sets Message.Topic per row; the Kafka writer carries no Topic.
const (
	// TopicEvents is the integration topic.
	TopicEvents = "warehouse.outbound-shipping.events"
	// TopicAnalytics is the analytics twin every event also lands on (the
	// projector's only feed).
	TopicAnalytics = "warehouse.outbound-shipping.analytics"
)

// The `<entity>` segments of the published types:
// com.warehouse.wms.outbound-shipping.<entity>.<EventName>.
const (
	EntityOutboundLoad    = "outboundload"
	EntityShippingPackage = "shippingpackage"
	EntityBillofLading    = "billoflading"
)

// schemaVersion is the dataschema version of every payload below
// (urn:warehouse:outbound-shipping:<events|analytics>:<EventName>:v1).
const schemaVersion = 1

// ts renders a domain timestamp on the wire (RFC 3339 UTC).
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Wire payloads (CloudEvents `data`, snake_case JSON, model.yaml `publishes`).

type loadPlannedData struct {
	LoadID        string `json:"load_id"`
	SiteID        string `json:"site_id"`
	CptID         string `json:"cpt_id"`
	OperatingDate string `json:"operating_date"`
	ShipMethod    string `json:"ship_method"`
	Cutoff        string `json:"cutoff"`
	Version       int64  `json:"version"`
}

type loadDoorAssignedData struct {
	LoadID        string `json:"load_id"`
	SiteID        string `json:"site_id"`
	DoorCode      string `json:"door_code"`
	AppointmentID string `json:"appointment_id"`
	Version       int64  `json:"version"`
}

type loadingStartedData struct {
	LoadID    string `json:"load_id"`
	SiteID    string `json:"site_id"`
	DoorCode  string `json:"door_code"`
	TrailerID string `json:"trailer_id"`
	StartedAt string `json:"started_at"`
	Version   int64  `json:"version"`
}

type loadClosedData struct {
	LoadID            string `json:"load_id"`
	SiteID            string `json:"site_id"`
	DoorCode          string `json:"door_code"`
	TrailerID         string `json:"trailer_id"`
	PackageCount      int    `json:"package_count"`
	ShortShippedCount int    `json:"short_shipped_count"`
	ClosedAt          string `json:"closed_at"`
	Version           int64  `json:"version"`
}

type loadReopenedData struct {
	LoadID     string `json:"load_id"`
	SiteID     string `json:"site_id"`
	ReopenedAt string `json:"reopened_at"`
	Version    int64  `json:"version"`
}

type loadCancelledData struct {
	LoadID      string `json:"load_id"`
	SiteID      string `json:"site_id"`
	CancelledAt string `json:"cancelled_at"`
	Version     int64  `json:"version"`
}

type confirmedOrder struct {
	OrderID string `json:"order_id"`
}

type shipmentConfirmedData struct {
	LoadID     string           `json:"load_id"`
	SiteID     string           `json:"site_id"`
	CptID      string           `json:"cpt_id"`
	Carrier    string           `json:"carrier"`
	TrailerID  string           `json:"trailer_id"`
	SealNumber string           `json:"seal_number"`
	DepartedAt string           `json:"departed_at"`
	Orders     []confirmedOrder `json:"orders"`
	Version    int64            `json:"version"`
}

// EncodeLoadEvent maps one OutboundLoad fact to its outbox messages: the
// integration topic and the analytics twin (same CloudEvents id, same bytes,
// only the dataschema stream segment differs).
func EncodeLoadEvent(id string, e outboundload.Event) ([]outbox.Message, error) {
	var (
		data     any
		name     = e.EventName()
		subject  = string(e.LoadID())
		occurred = e.OccurredAt()
	)
	switch v := e.(type) {
	case outboundload.LoadPlanned:
		data = loadPlannedData{string(v.Load), string(v.Site), string(v.CptID),
			string(v.OperatingDate), string(v.ShipMethod), ts(v.Cutoff), v.Version}
	case outboundload.LoadDoorAssigned:
		data = loadDoorAssignedData{string(v.Load), string(v.Site),
			string(v.DoorCode), string(v.AppointmentID), v.Version}
	case outboundload.LoadingStarted:
		data = loadingStartedData{string(v.Load), string(v.Site),
			string(v.DoorCode), string(v.TrailerID), ts(v.StartedAt), v.Version}
	case outboundload.LoadClosed:
		data = loadClosedData{string(v.Load), string(v.Site), string(v.DoorCode),
			string(v.TrailerID), v.PackageCount, v.ShortShippedCount,
			ts(v.ClosedAt), v.Version}
	case outboundload.LoadReopened:
		data = loadReopenedData{string(v.Load), string(v.Site), ts(v.ReopenedAt), v.Version}
	case outboundload.ShipmentConfirmed:
		orders := make([]confirmedOrder, 0, len(v.Orders))
		for _, o := range v.Orders {
			orders = append(orders, confirmedOrder{OrderID: o})
		}
		data = shipmentConfirmedData{string(v.Load), string(v.Site), string(v.CptID),
			v.Carrier, string(v.TrailerID), v.SealNumber, ts(v.DepartedAt),
			orders, v.Version}
	case outboundload.LoadCancelled:
		data = loadCancelledData{string(v.Load), string(v.Site), ts(v.CancelledAt), v.Version}
	default:
		return nil, fmt.Errorf("events: unknown outbound load event %T", e)
	}
	return encodeBoth(id, EntityOutboundLoad, name, subject, occurred, data)
}

// encodeBoth builds the integration message and its analytics twin.
func encodeBoth(id, entity, name, subject string, at time.Time, data any) ([]outbox.Message, error) {
	integration, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: entity, EventName: name, Subject: subject,
		Time: at, Stream: cloudevents.StreamEvents, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return nil, err
	}
	analytics, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: entity, EventName: name, Subject: subject,
		Time: at, Stream: cloudevents.StreamAnalytics, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return nil, err
	}
	key := []byte(subject)
	hdr := []outbox.Header{{Key: "content-type", Value: cloudevents.MediaType}}
	return []outbox.Message{
		{EventID: id, Topic: TopicEvents, EventType: cloudevents.Type(entity, name),
			Subject: subject, Key: key,
			DataSchema: cloudevents.DataSchema(cloudevents.StreamEvents, name, schemaVersion),
			Value:      integration, Headers: hdr},
		{EventID: id, Topic: TopicAnalytics, EventType: cloudevents.Type(entity, name),
			Subject: subject, Key: key,
			DataSchema: cloudevents.DataSchema(cloudevents.StreamAnalytics, name, schemaVersion),
			Value:      analytics, Headers: hdr},
	}, nil
}
