package kafka

import (
	"context"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// DockTopic is dock-scheduling's integration topic.
const DockTopic = "warehouse.dock-scheduling.events"

// TypeDockAppointmentBooked is the only dock-scheduling type this service
// acts on.
const TypeDockAppointmentBooked = "com.warehouse.wms.dock-scheduling.dockappointment.DockAppointmentBooked"

// The payload mirrors dock-scheduling's contract, pinned from the
// producer's origin/feature/service (campaign spike Q5; its develop
// AsyncAPI does not exist yet — re-pin from origin/develop before fleet
// wiring). cpt_id and carrier_scac are NULLABLE: an appointment without a
// cpt names no load. The extra fields (asn_numbers, shipment_refs,
// expected_seal_number, arrival_tolerance_minutes, provenance...) are
// ignored. The re-pin rule: a field this contract names that the merged
// producer contract does not carry is read omit-when-unknown (the zero
// value), never assumed.

type dockAppointmentBookedData struct {
	AppointmentID string `json:"appointment_id"`
	SiteID        string `json:"site_id"`
	Direction     string `json:"direction"`
	DoorCode      string `json:"door_code"`
	Carrier       string `json:"carrier"`
	CarrierScac   string `json:"carrier_scac"`
	CptID         string `json:"cpt_id"`
	WindowStart   string `json:"window_start"`
	WindowEnd     string `json:"window_end"`
	Version       int64  `json:"version"`
}

// DockUseCases is the use case the dock consumer dispatches to.
type DockUseCases struct {
	Book *usecases.BookAppointment
}

// DockHandlers maps the dock topic's consumed type to its use case.
func DockHandlers(uc DockUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeDockAppointmentBooked: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[dockAppointmentBookedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Book.Handle(ctx, e.ID(), usecases.Appointment{
					AppointmentID: shared.AppointmentID(d.AppointmentID),
					Site:          shared.SiteID(d.SiteID),
					Direction:     d.Direction,
					DoorCode:      shared.DoorCode(d.DoorCode),
					CptID:         shared.CptID(d.CptID),
					Carrier:       d.Carrier,
					CarrierScac:   d.CarrierScac,
					WindowStart:   parseInstant(d.WindowStart),
					WindowEnd:     parseInstant(d.WindowEnd),
					Version:       d.Version,
					BookedAt:      envelopeTime(e),
				})
			})
		},
	}
}

// NewDockConsumer reads dock-scheduling's topic under groupID
// (DOCK_CONSUMER_GROUP, never a literal).
func NewDockConsumer(brokers []string, groupID string, uc DockUseCases, logger *slog.Logger) *Consumer {
	return NewDockConsumerForTopic(brokers, DockTopic, groupID, uc, logger)
}

// NewDockConsumerForTopic is NewDockConsumer on an explicit topic.
func NewDockConsumerForTopic(brokers []string, topic, groupID string, uc DockUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("dock consumer", brokers, topic, groupID, DockHandlers(uc, logger), logger)
}

// YardTopic is yard-management's integration topic.
const YardTopic = "warehouse.yard-management.events"

// The yard-management types this service acts on, validated against the
// producer's merged AsyncAPI pinned in contracts/yard-management.asyncapi.yaml
// (producer_contract_test.go).
const (
	TypeTrailerDocked     = "com.warehouse.wms.yard-management.trailer.TrailerDocked"
	TypeDoorWorkCompleted = "com.warehouse.wms.yard-management.trailer.DoorWorkCompleted"
	TypeTrailerDeparted   = "com.warehouse.wms.yard-management.trailer.TrailerDeparted"
)

// The payloads mirror the yard contract's facts. The extra fields
// (visit_id, dwell_minutes, load_state, asn_numbers, appointment_id...) are
// ignored here. seal_number is optional: a departure without one confirms
// nothing (the BOL must certify the yard's seal).

type trailerDockedData struct {
	TrailerID string `json:"trailer_id"`
	SiteID    string `json:"site_id"`
	DoorCode  string `json:"door_code"`
	DockedAt  string `json:"docked_at"`
}

type doorWorkCompletedData struct {
	TrailerID     string `json:"trailer_id"`
	SiteID        string `json:"site_id"`
	DoorCode      string `json:"door_code"`
	AppointmentID string `json:"appointment_id"`
	CompletedAt   string `json:"completed_at"`
}

type trailerDepartedData struct {
	TrailerID  string `json:"trailer_id"`
	SiteID     string `json:"site_id"`
	DepartedAt string `json:"departed_at"`
	SealNumber string `json:"seal_number"`
}

// YardUseCases are the three use cases the yard consumer dispatches to.
type YardUseCases struct {
	Dock     *usecases.DockTrailer
	DoorWork *usecases.RecordDoorWork
	Depart   *usecases.OnDeparture
}

// YardHandlers maps the yard topic's consumed types to their use cases.
func YardHandlers(uc YardUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeTrailerDocked: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[trailerDockedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Dock.Handle(ctx, e.ID(), usecases.Docking{
					TrailerID: shared.TrailerID(d.TrailerID),
					Site:      shared.SiteID(d.SiteID),
					DoorCode:  shared.DoorCode(d.DoorCode),
					DockedAt:  parseInstant(d.DockedAt),
				})
			})
		},
		TypeDoorWorkCompleted: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[doorWorkCompletedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.DoorWork.Handle(ctx, e.ID(), repository.DoorWorkCompletion{
					TrailerID:     shared.TrailerID(d.TrailerID),
					Site:          shared.SiteID(d.SiteID),
					DoorCode:      shared.DoorCode(d.DoorCode),
					AppointmentID: shared.AppointmentID(d.AppointmentID),
					CompletedAt:   parseInstant(d.CompletedAt),
				})
			})
		},
		TypeTrailerDeparted: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[trailerDepartedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Depart.Handle(ctx, e.ID(), usecases.Departure{
					TrailerID:  shared.TrailerID(d.TrailerID),
					Site:       shared.SiteID(d.SiteID),
					SealNumber: d.SealNumber,
					DepartedAt: parseInstant(d.DepartedAt),
				})
			})
		},
	}
}

// NewYardConsumer reads yard-management's topic under groupID
// (YARD_CONSUMER_GROUP, never a literal).
func NewYardConsumer(brokers []string, groupID string, uc YardUseCases, logger *slog.Logger) *Consumer {
	return NewYardConsumerForTopic(brokers, YardTopic, groupID, uc, logger)
}

// NewYardConsumerForTopic is NewYardConsumer on an explicit topic.
func NewYardConsumerForTopic(brokers []string, topic, groupID string, uc YardUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("yard consumer", brokers, topic, groupID, YardHandlers(uc, logger), logger)
}
