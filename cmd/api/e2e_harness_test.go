//go:build integration

package main

// The shared harness of the cmd/api integration tests. It exists so the two
// journey files stay about behaviour, not plumbing.
//
// What it boots: the REAL run() of cmd/api/main.go, in-process, with nothing
// but the documented environment (DATABASE_URL, KAFKA_BROKERS and the six
// *_CONSUMER_GROUP variables — cmd/api/main.go's Environment note and
// docs/docs/operations/configuration.md) over testcontainers Postgres
// (internal/testing/pgtest) and testcontainers Kafka
// (internal/testing/kafkatest), per .claude/skills/how-to-test/SKILL.md: no
// os.Getenv skip-gates, no localhost DSNs, one container per binary.
//
// Shutdown: run() installs signal.NotifyContext(SIGINT, SIGTERM) itself, so
// the harness cancels it the way a deployment does — SIGTERM — and run()
// then drains (readiness first, consumers, the outbox relay LAST, then HTTP;
// cmd/api/main.go and consumers.go runConsumers).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/outbound-shipping/internal/testing/kafkatest"
	"github.com/claudioed/outbound-shipping/internal/testing/pgtest"
)

// The topics of apis/asyncapi.yaml and docs/docs/api-reference/events.md: the
// five consumed integration streams plus the two this service publishes.
var fleetTopics = []string{
	"warehouse.fulfillment.events",
	"warehouse.process-path-management.events",
	"warehouse.facility.events",
	"warehouse.dock-scheduling.events",
	"warehouse.yard-management.events",
	"warehouse.outbound-shipping.events",
	"warehouse.outbound-shipping.analytics",
}

const (
	topicIntegration = "warehouse.outbound-shipping.events"
	topicAnalytics   = "warehouse.outbound-shipping.analytics"
	ceSource         = "/warehouse/outbound-shipping"
	ceTypePrefix     = "com.warehouse.wms.outbound-shipping."
	ceContentType    = "application/cloudevents+json; charset=UTF-8"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	kafkatest.Shutdown()
	os.Exit(code)
}

var (
	topicsOnce sync.Once
	bootSeq    atomic.Int64
)

// ensureTopics pre-creates the seven topics the binary uses. The kafkatest
// broker has topic auto-creation OFF and "the service never calls a
// topic-admin API" (internal/testing/kafkatest), which mirrors the cluster
// where warehouse-infra pre-created all seven topics before the first sync
// (docs/docs/ecosystem/integration.md).
func ensureTopics(t *testing.T) {
	t.Helper()
	topicsOnce.Do(func() {
		for _, tp := range fleetTopics {
			kafkatest.CreateTopic(t, tp)
		}
	})
}

// apiEnv is the documented minimum environment of cmd/api: DATABASE_URL,
// KAFKA_BROKERS and the six consumer-group variables (cmd/api/config.go
// groupVars). Group ids are unique per boot so a later boot reads each topic
// from offset 0 into its own fresh database — the documented behaviour of a
// brand-new stable consumer group. The tag comes back so a test can wait on
// its own consumers' committed offsets (see waitCommitted).
func apiEnv(t *testing.T, databaseURL string) (map[string]string, string) {
	t.Helper()
	tag := fmt.Sprintf("it%d", bootSeq.Add(1))
	env := map[string]string{
		"DATABASE_URL":  databaseURL,
		"KAFKA_BROKERS": strings.Join(kafkatest.Brokers(t), ","),
	}
	for _, key := range groupVars {
		suffix := strings.ToLower(strings.TrimSuffix(key, "_CONSUMER_GROUP"))
		env[key] = tag + "-" + suffix
	}
	return env, tag
}

// apiService is one booted instance of the real entry point.
type apiService struct {
	base  string
	group string // this boot's consumer-group tag
	stop  func() error
}

