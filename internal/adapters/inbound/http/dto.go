package http

import (
	"time"

	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// Request bodies (apis/openapi.yaml schemas, snake_case JSON).

type stagePackageRequest struct {
	StagingLocation string `json:"staging_location"`
}

type loadPackageRequest struct {
	LoadID   string `json:"load_id"`
	DoorCode string `json:"door_code"`
}

type unloadPackageRequest struct {
	Reason string `json:"reason"`
}

type shortShipJSON struct {
	PackageID string `json:"package_id"`
	Reason    string `json:"reason"`
}

type closeLoadRequest struct {
	ShortShips []shortShipJSON `json:"short_ships"`
}

type reopenLoadRequest struct {
	Reason string `json:"reason"`
}

type cancelLoadRequest struct {
	Reason string `json:"reason"`
}

type voidBolRequest struct {
	Reason string `json:"reason"`
}

type missedLoadSweepRequest struct {
	SiteID string `json:"site_id"`
	Cutoff string `json:"cutoff"`
}

// Response bodies.

type shortShipResponse struct {
	PackageID string `json:"package_id"`
	Reason    string `json:"reason"`
}

type loadResponse struct {
	ID            string              `json:"id"`
	SiteID        string              `json:"site_id"`
	CptID         string              `json:"cpt_id"`
	OperatingDate string              `json:"operating_date"`
	ShipMethod    string              `json:"ship_method"`
	Cutoff        string              `json:"cutoff"`
	State         string              `json:"state"`
	DoorCode      string              `json:"door_code,omitempty"`
	TrailerID     string              `json:"trailer_id,omitempty"`
	AppointmentID string              `json:"appointment_id,omitempty"`
	ShortShips    []shortShipResponse `json:"short_ships"`
	PackageCount  int                 `json:"package_count"`
	Version       int64               `json:"version"`
}

type packageResponse struct {
	ID            string `json:"id"`
	SiteID        string `json:"site_id"`
	SourceOrderID string `json:"source_order_id,omitempty"`
	WorkUnitRef   string `json:"work_unit_ref,omitempty"`
	LoadID        string `json:"load_id,omitempty"`
	StagingLoc    string `json:"staging_location,omitempty"`
	State         string `json:"state"`
	Version       int64  `json:"version"`
}

type bolResponse struct {
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
	VoidedAt     string  `json:"voided_at,omitempty"`
	VoidReason   string  `json:"void_reason,omitempty"`
	State        string  `json:"state"`
	Version      int64   `json:"version"`
}

type sweepResponse struct {
	Flagged int    `json:"flagged"`
	SweptAt string `json:"swept_at"`
}

type loadPage struct {
	Items      []loadResponse `json:"items"`
	NextCursor *string        `json:"nextCursor"`
}

type packagePage struct {
	Items      []packageResponse `json:"items"`
	NextCursor *string           `json:"nextCursor"`
}

type adherenceRow struct {
	SiteID         string  `json:"site_id"`
	CptID          string  `json:"cpt_id"`
	OperatingDate  string  `json:"operating_date"`
	LoadsClosed    int64   `json:"loads_closed"`
	LoadsTotal     int64   `json:"loads_total"`
	PackagesMissed int64   `json:"packages_missed"`
	Adherence      float64 `json:"adherence"`
}

type adherenceReport struct {
	Rows        []adherenceRow `json:"rows"`
	GeneratedAt string         `json:"generated_at"`
}

// toLoadResponse maps a load aggregate to its wire shape.
func toLoadResponse(l *outboundload.OutboundLoad) loadResponse {
	shorts := make([]shortShipResponse, 0, len(l.ShortShips()))
	for _, s := range l.ShortShips() {
		shorts = append(shorts, shortShipResponse{PackageID: string(s.PackageID), Reason: s.Reason})
	}
	return loadResponse{
		ID: string(l.ID()), SiteID: string(l.SiteID()), CptID: string(l.CptID()),
		OperatingDate: string(l.OperatingDate()), ShipMethod: string(l.ShipMethod()),
		Cutoff: l.Cutoff().UTC().Format(time.RFC3339), State: string(l.State()),
		DoorCode: string(l.Door()), TrailerID: string(l.Trailer()),
		AppointmentID: string(l.Appointment()), ShortShips: shorts,
		PackageCount: l.PackageCount(), Version: l.Version(),
	}
}

// toPackageResponse maps a package aggregate to its wire shape.
func toPackageResponse(p *shippingpackage.ShippingPackage) packageResponse {
	return packageResponse{
		ID: string(p.ID()), SiteID: string(p.SiteID()),
		SourceOrderID: p.SourceOrderID(), WorkUnitRef: p.WorkUnitRef(),
		LoadID: string(p.LoadID()), StagingLoc: p.StagingLocation(),
		State: string(p.State()), Version: p.Version(),
	}
}

// toBolResponse maps a document aggregate to its wire shape.
func toBolResponse(b *billoflading.BillOfLading) bolResponse {
	resp := bolResponse{
		BolNumber: string(b.BolNumber()), LoadID: string(b.LoadID()),
		SiteID: string(b.SiteID()), Carrier: b.Carrier(),
		CarrierScac: b.CarrierScac(), TrailerID: string(b.TrailerID()),
		SealNumber: b.SealNumber(), PackageCount: b.PackageCount(),
		TotalWeightG: b.TotalWeightG(),
		IssuedAt:     b.IssuedAt().UTC().Format(time.RFC3339),
		VoidReason:   b.VoidReason(), State: string(b.State()), Version: b.Version(),
	}
	if !b.VoidedAt().IsZero() {
		resp.VoidedAt = b.VoidedAt().UTC().Format(time.RFC3339)
	}
	return resp
}

// pageOfLoadIDs and friends keep shared.PageID-free cursor plumbing local.
func nonNil(items []loadResponse) []loadResponse {
	if items == nil {
		return []loadResponse{}
	}
	return items
}

func nonNilPackages(items []packageResponse) []packageResponse {
	if items == nil {
		return []packageResponse{}
	}
	return items
}

func cursorOf(next string) *string {
	if next == "" {
		return nil
	}
	return &next
}
