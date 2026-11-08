package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// FulfillmentTopic is fulfillment-execution's integration topic.
const FulfillmentTopic = "warehouse.fulfillment.events"

// The fulfillment-execution types this service acts on, byte-identical to
// its AsyncAPI (origin/develop). Every other type on the topic is ignored.
const (
	TypePackageManifested = "com.warehouse.wes.fulfillment-execution.package.PackageManifested"
	TypePackageDiverted   = "com.warehouse.wes.fulfillment-execution.package.PackageDiverted"
)

// The payloads mirror fulfillment-execution's events (restated here, never
// imported). The four correlation fields are ADDITIVE (ADR 0003, the
// producer's wave-2 change): each is omitempty on the wire and the zero
// value here means "the producer did not know it" — the consumer tolerates
// BOTH shapes and never parses order_ref.

type packageManifestedData struct {
	PackageID     string  `json:"package_id"`
	OrderRef      string  `json:"order_ref"`
	SiteID        string  `json:"site_id"`
	SourceOrderID string  `json:"source_order_id"`
	Cpt           string  `json:"cpt"`
	WeightG       float64 `json:"weight_g"`
}

type packageDivertedData struct {
	PackageID string `json:"package_id"`
}

// FulfillmentUseCases are the two use cases the fulfillment consumer
// dispatches to.
type FulfillmentUseCases struct {
	Expect *usecases.ExpectPackage
	Divert *usecases.DivertPackage
}

// FulfillmentHandlers maps the fulfillment topic's consumed types to their
// use cases.
func FulfillmentHandlers(uc FulfillmentUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypePackageManifested: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[packageManifestedData](e)
			if err != nil {
				return err
			}
			at := envelopeTime(e)
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Expect.Handle(ctx, e.ID(), usecases.Manifest{
					PackageID:     shared.PackageID(d.PackageID),
					SiteID:        shared.SiteID(d.SiteID),
					SourceOrderID: d.SourceOrderID,
					OrderRef:      d.OrderRef,
					Cutoff:        parseInstant(d.Cpt),
					WeightG:       d.WeightG,
					ManifestedAt:  at,
				})
			})
		},
		TypePackageDiverted: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[packageDivertedData](e)
			if err != nil {
				return err
			}
			at := envelopeTime(e)
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Divert.Handle(ctx, e.ID(), shared.PackageID(d.PackageID), at)
			})
		},
	}
}

// NewFulfillmentConsumer reads fulfillment-execution's topic under groupID
// (FULFILLMENT_CONSUMER_GROUP, never a literal).
func NewFulfillmentConsumer(brokers []string, groupID string, uc FulfillmentUseCases, logger *slog.Logger) *Consumer {
	return NewFulfillmentConsumerForTopic(brokers, FulfillmentTopic, groupID, uc, logger)
}

// NewFulfillmentConsumerForTopic is NewFulfillmentConsumer on an explicit
// topic.
func NewFulfillmentConsumerForTopic(brokers []string, topic, groupID string, uc FulfillmentUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("fulfillment consumer", brokers, topic, groupID, FulfillmentHandlers(uc, logger), logger)
}

// envelopeTime reads the CloudEvents `time` attribute (zero when absent).
func envelopeTime(e ce.Event) time.Time {
	t := e.Time()
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

// parseInstant parses an RFC 3339 instant ("" stays zero: unknown).
func parseInstant(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