// bootAPI starts run() with env only and waits for /readyz to report ready
// (200 {"status":"ready"} — internal/adapters/inbound/http/readiness.go).
func bootAPI(t *testing.T, env map[string]string, groupTag string) *apiService {
	t.Helper()
	ensureTopics(t)
	addr := freeAddr(t)
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Setenv("ADDR", addr)

	done := make(chan error, 1)
	go func() { done <- run() }()

	var (
		stopOnce sync.Once
		stopErr  error
	)
	s := &apiService{base: "http://" + addr, group: groupTag, stop: func() error {
		stopOnce.Do(func() {
			// The documented shutdown trigger: run() owns a
			// signal.NotifyContext(SIGINT, SIGTERM); delivering SIGTERM to
			// this process is cancelled by that handler, never terminal.
			if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
				stopErr = fmt.Errorf("send SIGTERM: %w", err)
				return
			}
			select {
			case stopErr = <-done:
			case <-time.After(60 * time.Second):
				stopErr = fmt.Errorf("the api did not drain within 60s")
			}
		})
		return stopErr
	}}
	t.Cleanup(func() { _ = s.stop() })

	eventually(t, 90*time.Second, "/readyz to report ready", func() bool {
		resp, err := http.Get(s.base + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	})
	return s
}

// waitCommitted waits until this boot's <suffix> consumer group has
// committed at least n offsets on topic: the deterministic way to know a
// fixture was applied before sending one whose handling depends on it.
// Ordering between topics is NOT guaranteed (docs/docs/ecosystem/
// integration.md): a manifest that arrives before its site's schedule copy
// is ignored, and no load is planned, so the test sequences the fixtures
// through the group's committed offset, never a sleep.
func (s *apiService) waitCommitted(t *testing.T, suffix, topic string, n int64) {
	t.Helper()
	eventually(t, 30*time.Second, fmt.Sprintf("%s to commit %d offsets on %s", suffix, n, topic), func() bool {
		return kafkatest.CommittedOffset(t, s.group+"-"+suffix, topic) >= n
	})
}

