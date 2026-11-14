package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/telemetry"
)

// loadClosureSeries is one data point of outbound.loads.closures.
type loadClosureSeries struct {
	outcome string
	reason  string
	value   int64
	keys    []string
}

// collectLoadClosures installs a ManualReader MeterProvider, builds the
// recorder, runs record and returns every data point of the counter.
func collectLoadClosures(t *testing.T, record func(context.Context, *telemetry.LoadMetrics)) []loadClosureSeries {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewLoadMetrics()
	if err != nil {
		t.Fatalf("NewLoadMetrics: %v", err)
	}
	ctx := context.Background()
	record(ctx, metrics)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var series []loadClosureSeries
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "outbound.loads.closures" {
				continue
			}
			if m.Unit != "{closure}" {
				t.Errorf("unit = %q, want the UCUM annotation {closure}", m.Unit)
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("outbound.loads.closures is a %T, want an int64 Sum", m.Data)
			}
			if !sum.IsMonotonic {
				t.Error("outbound.loads.closures must be a monotonic counter")
			}
			for _, point := range sum.DataPoints {
				series = append(series, seriesOf(point))
			}
		}
	}
	return series
}

func seriesOf(point metricdata.DataPoint[int64]) loadClosureSeries {
	s := loadClosureSeries{value: point.Value}
	iter := point.Attributes.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		s.keys = append(s.keys, string(kv.Key))
		switch kv.Key {
		case attribute.Key("outcome"):
			s.outcome = kv.Value.AsString()
		case attribute.Key("reason"):
			s.reason = kv.Value.AsString()
		}
	}
	return s
}

func TestLoadMetrics_ASuccessCarriesOutcomeClosedAndNoReason(t *testing.T) {
	series := collectLoadClosures(t, func(ctx context.Context, m *telemetry.LoadMetrics) {
		m.LoadClosed(ctx)
		m.LoadClosed(ctx)
	})

	if len(series) != 1 {
		t.Fatalf("got %d series, want 1: %+v", len(series), series)
	}
	got := series[0]
	if got.outcome != "closed" || got.value != 2 {
		t.Errorf("series = %+v, want outcome=closed value=2", got)
	}
	if len(got.keys) != 1 || got.keys[0] != "outcome" {
		t.Errorf("attribute keys = %v, want exactly [outcome]", got.keys)
	}
}

func TestLoadMetrics_AFailureCarriesOutcomeFailedAndTheProblemSlugAsReason(t *testing.T) {
	series := collectLoadClosures(t, func(ctx context.Context, m *telemetry.LoadMetrics) {
		m.LoadCloseFailed(ctx, "packages-outstanding")
		m.LoadCloseFailed(ctx, "packages-outstanding")
		m.LoadCloseFailed(ctx, "load-not-found")
	})

	byReason := map[string]loadClosureSeries{}
	for _, s := range series {
		byReason[s.reason] = s
		if s.outcome != "failed" {
			t.Errorf("series %+v: outcome = %q, want failed", s, s.outcome)
		}
		if len(s.keys) != 2 {
			t.Errorf("series %+v: attribute keys = %v, want exactly outcome and reason", s, s.keys)
		}
	}
	if byReason["packages-outstanding"].value != 2 || byReason["load-not-found"].value != 1 {
		t.Errorf("series by reason = %+v, want packages-outstanding=2 load-not-found=1", byReason)
	}
}
