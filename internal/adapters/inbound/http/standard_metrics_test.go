package http_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inboundhttp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/http"
	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

const durationMetric = "http.server.request.duration"

// notFoundLoads is a LoadRepo whose every Get misses: enough to drive
// GET /loads/{loadId} to a 404 without a database. Any other method panics
// (nil embedded interface), which the router's recoverer would turn into a 500.
type notFoundLoads struct{ ports.LoadRepo }

func (notFoundLoads) Get(context.Context, shared.LoadID) (*outboundload.OutboundLoad, error) {
	return nil, repository.ErrLoadNotFound
}

// installMeterProvider swaps in a ManualReader MeterProvider for the test and
// returns a collector of the data points of one histogram. It must run BEFORE
// the router is built: otelchimetric binds to otel.GetMeterProvider() at
// construction time.
func installMeterProvider(t *testing.T) func(name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	return func(name string) []metricdata.HistogramDataPoint[float64] {
		t.Helper()
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatalf("collect metrics: %v", err)
		}
		var points []metricdata.HistogramDataPoint[float64]
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != name {
					continue
				}
				hist, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("%s is a %T, want a float64 Histogram", name, m.Data)
				}
				points = append(points, hist.DataPoints...)
			}
		}
		return points
	}
}

func newTestRouter(logger *slog.Logger) http.Handler {
	server := &inboundhttp.Server{Queries: &usecases.Queries{Loads: notFoundLoads{}}}
	return inboundhttp.NewRouter(server, logger, "outbound-shipping")
}

// ADR-0016 Tier 1: a request to the api produces http.server.request.duration,
// labelled with the chi route PATTERN (never the raw path, which would mint a
// series per load id).
func TestRouter_EmitsRequestDurationLabelledWithTheRoutePattern(t *testing.T) {
	collect := installMeterProvider(t)
	router := newTestRouter(nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/loads/load-CARDINALITY-1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /loads/{loadId} = %d, want 404", rec.Code)
	}

	points := collect(durationMetric)
	if len(points) != 1 {
		t.Fatalf("%s has %d data points, want 1: %+v", durationMetric, len(points), points)
	}
	route, ok := points[0].Attributes.Value(attribute.Key("http.route"))
	if !ok {
		t.Fatalf("data point has no http.route attribute: %v", points[0].Attributes)
	}
	if route.AsString() != "/loads/{loadId}" {
		t.Errorf("http.route = %q, want the pattern %q", route.AsString(), "/loads/{loadId}")
	}
	if points[0].Count != 1 {
		t.Errorf("count = %d, want 1", points[0].Count)
	}
}

func TestRouter_HealthzIsUnchangedByTheTelemetryMiddleware(t *testing.T) {
	installMeterProvider(t)
	router := newTestRouter(nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", rec.Code)
	}
	if got := bytes.TrimSpace(rec.Body.Bytes()); string(got) != `{"status":"ok"}` {
		t.Errorf("GET /healthz body = %s, want {\"status\":\"ok\"}", got)
	}
}

// spanCapturingHandler stands in for the trace-correlating slog handler the
// composition root installs (it lives in the outbound telemetry adapter,
// which this inbound adapter must not import). It records the span context
// each log record was written under.
type spanCapturingHandler struct {
	slog.Handler
	seen trace.SpanContext
}

func (h *spanCapturingHandler) Handle(ctx context.Context, record slog.Record) error {
	h.seen = trace.SpanContextFromContext(ctx)
	return h.Handler.Handle(ctx, record)
}

func tracedRouter(t *testing.T) (http.Handler, *tracetest.SpanRecorder, *spanCapturingHandler) {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	handler := &spanCapturingHandler{Handler: slog.NewJSONHandler(&bytes.Buffer{}, nil)}
	return newTestRouter(slog.New(handler)), recorder, handler
}

func TestRouter_SpanNameIsTheRoutePatternAndKindServer(t *testing.T) {
	router, recorder, _ := tracedRouter(t)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/loads/load-CARDINALITY-2", nil))

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	if spans[0].SpanKind() != trace.SpanKindServer {
		t.Errorf("span kind = %v, want server", spans[0].SpanKind())
	}
	if name := spans[0].Name(); name != "/loads/{loadId}" {
		t.Errorf("span name = %q, want the route pattern %q", name, "/loads/{loadId}")
	}
}

func TestRouter_RequestLogIsWrittenUnderTheRequestSpan(t *testing.T) {
	router, recorder, logHandler := tracedRouter(t)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	want := spans[0].SpanContext()
	if !logHandler.seen.IsValid() {
		t.Fatal("the request log was written with a context carrying no span: middleware order or the *Context logger variants are wrong")
	}
	if logHandler.seen.TraceID() != want.TraceID() || logHandler.seen.SpanID() != want.SpanID() {
		t.Errorf("log span = %s/%s, want %s/%s", logHandler.seen.TraceID(), logHandler.seen.SpanID(), want.TraceID(), want.SpanID())
	}
}
