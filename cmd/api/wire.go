package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	inboundhttp "github.com/claudioed/outbound-shipping/internal/adapters/inbound/http"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/events"
	outboundkafka "github.com/claudioed/outbound-shipping/internal/adapters/outbound/kafka"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/postgres"
	"github.com/claudioed/outbound-shipping/internal/adapters/outbound/system"
	"github.com/claudioed/outbound-shipping/internal/application/usecases"
)

// application is everything run() started: the HTTP server, the readiness
// gate, the six Kafka consumers and the outbox relay, over one Postgres pool.
type application struct {
	server    *http.Server
	readiness *inboundhttp.Readiness
	consumers []*worker
	relay     *worker
	relaySink *outboundkafka.RelaySink
	pool      closer
	logger    *slog.Logger
}

// wiredPolicies are the consumer use cases the Kafka dispatchers share.
type wiredPolicies struct {
	expect      *usecases.ExpectPackage
	divert      *usecases.DivertPackage
	book        *usecases.BookAppointment
	dock        *usecases.DockTrailer
	doorWork    *usecases.RecordDoorWork
	depart      *usecases.OnDeparture
	applyCpt    *usecases.ApplyCptSchedule
	applyLayout *usecases.ApplyLayout
	project     *usecases.ProjectBoard
}

// closer lets close() release the pool without importing pgxpool here.
type closer interface{ Close() }

// wire builds the whole service: migrations, adapters, use cases, the router
// and the background workers. Real wiring only -- Postgres and Kafka; a
// missing mandatory variable already failed in loadConfig.
func wire(ctx context.Context, cfg config, logger *slog.Logger) (*application, error) {
	migrationsURL := postgres.MigrationsURL(os.Getenv)
	if err := postgres.RunMigrations(migrationsURL); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	uow := postgres.NewUnitOfWork(pool)
	loads := postgres.NewLoadRepo(pool)
	pkgs := postgres.NewPackageRepo(pool)
	bols := postgres.NewBolRepo(pool)
	copies := postgres.NewCopies(pool)
	appts := postgres.NewAppointments(pool)
	processed := postgres.NewProcessedEventRepo(pool)
	idemStore := postgres.NewIdempotencyStore(pool)
	board := postgres.NewBoardStore(pool)
	outboxRepo := postgres.NewOutboxRepo(pool)

	sink := events.NewSink(outboxRepo)
	clock := system.Clock{}
	ids := system.RandomIDs{}
	intake := usecases.Intake{UoW: uow, Processed: processed}

	// The consumer policies.
	expect := &usecases.ExpectPackage{Intake: intake, Loads: loads, Packages: pkgs, Copies: copies, Sink: sink, IDs: ids, Clock: clock}
	divert := &usecases.DivertPackage{Intake: intake, Packages: pkgs, Clock: clock}
	book := &usecases.BookAppointment{Intake: intake, Loads: loads, Copies: appts, Sink: sink, Clock: clock}
	dock := &usecases.DockTrailer{Intake: intake, Loads: loads, Sink: sink, Clock: clock}
	depart := &usecases.OnDeparture{Intake: intake, Loads: loads, Packages: pkgs, Bols: bols, Copies: appts, Sink: sink, IDs: ids, Clock: clock}
	applyCpt := &usecases.ApplyCptSchedule{Intake: intake, Copies: copies}
	applyLayout := &usecases.ApplyLayout{Intake: intake, Copies: copies}
	project := &usecases.ProjectBoard{Intake: intake, Board: board}
	policies := wiredPolicies{
		expect:      expect,
		divert:      divert,
		book:        book,
		dock:        dock,
		doorWork:    &usecases.RecordDoorWork{Intake: intake, Copies: appts},
		depart:      depart,
		applyCpt:    applyCpt,
		applyLayout: applyLayout,
		project:     project,
	}

	// The REST commands and the reads (the same Queries the MCP adapter
	// serves; the projector feeds the board through its own consumer).
	queries := &usecases.Queries{Loads: loads, Pkgs: pkgs, Bols: bols, Board: board, Clock: clock}
	readiness := inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: kafkaCheck(cfg.Brokers)},
	)
	srv := buildHTTPServer(httpDeps{
		uow: uow, loads: loads, pkgs: pkgs, bols: bols, copies: copies,
		sink: sink, idemStore: idemStore, queries: queries,
		readiness: readiness, clock: clock,
	})

	consumers := startConsumers(cfg, logger, policies)
	relaySink, relay := startRelay(cfg, outboxRepo, logger)

	return &application{
		server: &http.Server{
			Addr: cfg.Addr, Handler: inboundhttp.NewRouter(srv), ReadHeaderTimeout: 5 * time.Second,
		},
		readiness: readiness,
		consumers: consumers,
		relay:     relay,
		relaySink: relaySink,
		pool:      pool,
		logger:    logger,
	}, nil
}

// httpDeps bundles what buildHTTPServer needs; it exists only to keep the
// helper's signature flat.
type httpDeps struct {
	uow       *postgres.UnitOfWork
	loads     *postgres.LoadRepo
	pkgs      *postgres.PackageRepo
	bols      *postgres.BolRepo
	copies    *postgres.Copies
	sink      *events.Sink
	idemStore *postgres.IdempotencyStore
	queries   *usecases.Queries
	readiness *inboundhttp.Readiness
	clock     system.Clock
}

// buildHTTPServer wires the REST adapter: the scan and close commands, the
// sweep, the reads and the Idempotency-Key middleware over the Postgres
// idempotency store.
func buildHTTPServer(d httpDeps) *inboundhttp.Server {
	idem := inboundhttp.NewIdempotency(d.idemStore, d.uow, d.clock)
	return &inboundhttp.Server{
		Stage:   &usecases.StagePackage{UoW: d.uow, Pkgs: d.pkgs, Layout: d.copies, Sink: d.sink, Clock: d.clock},
		Load:    &usecases.LoadPackage{UoW: d.uow, Pkgs: d.pkgs, Loads: d.loads, Sink: d.sink, Clock: d.clock},
		Unload:  &usecases.UnloadPackage{UoW: d.uow, Pkgs: d.pkgs, Sink: d.sink, Clock: d.clock},
		Close:   &usecases.CloseLoad{UoW: d.uow, Loads: d.loads, Pkgs: d.pkgs, Sink: d.sink, Clock: d.clock},
		Reopen:  &usecases.ReopenLoad{UoW: d.uow, Loads: d.loads, Sink: d.sink, Clock: d.clock},
		Cancel:  &usecases.CancelLoad{UoW: d.uow, Loads: d.loads, Pkgs: d.pkgs, Sink: d.sink, Clock: d.clock},
		VoidBol: &usecases.VoidBillOfLading{UoW: d.uow, Bols: d.bols, Sink: d.sink, Clock: d.clock},
		Sweep:   &usecases.SweepMissedLoads{UoW: d.uow, Pkgs: d.pkgs, Sink: d.sink, Clock: d.clock},
		Queries: d.queries,
		Idempotency: func(next http.Handler) http.Handler {
			return idem.Middleware(next)
		},
		Readiness: d.readiness,
	}
}

// close releases the relay's writer and the pool, after run() drained HTTP
// and stopped the workers.
func (a *application) close() {
	if a.relaySink != nil {
		if err := a.relaySink.Close(); err != nil {
			a.logger.Error("kafka relay sink close failed", "error", err)
		}
	}
	if a.pool != nil {
		a.pool.Close()
	}
}
