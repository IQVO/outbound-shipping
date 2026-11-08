package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// AnalyticsTopic is this service's own analytics stream: the twin of every
// published event, the projector's only feed.
const AnalyticsTopic = "warehouse.outbound-shipping.analytics"

// analyticsTypePrefix is the prefix of every own type.
const analyticsTypePrefix = "com.warehouse.wms.outbound-shipping."

// boardEventData carries the projection facts of one own event, decoded
// once for every type (each field is set only by the events that carry it).
type boardEventData struct {
	LoadID        string  `json:"load_id"`
	SiteID        string  `json:"site_id"`
	CptID         string  `json:"cpt_id"`
	OperatingDate string  `json:"operating_date"`
	ShipMethod    string  `json:"ship_method"`
	Cutoff        string  `json:"cutoff"`
	ClosedAt      string  `json:"closed_at"`
	DepartedAt    string  `json:"departed_at"`
	CancelledAt   string  `json:"cancelled_at"`
	StagedAt      string  `json:"staged_at"`
	LoadedAt      string  `json:"loaded_at"`
	DetectedAt    string  `json:"detected_at"`
	StartedAt     string  `json:"started_at"`
	Version       float64 `json:"version"`
}

// ProjectorUseCases is the projector's use case.
type ProjectorUseCases struct {
	Project *usecases.ProjectBoard
}

// AnalyticsHandlers maps every own event type to the projector use case.
// Types the fold does not act on (LoadDoorAssigned, LoadingStarted,
// PackageUnloaded, BillOfLading*) are still claimed and folded, then ignored
// inside the use case — the board keeps them for future folds.
func AnalyticsHandlers(uc ProjectorUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	handlers := map[string]HandlerFunc{}
	for _, entity := range []string{"outboundload", "shippingpackage", "billoflading"} {
		for _, name := range []string{
			"LoadPlanned", "LoadDoorAssigned", "LoadingStarted", "LoadClosed",
			"LoadReopened", "LoadCancelled", "ShipmentConfirmed",
			"PackageStaged", "PackageLoaded", "PackageUnloaded",
			"PackageMissedLoad", "BillOfLadingIssued", "BillOfLadingVoided",
		} {
			eventType := analyticsTypePrefix + entity + "." + name
			handlers[eventType] = projectorHandler(uc, entity, name, logger)
		}
	}
	return handlers
}

func projectorHandler(uc ProjectorUseCases, entity, name string, logger *slog.Logger) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[boardEventData](e)
		if err != nil {
			return err
		}
		at := envelopeTime(e)
		fold := usecases.BoardFold{
			Name: name, Load: shared.LoadID(d.LoadID),
			Site: shared.SiteID(d.SiteID), Cpt: shared.CptID(d.CptID),
			Date: d.OperatingDate, Method: d.ShipMethod,
			Cutoff: parseInstant(d.Cutoff), At: at,
		}
		switch name {
		case "LoadClosed":
			fold.At = firstInstant(at, parseInstant(d.ClosedAt))
		case "ShipmentConfirmed":
			fold.At = firstInstant(at, parseInstant(d.DepartedAt))
		case "LoadCancelled":
			fold.At = firstInstant(at, parseInstant(d.CancelledAt))
		case "PackageStaged":
			fold.At = firstInstant(at, parseInstant(d.StagedAt))
		case "PackageLoaded":
			fold.At = firstInstant(at, parseInstant(d.LoadedAt))
		case "PackageMissedLoad":
			fold.At = firstInstant(at, parseInstant(d.DetectedAt))
		}
		return apply(ctx, logger, e, func() (usecases.Outcome, error) {
			return uc.Project.Handle(ctx, e.ID(), fold)
		})
	}
}

// firstInstant returns a when a is non-zero, else b.
func firstInstant(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

// NewAnalyticsConsumer reads this service's own analytics topic under
// groupID (ANALYTICS_CONSUMER_GROUP, never a literal).
func NewAnalyticsConsumer(brokers []string, groupID string, uc ProjectorUseCases, logger *slog.Logger) *Consumer {
	return NewAnalyticsConsumerForTopic(brokers, AnalyticsTopic, groupID, uc, logger)
}

// NewAnalyticsConsumerForTopic is NewAnalyticsConsumer on an explicit topic.
func NewAnalyticsConsumerForTopic(brokers []string, topic, groupID string, uc ProjectorUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("analytics consumer", brokers, topic, groupID, AnalyticsHandlers(uc, logger), logger)
}
