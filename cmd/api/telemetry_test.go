package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// The telemetry environment: names follow the OTel spec (OTEL_*) plus the
// fleet's SERVICE_VERSION. An unset endpoint means telemetry is off.
func TestLoadConfigTelemetryDefaults(t *testing.T) {
	cfg, err := loadConfig(requiredEnv(nil))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.ServiceName != "outbound-shipping" {
		t.Errorf("ServiceName = %q, want outbound-shipping", cfg.ServiceName)
	}
	if cfg.ServiceVersion != "" {
		t.Errorf("ServiceVersion = %q, want empty (Setup falls back to dev)", cfg.ServiceVersion)
	}
	if cfg.OTLPEndpoint != "" {
		t.Errorf("OTLPEndpoint = %q, want empty: telemetry is off unless configured", cfg.OTLPEndpoint)
	}
}

func TestLoadConfigTelemetryFromEnvironment(t *testing.T) {
	cfg, err := loadConfig(requiredEnv(map[string]string{
		"OTEL_SERVICE_NAME":           "outbound-shipping-staging",
		"SERVICE_VERSION":             "v1.2.3",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector:4317",
	}))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.ServiceName != "outbound-shipping-staging" || cfg.ServiceVersion != "v1.2.3" || cfg.OTLPEndpoint != "otel-collector:4317" {
		t.Errorf("telemetry config = %q / %q / %q", cfg.ServiceName, cfg.ServiceVersion, cfg.OTLPEndpoint)
	}
}

// Every log line written with a context that carries a span is stamped with
// trace_id/span_id: the logger the composition root builds must be the
// trace-correlating one.
func TestNewLogger_StampsTraceAndSpanIDsOntoRecordsWrittenUnderASpan(t *testing.T) {
	var buf bytes.Buffer
	logger := newLoggerTo(&buf, "info")

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	logger.InfoContext(ctx, "load closed")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if line["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || line["span_id"] != "00f067aa0ba902b7" {
		t.Errorf("trace_id/span_id = %v/%v, want the active span's ids", line["trace_id"], line["span_id"])
	}
}

func TestStartTelemetry_WithoutAnEndpointIsANoOp(t *testing.T) {
	var buf bytes.Buffer
	flush, err := startTelemetry(newLoggerTo(&buf, "info"), config{ServiceName: "outbound-shipping"})
	if err != nil {
		t.Fatalf("startTelemetry: %v", err)
	}
	flush() // must not block or panic.
}
