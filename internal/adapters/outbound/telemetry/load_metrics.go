package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/outbound-shipping/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/outbound-shipping"

// loadClosureCounterName is the Tier 2 business metric (ADR 0008): every
// attempt of the supervisor's close
// command (POST /loads/{loadId}/closures), by outcome. A load that closes
// is the precondition of ship-confirm (ADR 0002), so a falling closed rate
// means loads are not reaching the dock-departure step; a rising failed
// rate with reason=packages-outstanding means packages are not being loaded
// or short-shipped before the cutoff, which is what the load-adherence
// report measures after the fact.
const loadClosureCounterName = "outbound.loads.closures"

// Attribute keys of the Tier 2 convention: `outcome` is what happened to the
// attempt, `reason` is why it failed (the RFC 7807 problem slug the client
// received, a bounded catalogue, so its cardinality is bounded too).
const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")
)

const (
	outcomeClosed = "closed"
	outcomeFailed = "failed"
)

// LoadMetrics implements ports.LoadMetrics against the global MeterProvider.
// Until Setup installs a real provider the global one is a no-op, so
// recording is cheap and safe in tests, local runs and with telemetry off.
type LoadMetrics struct {
	counter metric.Int64Counter
}

var _ ports.LoadMetrics = (*LoadMetrics)(nil)

// NewLoadMetrics registers the load-closure counter. It only fails if the
// instrument name is invalid, which is a programming error, not a runtime
// condition; a caller that would rather run un-instrumented than not at all
// can ignore the error and leave the port nil.
func NewLoadMetrics() (*LoadMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		loadClosureCounterName,
		metric.WithDescription("Attempts to close an outbound load, by outcome (closed or failed); failures carry the problem slug as reason."),
		metric.WithUnit("{closure}"),
	)
	if err != nil {
		return nil, err
	}
	return &LoadMetrics{counter: counter}, nil
}

// LoadClosed counts one successful close.
func (m *LoadMetrics) LoadClosed(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeClosed)))
}

// LoadCloseFailed counts one refused or failed close under reason.
func (m *LoadMetrics) LoadCloseFailed(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeFailed), reasonKey.String(reason)))
}
