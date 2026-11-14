package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestTelemetryEnv_Defaults(t *testing.T) {
	name, version, endpoint := telemetryEnv(func(string) string { return "" })

	if name != "outbound-shipping-mcp" {
		t.Errorf("service name = %q, want outbound-shipping-mcp (distinct from the api's series)", name)
	}
	if version != "" || endpoint != "" {
		t.Errorf("version=%q endpoint=%q, want both empty: telemetry is off unless configured", version, endpoint)
	}
}

func TestTelemetryEnv_FromEnvironment(t *testing.T) {
	env := map[string]string{
		"OTEL_SERVICE_NAME":           "custom-mcp",
		"SERVICE_VERSION":             "v1.2.3",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector:4317",
	}
	name, version, endpoint := telemetryEnv(func(k string) string { return env[k] })

	if name != "custom-mcp" || version != "v1.2.3" || endpoint != "otel-collector:4317" {
		t.Errorf("telemetryEnv = %q / %q / %q", name, version, endpoint)
	}
}

func TestNewLogger_StampsTraceAndSpanIDsOntoRecordsWrittenUnderASpan(t *testing.T) {
	var buf bytes.Buffer
	logger := newLoggerTo(&buf, "info")

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	logger.InfoContext(ctx, "tool called")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not valid JSON: %v", err)
	}
	if line["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || line["span_id"] != "00f067aa0ba902b7" {
		t.Errorf("trace_id/span_id = %v/%v, want the active span's ids", line["trace_id"], line["span_id"])
	}
}

// ADR-0016 Tier 1 on the MCP router: a request produces
// http.server.request.duration and /healthz keeps answering {"status":"ok"}.
func TestNewRouter_EmitsStandardHTTPServerMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	router := newRouter(http.NotFoundHandler(), nil, "outbound-shipping-mcp")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %q, want 200 {\"status\":\"ok\"}", rec.Code, rec.Body)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	var route string
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if m.Name != "http.server.request.duration" || !ok {
				continue
			}
			found = true
			if v, ok := hist.DataPoints[0].Attributes.Value(attribute.Key("http.route")); ok {
				route = v.AsString()
			}
		}
	}
	if !found {
		t.Fatal("http.server.request.duration not emitted by the MCP router")
	}
	if route != "/healthz" {
		t.Errorf("http.route = %q, want /healthz", route)
	}
}
