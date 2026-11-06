package events

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/claudioed/outbound-shipping/internal/application/outbox"
	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/domain/billoflading"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// Inserter is the write side of the transactional outbox the Sink needs;
// postgres.OutboxRepo implements it (cmd/ wires them, adapters never import
// each other). Called with the ctx handed to UnitOfWork.Do it joins that
// transaction.
type Inserter interface {
	Insert(ctx context.Context, msgs ...outbox.Message) error
}

// Sink implements ports.EventSink: each fact becomes one CloudEvents
// occurrence (a fresh UUID, minted once) written to the integration topic AND
// the analytics twin in ONE outbox Insert, so the rows land atomically with
// the aggregate.
type Sink struct {
	// Outbox receives the encoded messages.
	Outbox Inserter
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
}

var _ ports.EventSink = (*Sink)(nil)

// NewSink returns a Sink writing to out and minting random UUID v4 ids.
func NewSink(out Inserter) *Sink {
	return &Sink{Outbox: out, NewID: uuid.NewString}
}

// PublishLoadEvents enqueues the facts an OutboundLoad raised.
func (s *Sink) PublishLoadEvents(ctx context.Context, evs ...outboundload.Event) error {
	return s.publish(ctx, len(evs), func(i int) ([]outbox.Message, error) {
		return EncodeLoadEvent(s.id(), evs[i])
	})
}

// PublishPackageEvents enqueues the facts a ShippingPackage raised.
func (s *Sink) PublishPackageEvents(ctx context.Context, evs ...shippingpackage.Event) error {
	return s.publish(ctx, len(evs), func(i int) ([]outbox.Message, error) {
		return EncodePackageEvent(s.id(), evs[i])
	})
}

// PublishBolEvents enqueues the facts a BillOfLading raised.
func (s *Sink) PublishBolEvents(ctx context.Context, evs ...billoflading.Event) error {
	return s.publish(ctx, len(evs), func(i int) ([]outbox.Message, error) {
		return EncodeBolEvent(s.id(), evs[i])
	})
}

// id mints one CloudEvents id per occurrence.
func (s *Sink) id() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return uuid.NewString()
}

// publish encodes every fact and inserts all messages in one call.
func (s *Sink) publish(ctx context.Context, n int, encode func(int) ([]outbox.Message, error)) error {
	if n == 0 {
		return nil
	}
	msgs := make([]outbox.Message, 0, 2*n)
	for i := range n {
		encoded, err := encode(i)
		if err != nil {
			return fmt.Errorf("events: encode: %w", err)
		}
		msgs = append(msgs, encoded...)
	}
	return s.Outbox.Insert(ctx, msgs...)
}
