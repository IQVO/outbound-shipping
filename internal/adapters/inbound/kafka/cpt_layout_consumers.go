package kafka

import (
	"context"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// CptTopic is process-path-management's integration topic.
const CptTopic = "warehouse.process-path-management.events"

// TypeCPTScheduleChanged is the only process-path-management type this
// service acts on.
const TypeCPTScheduleChanged = "com.warehouse.wes.process-path-management.cptschedule.CPTScheduleChanged"

// The payload mirrors process-path-management's event. cutoffs carries the
// schedule verbatim; the extra fields of each entry (eligible_path_ids...)
// are ignored.

type cptCutoff struct {
	CptID      string   `json:"cpt_id"`
	LocalTime  string   `json:"local_time"`
	DaysOfWeek []string `json:"days_of_week"`
	ShipMethod string   `json:"ship_method"`
}

// cptVersion is the version stored with a schedule copy. process-path-management's
// CPTScheduleChanged is a full snapshot keyed by site_id and carries NO version
// field (its AsyncAPI has none), so a real payload decodes to 0; the copy is
// replaced wholesale, last writer wins per site, so the number orders nothing.
// Treating 0 as invalid dead-lettered every real event and no load could ever
// be planned. A producer that does send a version keeps it.
func cptVersion(v int64) int64 {
	if v < 1 {
		return 1
	}
	return v
}

type cptScheduleChangedData struct {
	SiteID   string      `json:"site_id"`
	Timezone string      `json:"timezone"`
	Cutoffs  []cptCutoff `json:"cutoffs"`
	Version  int64       `json:"version"`
}

// CptUseCases is the use case the cpt consumer dispatches to.
type CptUseCases struct {
	Apply *usecases.ApplyCptSchedule
}

// CptHandlers maps the cpt topic's consumed type to its use case.
func CptHandlers(uc CptUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeCPTScheduleChanged: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[cptScheduleChangedData](e)
			if err != nil {
				return err
			}
			entries := make([]repository.CutoffEntry, 0, len(d.Cutoffs))
			for _, c := range d.Cutoffs {
				entries = append(entries, repository.CutoffEntry{
					CptID: c.CptID, LocalTime: c.LocalTime,
					DaysOfWeek: c.DaysOfWeek,
					ShipMethod: shared.ShipMethod(c.ShipMethod),
				})
			}
			at := envelopeTime(e)
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Apply.Handle(ctx, e.ID(), usecases.CptChange{
					SiteID:   shared.SiteID(d.SiteID),
					Timezone: d.Timezone,
					Cutoffs:  entries,
					Version:  cptVersion(d.Version),
					Changed:  at,
				})
			})
		},
	}
}

// NewCptConsumer reads process-path-management's topic under groupID
// (CPT_CONSUMER_GROUP, never a literal).
func NewCptConsumer(brokers []string, groupID string, uc CptUseCases, logger *slog.Logger) *Consumer {
	return NewCptConsumerForTopic(brokers, CptTopic, groupID, uc, logger)
}

// NewCptConsumerForTopic is NewCptConsumer on an explicit topic.
func NewCptConsumerForTopic(brokers []string, topic, groupID string, uc CptUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("cpt consumer", brokers, topic, groupID, CptHandlers(uc, logger), logger)
}

// LayoutTopic is facility-layout's integration topic.
const LayoutTopic = "warehouse.facility.events"

// The facility-layout types this service acts on, byte-identical to its
// AsyncAPI. Every other type on the topic is ignored.
const (
	TypeZoneRegistered         = "com.warehouse.wms.facility-layout.zone.ZoneRegistered"
	TypeLocationSlotRegistered = "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"
)

// The payloads mirror facility-layout's v1 events (camelCase, restated
// here, never imported). Their extra fields (areaCode, aisleId,
// locationType, dockFlow...) are ignored; `role` is absent for a pre-role
// location (Storage, so never Staging).

type zoneRegisteredData struct {
	ZoneID   string `json:"zoneId"`
	SiteCode string `json:"siteCode"`
	ZoneCode string `json:"zoneCode"`
}

type locationSlotRegisteredData struct {
	LocationCode string `json:"locationCode"`
	ZoneID       string `json:"zoneId"`
	Role         string `json:"role"`
}

// LayoutUseCases are the two use cases the layout consumer dispatches to.
type LayoutUseCases struct {
	Zone     *usecases.ApplyLayout
	Location *usecases.ApplyLayout
}

// LayoutHandlers maps the facility topic's consumed types to their use cases.
func LayoutHandlers(uc LayoutUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeZoneRegistered: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[zoneRegisteredData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Zone.HandleZone(ctx, e.ID(), usecases.ZoneChange{
					ZoneID: d.ZoneID, SiteCode: d.SiteCode, ZoneCode: d.ZoneCode,
				})
			})
		},
		TypeLocationSlotRegistered: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[locationSlotRegisteredData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Location.HandleLocation(ctx, e.ID(), usecases.LocationChange{
					Code: d.LocationCode, ZoneID: d.ZoneID, Role: d.Role,
				})
			})
		},
	}
}

// NewLayoutConsumer reads facility-layout's topic under groupID
// (LAYOUT_CONSUMER_GROUP, never a literal).
func NewLayoutConsumer(brokers []string, groupID string, uc LayoutUseCases, logger *slog.Logger) *Consumer {
	return NewLayoutConsumerForTopic(brokers, LayoutTopic, groupID, uc, logger)
}

// NewLayoutConsumerForTopic is NewLayoutConsumer on an explicit topic.
func NewLayoutConsumerForTopic(brokers []string, topic, groupID string, uc LayoutUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("layout consumer", brokers, topic, groupID, LayoutHandlers(uc, logger), logger)
}
