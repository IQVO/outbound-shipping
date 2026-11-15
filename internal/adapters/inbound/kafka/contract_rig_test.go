package kafka

// The dispatch rig: the consumers' REAL handler maps over the same memory
// ports the use-case harness uses, so the archetype tests feed a producer's
// golden example bytes through Consumer.HandleMessage — the same path a
// live reader takes — and assert the durable effect, not a mock's call
// log. Same-name helpers in this file and the usecases package's own test
// harness are independent (different packages).

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/memory"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
	"github.com/claudioed/outbound-shipping/internal/domain/outboundload"
	"github.com/claudioed/outbound-shipping/internal/domain/shared"
)

// contractRig carries the shared ports and the six real handler maps.
type contractRig struct {
	loads     *memory.LoadRepo
	pkgs      *memory.PackageRepo
	copies    *memory.Copies
	appts     *memory.Appointments
	bols      *memory.BolRepo
	processed *memory.ProcessedEventRepo
	board     *memory.BoardStore
	rec       *memory.OutboxRecorder

	fulfillment, dock, yard, cpt, layout, analytics map[string]HandlerFunc
}

// fixedClock freezes time for every use case the rig wires.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs implements ports.LoadIDs deterministically.
type seqIDs struct{ n int }

func (i *seqIDs) NewLoadID() shared.LoadID {
	i.n++
	return shared.LoadID("load-ctest-" + time.Duration(i.n).String())
}

func (i *seqIDs) NewBolNumber(_ outboundload.OperatingDate) shared.BolNumber {
	i.n++
	return shared.BolNumber("BOL-ctest-" + time.Duration(i.n).String())
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newContractRig wires the real use cases over memory ports and returns
// their handler maps.
func newContractRig(t *testing.T) *contractRig {
	t.Helper()
	r := &contractRig{
		loads: memory.NewLoadRepo(), copies: memory.NewCopies(),
		appts: memory.NewAppointments(), bols: memory.NewBolRepo(),
		processed: memory.NewProcessedEventRepo(), board: memory.NewBoardStore(),
		rec: memory.NewOutboxRecorder(),
	}
	r.pkgs = memory.NewPackageRepo(r.loads)
	uow := memory.NewUnitOfWork(r.copies, r.processed, r.rec)
	sink := events.NewSink(r.rec)
	// The golden day of the pinned fulfillment examples (their cpt is
	// 2026-09-30T15:30Z): a load can only be planned for a cutoff still
	// ahead of the clock.
	intake := usecases.Intake{UoW: uow, Processed: r.processed}
	clock := fixedClock{at: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	logger := discardLogger()

	r.fulfillment = FulfillmentHandlers(FulfillmentUseCases{
		Expect: &usecases.ExpectPackage{Intake: intake, Loads: r.loads, Packages: r.pkgs,
			Copies: r.copies, Sink: sink, IDs: ids, Clock: clock},
		Divert: &usecases.DivertPackage{Intake: intake, Packages: r.pkgs, Clock: clock},
	}, logger)
	r.dock = DockHandlers(DockUseCases{
		Book: &usecases.BookAppointment{Intake: intake, Loads: r.loads, Copies: r.appts, Sink: sink, Clock: clock},
	}, logger)
	r.yard = YardHandlers(YardUseCases{
		Dock:     &usecases.DockTrailer{Intake: intake, Loads: r.loads, Sink: sink, Clock: clock},
		DoorWork: &usecases.RecordDoorWork{Intake: intake, Copies: r.appts},
		Depart: &usecases.OnDeparture{Intake: intake, Loads: r.loads, Packages: r.pkgs,
			Bols: r.bols, Copies: r.appts, Sink: sink, IDs: ids, Clock: clock},
	}, logger)
	r.cpt = CptHandlers(CptUseCases{
		Apply: &usecases.ApplyCptSchedule{Intake: intake, Copies: r.copies},
	}, logger)
	r.layout = LayoutHandlers(LayoutUseCases{
		Zone:     &usecases.ApplyLayout{Intake: intake, Copies: r.copies},
		Location: &usecases.ApplyLayout{Intake: intake, Copies: r.copies},
	}, logger)
	r.analytics = AnalyticsHandlers(ProjectorUseCases{
		Project: &usecases.ProjectBoard{Intake: intake, Board: r.board},
	}, logger)
	return r
}

// dispatch feeds raw Kafka message bytes through the REAL consumer
// (decode + handler lookup + use case) exactly as Run does.
func dispatch(t *testing.T, handlers map[string]HandlerFunc, name, topic string, raw []byte) error {
	t.Helper()
	c := &Consumer{Name: name, Topic: topic, Handlers: handlers, Logger: discardLogger()}
	return c.HandleMessage(context.Background(), raw)
}

// rawEvent re-serialises a CloudEvent to the structured-mode JSON bytes a
// Kafka message value carries, applying mutate first (nil = verbatim).
func rawEvent(t *testing.T, mutate func(map[string]any) map[string]any, ev ce.Event) []byte {
	t.Helper()
	var payload map[string]any
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal the event: %v", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal the event: %v", err)
	}
	if mutate != nil {
		payload = mutate(payload)
	}
	body, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal the mutated event: %v", err)
	}
	return body
}

// withID sets an explicit FRESH CloudEvents id on a perturbed clone: the
// id IS the idempotency key, so every clone that must not be deduped
// against another run gets its own (a verbatim golden keeps the pinned
// example's own id).
func withID(id string) func(map[string]any) map[string]any {
	return func(payload map[string]any) map[string]any {
		payload["id"] = id
		return payload
	}
}
