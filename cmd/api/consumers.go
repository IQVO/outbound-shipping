package main

import (
	"context"
	"errors"
	"io"
	"log/slog"

	inboundkafka "github.com/claudioed/outbound-shipping/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/outbound-shipping/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/outbound-shipping/internal/adapters/outbound/outbox"
)

// worker is one started background loop: the channel closed once it
// returned, the cancel func that stops it and the closer that releases its
// kafka reader (nil when the loop owns nothing to close).
type worker struct {
	name   string
	done   chan struct{}
	stop   context.CancelFunc
	closer io.Closer
}

// startConsumers starts the six Kafka consumers: fulfillment (PackageManifested,
// PackageDiverted), dock scheduling (DockAppointmentBooked), yard (TrailerDocked,
// DoorWorkCompleted, TrailerDeparted), cpt, layout and this service's own
// analytics stream feeding the LoadBoardView projector. Kafka is dialled
// lazily by each consumer's first fetch, never here.
func startConsumers(cfg config, logger *slog.Logger, uc wiredPolicies) []*worker {
	new := func(name string, c *inboundkafka.Consumer) *worker {
		ctx, stop := context.WithCancel(context.Background())
		w := &worker{name: name, done: make(chan struct{}), stop: stop, closer: c}
		go func() {
			defer close(w.done)
			if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
				logger.Error("kafka consumer stopped", "consumer", name, "error", err)
			}
		}()
		return w
	}
	g := cfg.Groups
	return []*worker{
		new("fulfillment", inboundkafka.NewFulfillmentConsumer(cfg.Brokers, g["FULFILLMENT_CONSUMER_GROUP"],
			inboundkafka.FulfillmentUseCases{Expect: uc.expect, Divert: uc.divert}, logger)),
		new("dock", inboundkafka.NewDockConsumer(cfg.Brokers, g["DOCK_CONSUMER_GROUP"],
			inboundkafka.DockUseCases{Book: uc.book}, logger)),
		new("yard", inboundkafka.NewYardConsumer(cfg.Brokers, g["YARD_CONSUMER_GROUP"],
			inboundkafka.YardUseCases{Dock: uc.dock, DoorWork: uc.doorWork, Depart: uc.depart}, logger)),
		new("cpt", inboundkafka.NewCptConsumer(cfg.Brokers, g["CPT_CONSUMER_GROUP"],
			inboundkafka.CptUseCases{Apply: uc.applyCpt}, logger)),
		new("layout", inboundkafka.NewLayoutConsumer(cfg.Brokers, g["LAYOUT_CONSUMER_GROUP"],
			inboundkafka.LayoutUseCases{Zone: uc.applyLayout, Location: uc.applyLayout}, logger)),
		new("analytics", inboundkafka.NewAnalyticsConsumer(cfg.Brokers, g["ANALYTICS_CONSUMER_GROUP"],
			inboundkafka.ProjectorUseCases{Project: uc.project}, logger)),
	}
}

// startRelay starts the transactional-outbox relay: the only path events
// leave the service through. The sink's writer dials lazily on the first
// send.
func startRelay(cfg config, store outboxrelay.Store, logger *slog.Logger) (*outboundkafka.RelaySink, *worker) {
	sink := outboundkafka.NewRelaySink(cfg.Brokers)
	relay := outboxrelay.NewOutboxRelay(store, sink, logger,
		outboxrelay.WithInterval(cfg.OutboxInterval),
		outboxrelay.WithBatchSize(cfg.OutboxBatch),
		outboxrelay.WithMaxAttempts(cfg.OutboxMaxAttempt),
	)
	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{}), stop: stop}
	go func() {
		defer close(w.done)
		logger.Info("outbox relay running", "interval", cfg.OutboxInterval,
			"batch", cfg.OutboxBatch, "max_attempts", cfg.OutboxMaxAttempt)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return sink, w
}

// runConsumers blocks until ctx is done (the shutdown signal), then stops and
// awaits every worker in the fleet order: the consumers first -- they may
// still enqueue outbox rows while draining -- the relay LAST, then the
// readers' kafka resources. The relay sink's writer is released by close(),
// after the HTTP drain.
func (a *application) runConsumers(ctx context.Context) error {
	<-ctx.Done()
	for _, w := range a.consumers {
		w.stop()
	}
	a.relay.stop()
	for _, w := range append(a.consumers, a.relay) {
		<-w.done
		if w.closer != nil {
			if err := w.closer.Close(); err != nil {
				a.logger.Error("kafka consumer close failed", "consumer", w.name, "error", err)
			}
		}
	}
	return nil
}