// eventually retries cond until it holds or the deadline passes.
func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// eventuallyDetail is eventually with the last observed detail (e.g. the
// response body) folded into the timeout failure.
func eventuallyDetail(t *testing.T, within time.Duration, what string, cond func() (bool, string)) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		ok, detail := cond()
		if ok {
			return
		}
		if detail != "" {
			what = detail
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// reply is one REST exchange: status, body and headers.
type reply struct {
	status int
	body   string
	header http.Header
}

// call drives the documented REST surface of apis/openapi.yaml: every POST
// carries an Idempotency-Key (the middleware rule, AGENTS.md item 9).
func (s *apiService) call(t *testing.T, method, path, key, body string) reply {
	t.Helper()
	req, err := http.NewRequest(method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: resp.StatusCode, body: string(b), header: resp.Header}
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("not JSON: %s", r.body)
	}
	return m
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// fixtureEvent shapes one consumed CloudEvents 1.0 occurrence in structured
// JSON, the envelope every consumer decodes (docs/docs/api-reference/events.md
// "Envelope"; the same shape internal/adapters/inbound/kafka's own contract
// tests parse). key = subject, as the producers hash-partition.
func fixtureEvent(id, source, typ, subject, data string) kafkatest.Message {
	value := fmt.Sprintf(`{"specversion":"1.0","id":%q,"source":%q,"type":%q,"subject":%q,`+
		`"time":%q,"datacontenttype":"application/json","data":%s}`,
		id, source, typ, subject, time.Now().UTC().Format(time.RFC3339Nano), data)
	return kafkatest.Message{
		Key:     subject,
		Value:   []byte(value),
		Headers: map[string]string{"content-type": ceContentType},
	}
}

// cloudEvent is one published occurrence, decoded for assertions.
type cloudEvent struct {
	id         string
	typ        string
	subject    string
	dataschema string
	data       map[string]any
}

// decodeCloudEvent parses a published message and checks the envelope every
// outgoing message must carry: specversion 1.0, source
// /warehouse/outbound-shipping, the type catalogue prefix, subject = Kafka
// key = aggregate id, the content-type header and the stream-tagged
// dataschema (ADR 0005, docs/docs/api-reference/events.md).
func decodeCloudEvent(t *testing.T, m kafkatest.Message, stream string) cloudEvent {
	t.Helper()
	if got := m.Headers["content-type"]; got != ceContentType {
		t.Fatalf("content-type header of %s = %q, want %q", m.Key, got, ceContentType)
	}
	var raw struct {
		SpecVersion    string         `json:"specversion"`
		ID             string         `json:"id"`
		Source         string         `json:"source"`
		Type           string         `json:"type"`
		Subject        string         `json:"subject"`
		Time           string         `json:"time"`
		Datacontenttyp string         `json:"datacontenttype"`
		Dataschema     string         `json:"dataschema"`
		Data           map[string]any `json:"data"`
	}
	if err := json.Unmarshal(m.Value, &raw); err != nil {
		t.Fatalf("decode %s: %v", string(m.Value), err)
	}
	name := raw.Type[strings.LastIndex(raw.Type, ".")+1:]
	switch {
	case raw.SpecVersion != "1.0":
		t.Fatalf("specversion = %q", raw.SpecVersion)
	case raw.ID == "":
		t.Fatal("id is empty")
	case raw.Source != ceSource:
		t.Fatalf("source = %q, want %q", raw.Source, ceSource)
	case !strings.HasPrefix(raw.Type, ceTypePrefix):
		t.Fatalf("type %q is outside the catalogue prefix", raw.Type)
	case raw.Subject == "" || raw.Subject != string(m.Key):
		t.Fatalf("subject %q must equal the Kafka key %q", raw.Subject, m.Key)
	case raw.Datacontenttyp != "application/json":
		t.Fatalf("datacontenttype = %q", raw.Datacontenttyp)
	case raw.Dataschema != fmt.Sprintf("urn:warehouse:outbound-shipping:%s:%s:v1", stream, name):
		t.Fatalf("dataschema = %q", raw.Dataschema)
	case raw.Time == "":
		t.Fatal("time is empty")
	}
	if v, ok := raw.Data["version"].(float64); !ok || v < 1 {
		t.Fatalf("%s data carries no aggregate version: %v", raw.Type, raw.Data)
	}
	return cloudEvent{id: raw.ID, typ: raw.Type, subject: raw.Subject, dataschema: raw.Dataschema, data: raw.Data}
}

// dedupeEvents keeps the first occurrence per CloudEvents id: the relay is
// at-least-once, so a retried send can legitimately appear twice under the
// same id (docs/docs/ecosystem/integration.md, publishing guarantees).
func dedupeEvents(t *testing.T, msgs []kafkatest.Message, stream string) []cloudEvent {
	t.Helper()
	seen := map[string]bool{}
	var out []cloudEvent
	for _, m := range msgs {
		ev := decodeCloudEvent(t, m, stream)
		if seen[ev.id] {
			continue
		}
		seen[ev.id] = true
		out = append(out, ev)
	}
	return out
}

// typeCounts is the multiset of published types, for order-agnostic checks.
func typeCounts(evs []cloudEvent) map[string]int {
	counts := map[string]int{}
	for _, e := range evs {
		counts[strings.TrimPrefix(e.typ, ceTypePrefix)]++
	}
	return counts
}

// awaitEvents reads topic's single partition from offset until want returns
// true for everything read so far, bounded by a deadline — no Kafka read in
// these tests is ever unbounded. It fails with what it saw on timeout.
func awaitEvents(t *testing.T, topic string, offset int64, within time.Duration, want func([]kafkatest.Message) bool) []kafkatest.Message {
	t.Helper()
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: kafkatest.Brokers(t), Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20,
	})
	defer func() { _ = reader.Close() }()
	if err := reader.SetOffset(offset); err != nil {
		t.Fatalf("seek %s to %d: %v", topic, offset, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	var msgs []kafkatest.Message
	for {
		m, err := reader.ReadMessage(ctx)
		if err != nil {
			kinds := map[string]int{}
			for _, got := range msgs {
				var raw struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(got.Value, &raw)
				kinds[raw.Type]++
			}
			t.Fatalf("timed out after %s having read %d messages from %s (last error %v; saw %v)",
				within, len(msgs), topic, err, kinds)
		}
		headers := make(map[string]string, len(m.Headers))
		for _, h := range m.Headers {
			headers[h.Key] = string(h.Value)
		}
		msgs = append(msgs, kafkatest.Message{Key: string(m.Key), Value: m.Value, Headers: headers})
		if want(msgs) {
			return msgs
		}
	}
}
