package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/http"
	"github.com/claudioed/outbound-shipping/internal/application/ports"
	"github.com/claudioed/outbound-shipping/internal/application/repository"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
	"github.com/claudioed/outbound-shipping/internal/domain/shippingpackage"
)

// recordingMetrics is a ports.LoadMetrics that remembers every call.
type recordingMetrics struct {
	closed  int
	failure []string
}

func (m *recordingMetrics) LoadClosed(context.Context) { m.closed++ }
func (m *recordingMetrics) LoadCloseFailed(_ context.Context, reason string) {
	m.failure = append(m.failure, reason)
}

// The doubles below drive the REAL CloseLoad use case without a database.
type inlineUoW struct{}

func (inlineUoW) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type singleLoad struct {
	ports.LoadRepo
	load *outboundload.OutboundLoad
}

func (s singleLoad) Get(context.Context, shared.LoadID) (*outboundload.OutboundLoad, error) {
	return s.load, nil
}
func (singleLoad) Save(context.Context, *outboundload.OutboundLoad, int64) error { return nil }

type noPackages struct{ ports.PackageRepo }

func (noPackages) ListByLoad(context.Context, shared.LoadID, repository.PackageFilter, shared.PackageID, int) ([]*shippingpackage.ShippingPackage, error) {
	return nil, nil
}

type discardSink struct{ ports.EventSink }

func (discardSink) PublishLoadEvents(context.Context, ...outboundload.Event) error { return nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC) }

func rehydrated(t *testing.T, state outboundload.State) *outboundload.OutboundLoad {
	t.Helper()
	load, err := outboundload.Rehydrate(outboundload.RehydrateParams{
		ID: "load-1", Site: "site-1", Cpt: "cpt-1", OperatingDate: "2026-11-13",
		ShipMethod: "GROUND", Cutoff: time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC),
		State: state, Door: "D1", Trailer: "T1", Version: 3,
	})
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	return load
}

func closeRouter(t *testing.T, state outboundload.State, metrics ports.LoadMetrics) http.Handler {
	t.Helper()
	server := &inboundhttp.Server{
		Close: &usecases.CloseLoad{
			UoW: inlineUoW{}, Loads: singleLoad{load: rehydrated(t, state)},
			Pkgs: noPackages{}, Sink: discardSink{}, Clock: fixedClock{},
		},
		Metrics: metrics,
	}
	return inboundhttp.NewRouter(server, nil, "outbound-shipping")
}

func postClosure(router http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/loads/load-1/closures", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func TestCloseLoad_ASuccessfulCloseCountsAClosedLoad(t *testing.T) {
	metrics := &recordingMetrics{}

	rec := postClosure(closeRouter(t, outboundload.StateLoading, metrics))

	if rec.Code != http.StatusOK {
		t.Fatalf("POST closures = %d, want 200: %s", rec.Code, rec.Body)
	}
	if metrics.closed != 1 || len(metrics.failure) != 0 {
		t.Errorf("closed=%d failures=%v, want closed=1 and no failure", metrics.closed, metrics.failure)
	}
}

// A refused close records the RFC 7807 problem slug the client saw as reason.
func TestCloseLoad_ARefusedCloseCountsAFailureWithTheProblemSlug(t *testing.T) {
	metrics := &recordingMetrics{}

	rec := postClosure(closeRouter(t, outboundload.StatePlanned, metrics))

	if rec.Code != http.StatusConflict {
		t.Fatalf("POST closures = %d, want 409: %s", rec.Code, rec.Body)
	}
	if metrics.closed != 0 || len(metrics.failure) != 1 || metrics.failure[0] != "load-state" {
		t.Errorf("closed=%d failures=%v, want one failure with reason load-state", metrics.closed, metrics.failure)
	}
}

// An unreadable body never reaches the use case, but it is still a failed
// close attempt and carries its problem slug.
func TestCloseLoad_AMalformedBodyCountsAFailureToo(t *testing.T) {
	metrics := &recordingMetrics{}
	router := closeRouter(t, outboundload.StateLoading, metrics)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/loads/load-1/closures", strings.NewReader(`not json`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST closures = %d, want 400", rec.Code)
	}
	if len(metrics.failure) != 1 || metrics.failure[0] != "bad-request" {
		t.Errorf("failures = %v, want [bad-request]", metrics.failure)
	}
}

// Metrics is optional: a Server built without it (handler unit tests) must
// serve the close exactly as before.
func TestCloseLoad_WithoutMetricsTheCloseStillWorks(t *testing.T) {
	rec := postClosure(closeRouter(t, outboundload.StateLoading, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("POST closures without metrics = %d, want 200: %s", rec.Code, rec.Body)
	}
}
