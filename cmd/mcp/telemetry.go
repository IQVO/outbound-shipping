package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/telemetry"
)

// defaultServiceName is this binary's service.name. It differs from the api's
// ("outbound-shipping") so the two Deployments never share one metric series;
// the chart sets OTEL_SERVICE_NAME explicitly and this is the fallback for a
// locally run binary.
const defaultServiceName = "outbound-shipping-mcp"

// telemetryFlushTimeout bounds the final export attempt, so an unreachable
// Collector cannot stretch a shutdown past what an orchestrator will wait for.
const telemetryFlushTimeout = 5 * time.Second

// telemetryEnv reads the OpenTelemetry environment: OTEL_SERVICE_NAME,
// SERVICE_VERSION and OTEL_EXPORTER_OTLP_ENDPOINT. An empty endpoint means
// telemetry is off; an empty version falls back to "dev" inside Setup.
func telemetryEnv(getenv func(string) string) (serviceName, serviceVersion, otlpEndpoint string) {
	serviceName = getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = defaultServiceName
	}
	return serviceName, getenv("SERVICE_VERSION"), getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
}

// startTelemetry brings OpenTelemetry up and returns the flush func to defer.
// Export is non-blocking: an unreachable Collector costs telemetry, never
// availability. A failed flush is logged, never returned: turning that into a
// non-zero exit would make every clean shutdown look like a crash.
func startTelemetry(logger *slog.Logger, serviceName, serviceVersion, otlpEndpoint string) (func(), error) {
	shutdownTelemetry, err := telemetry.Setup(context.Background(), serviceName, serviceVersion, otlpEndpoint)
	if err != nil {
		return nil, err
	}
	if otlpEndpoint == "" {
		logger.Info("telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set", "service_name", serviceName)
	} else {
		logger.Info("telemetry configured", "service_name", serviceName, "otlp_endpoint", otlpEndpoint)
	}
	return func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}, nil
}
