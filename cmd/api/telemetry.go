package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/telemetry"
	"github.com/claudioed/outbound-shipping/internal/application/ports"
)

// telemetryFlushTimeout bounds the final export attempt. Without a deadline
// the exporter would retry against an unreachable Collector and stretch a
// shutdown out well past what an orchestrator will wait for.
const telemetryFlushTimeout = 5 * time.Second

// startTelemetry brings OpenTelemetry up and returns the flush func to
// defer. Export is non-blocking: an unreachable Collector costs telemetry,
// never availability. With no OTLP endpoint configured nothing is installed
// and the flush does nothing. A failed flush is logged, never returned:
// turning that into a non-zero exit would make every clean shutdown look
// like a crash.
func startTelemetry(logger *slog.Logger, cfg config) (func(), error) {
	shutdownTelemetry, err := telemetry.Setup(context.Background(), cfg.ServiceName, cfg.ServiceVersion, cfg.OTLPEndpoint)
	if err != nil {
		return nil, err
	}
	if cfg.OTLPEndpoint == "" {
		logger.Info("telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set", "service_name", cfg.ServiceName)
	} else {
		logger.Info("telemetry configured", "service_name", cfg.ServiceName, "otlp_endpoint", cfg.OTLPEndpoint)
	}
	return func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}, nil
}

// loadMetrics builds the Tier 2 load-closure counter. A registration error
// (an invalid instrument name: a programming error) is non-fatal: the
// service then runs without the counter, never without the close command,
// so the port is left nil rather than a typed-nil adapter.
func loadMetrics(logger *slog.Logger) ports.LoadMetrics {
	m, err := telemetry.NewLoadMetrics()
	if err != nil {
		logger.Warn("load metrics unavailable; the outbound.loads.closures counter is off", "error", err)
		return nil
	}
	return m
}
