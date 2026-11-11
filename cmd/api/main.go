// Command api is the outbound-shipping service: the 13 REST routes of
// apis/openapi.yaml, the six Kafka consumers (fulfillment, dock scheduling,
// yard, cpt, layout, and this service's own analytics stream feeding the
// LoadBoardView projector), the transactional-outbox relay, and the
// liveness/readiness endpoints. Like every fleet runtime it has NO in-memory
// mode: a missing variable is a boot error naming it, and
// internal/architecture's TestRuntimeBinaryHasNoInMemoryAdapters fails the
// build if cmd/ ever imports the memory adapters.
//
// Environment:
//
//	DATABASE_URL                  REQUIRED. Postgres DSN (migrations run at boot)
//	KAFKA_BROKERS                 REQUIRED. Comma-separated brokers
//	FULFILLMENT_CONSUMER_GROUP    REQUIRED. fulfillment topic consumer group
//	DOCK_CONSUMER_GROUP           REQUIRED. dock topic consumer group
//	YARD_CONSUMER_GROUP           REQUIRED. yard topic consumer group
//	CPT_CONSUMER_GROUP            REQUIRED. cpt topic consumer group
//	LAYOUT_CONSUMER_GROUP         REQUIRED. layout topic consumer group
//	ANALYTICS_CONSUMER_GROUP      REQUIRED. projector's consumer group
//	ADDR                          listen address (default :8080)
//	LOG_LEVEL                     debug | info | warn (or warning) | error (default info)
//	OUTBOX_INTERVAL_MS            relay pass interval (default 250)
//	OUTBOX_BATCH                  relay batch size (default 100)
//	OUTBOX_MAX_ATTEMPTS           failed sends after which the relay excludes a row from
//	                              future claims, logs one ERROR and lets later rows flow;
//	                              re-arm with attempts = 0 (default 20; 0 = unlimited;
//	                              negative or non-numeric = default)
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// shutdownTimeout bounds the HTTP drain and the consumers' final commit.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := wire(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer app.close()

	// Draining flips readiness FIRST so the load balancer stops routing
	// before the listener closes (Readiness doc comment).
	go func() {
		<-ctx.Done()
		app.readiness.SetNotReady()
	}()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", cfg.Addr)
		if err := app.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	consumerErr := app.runConsumers(ctx)

	select {
	case err := <-errCh:
		return err
	default:
	}
	if consumerErr != nil && consumerErr != context.Canceled {
		return consumerErr
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return app.server.Shutdown(drainCtx)
}

// newLogger maps LOG_LEVEL onto slog levels; unknown values mean info.
func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
