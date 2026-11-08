package events

import (
	"fmt"

	"github.com/claudioed/outbound-shipping/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/outbound-shipping/internal/application/outbox"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// Package and BOL wire payloads (apis/asyncapi.yaml).

type packageStagedData struct {
	PackageID       string `json:"package_id"`
	LoadID          string `json:"load_id"`
	SiteID          string `json:"site_id"`
	StagingLocation string `json:"staging_location"`
	StagedAt        string `json:"staged_at"`
	Version         int64  `json:"version"`
}

type packageLoadedData struct {
	PackageID string `json:"package_id"`
	LoadID    string `json:"load_id"`
	SiteID    string `json:"site_id"`
	LoadedAt  string `json:"loaded_at"`
	Version   int64  `json:"version"`
}

type packageUnloadedData struct {
	PackageID  string `json:"package_id"`
	LoadID     string `json:"load_id"`
	SiteID     string `json:"site_id"`
	UnloadedAt string `json:"unloaded_at"`
	Reason     string `json:"reason,omitempty"`
	Version    int64  `json:"version"`
}

type packageMissedLoadData struct {
	PackageID     string `json:"package_id"`
	LoadID        string `json:"load_id"`
	SiteID        string `json:"site_id"`
	SourceOrderID string `json:"source_order_id,omitempty"`
	Cutoff        string `json:"cutoff"`
	DetectedAt    string `json:"detected_at"`
	Version       int64  `json:"version"`
}

type bolIssuedData struct {
	BolNumber    string  `json:"bol_number"`
	LoadID       string  `json:"load_id"`
	SiteID       string  `json:"site_id"`
	Carrier      string  `json:"carrier"`
	CarrierScac  string  `json:"carrier_scac,omitempty"`
	TrailerID    string  `json:"trailer_id"`
	SealNumber   string  `json:"seal_number"`
	PackageCount int     `json:"package_count"`
	TotalWeightG float64 `json:"total_weight_g"`
	IssuedAt     string  `json:"issued_at"`
	Version      int64   `json:"version"`
}

type bolVoidedData struct {
	BolNumber string `json:"bol_number"`
	LoadID    string `json:"load_id"`
	SiteID    string `json:"site_id"`
	Reason    string `json:"reason"`
	VoidedAt  string `json:"voided_at"`
	Version   int64  `json:"version"`
}

// EncodePackageEvent maps one ShippingPackage fact to its outbox messages.
func EncodePackageEvent(id string, e shippingpackage.Event) ([]outbox.Message, error) {
	var data any
	name := e.EventName()
	subject := string(e.PackageID())
	occurred := e.OccurredAt()
	switch v := e.(type) {
	case shippingpackage.PackageStaged:
		data = packageStagedData{string(v.Package), string(v.LoadID), string(v.Site),
			v.StagingLocation, ts(v.StagedAt), v.Version}
	case shippingpackage.PackageLoaded:
		data = packageLoadedData{string(v.Package), string(v.LoadID),
			string(v.Site), ts(v.LoadedAt), v.Version}
	case shippingpackage.PackageUnloaded:
		data = packageUnloadedData{string(v.Package), string(v.LoadID),
			string(v.Site), ts(v.UnloadedAt), v.Reason, v.Version}
	case shippingpackage.PackageMissedLoad:
		data = packageMissedLoadData{string(v.Package), string(v.LoadID),
			string(v.Site), v.SourceOrderID, ts(v.Cutoff), ts(v.DetectedAt), v.Version}
	default:
		return nil, fmt.Errorf("events: unknown shipping package event %T", e)
	}
	return encodeBoth(id, EntityShippingPackage, name, subject, occurred, data)
}

// EncodeBolEvent maps one BillOfLading fact to its outbox messages.
func EncodeBolEvent(id string, e billoflading.Event) ([]outbox.Message, error) {
	var data any
	name := e.EventName()
	subject := string(e.BolNumber())
	occurred := e.OccurredAt()
	switch v := e.(type) {
	case billoflading.BillOfLadingIssued:
		data = bolIssuedData{string(v.Bol), string(v.LoadID), string(v.Site),
			v.Carrier, v.CarrierScac, string(v.TrailerID), v.SealNumber,
			v.PackageCount, v.TotalWeightG, ts(v.IssuedAt), v.Version}
	case billoflading.BillOfLadingVoided:
		data = bolVoidedData{string(v.Bol), string(v.LoadID), string(v.Site),
			v.Reason, ts(v.VoidedAt), v.Version}
	default:
		return nil, fmt.Errorf("events: unknown bill of lading event %T", e)
	}
	return encodeBoth(id, EntityBillofLading, name, subject, occurred, data)
}

// TypeOf exposes the full CloudEvents type of one entity/event pair (tests
// and the catalogue fitness check).
func TypeOf(entity, eventName string) string { return cloudevents.Type(entity, eventName) }
