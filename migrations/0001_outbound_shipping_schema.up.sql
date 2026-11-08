-- 0001_outbound_shipping_schema.up.sql: outbound-shipping's OLTP schema
-- (wave 5).
--
-- Text keys that are compared or ordered in Go (ids, sites, doors) use the
-- "C" collation so ORDER BY and keyset comparisons follow byte order, the
-- same order the in-memory adapters use.

-- outbound_loads: one row per OutboundLoad aggregate. version starts at 1
-- (LoadPlanned) and guards every UPDATE (... WHERE version = <loaded>).
CREATE TABLE outbound_loads (
    load_id        TEXT COLLATE "C" PRIMARY KEY,
    site_id        TEXT COLLATE "C" NOT NULL,
    cpt_id         TEXT COLLATE "C" NOT NULL,
    operating_date TEXT COLLATE "C" NOT NULL,
    ship_method    TEXT COLLATE "C" NOT NULL,
    cutoff         TIMESTAMPTZ NOT NULL,
    state          TEXT        NOT NULL CHECK (state IN ('PLANNED', 'LOADING', 'CLOSED', 'SHIP_CONFIRMED', 'CANCELLED')),
    door_code      TEXT COLLATE "C" NOT NULL DEFAULT '',
    trailer_id     TEXT COLLATE "C" NOT NULL DEFAULT '',
    appointment_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    short_ships    JSONB       NOT NULL DEFAULT '[]',
    package_count  BIGINT      NOT NULL DEFAULT 0 CHECK (package_count >= 0),
    version        BIGINT      NOT NULL CHECK (version >= 1),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The one-open-load invariant (model.yaml, ErrDuplicateOpenLoad): at most ONE
-- open (PLANNED or LOADING) load per (site, cpt, operating date, ship
-- method). The use case checks first; this partial unique index is the
-- race backstop when two packages of one cutoff manifest simultaneously: the
-- losing INSERT fails here and the use case retries by re-reading the open
-- load (PLAN wave 5; proven by the concurrency integration test).
CREATE UNIQUE INDEX uq_open_load_per_cutoff
    ON outbound_loads (site_id, cpt_id, operating_date, ship_method)
    WHERE state IN ('PLANNED', 'LOADING');

-- GET /loads: keyset pages by (created_at, load_id) with site/state/date
-- filters.
CREATE INDEX idx_loads_listing
    ON outbound_loads (created_at DESC, load_id DESC);
CREATE INDEX idx_loads_site_state ON outbound_loads (site_id, state);
CREATE INDEX idx_loads_open_by_site_cpt
    ON outbound_loads (site_id, cpt_id)
    WHERE state IN ('PLANNED', 'LOADING');
-- DoorFromAppointment / StartOnDock: the open loads of one door.
CREATE INDEX idx_loads_open_by_site_door
    ON outbound_loads (site_id, door_code)
    WHERE state IN ('PLANNED', 'LOADING');
-- ConfirmOnDeparture: the load a departed trailer was attached to.
CREATE INDEX idx_loads_by_site_trailer
    ON outbound_loads (site_id, trailer_id) WHERE trailer_id <> '';

-- shipping_packages: one row per ShippingPackage aggregate (the whole
-- snapshot; staging_location and weight_g are the wave-5 additions the BOL
-- needs). version guards every UPDATE.
CREATE TABLE shipping_packages (
    package_id      TEXT COLLATE "C" PRIMARY KEY,
    site_id         TEXT COLLATE "C" NOT NULL,
    source_order_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    work_unit_ref   TEXT COLLATE "C" NOT NULL DEFAULT '',
    load_id         TEXT COLLATE "C" NOT NULL DEFAULT '',
    staging_location TEXT COLLATE "C" NOT NULL DEFAULT '',
    weight_g        DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (weight_g >= 0),
    state           TEXT        NOT NULL CHECK (state IN ('EXPECTED', 'STAGED', 'LOADED', 'SHIPPED', 'SHORT_SHIPPED')),
    missed_flagged  BOOLEAN     NOT NULL DEFAULT false,
    version         BIGINT      NOT NULL CHECK (version >= 1),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_packages_by_load ON shipping_packages (load_id);
CREATE INDEX idx_packages_load_state ON shipping_packages (load_id, state);
-- GET /loads/{id}/packages pages by package id within the load.
CREATE INDEX idx_packages_listing ON shipping_packages (load_id, package_id);
-- SweepMissedLoad: the EXPECTED/STAGED packages of a site on still-open
-- loads, plus the missed-load MCP read.
CREATE INDEX idx_packages_site_missed
    ON shipping_packages (site_id, state, missed_flagged);

-- bills_of_lading: one row per BillOfLading aggregate. One document per load
-- in v1 (ErrBolAlreadyIssued), enforced by the UNIQUE load_id.
CREATE TABLE bills_of_lading (
    bol_number    TEXT COLLATE "C" PRIMARY KEY,
    load_id       TEXT COLLATE "C" NOT NULL UNIQUE,
    site_id       TEXT COLLATE "C" NOT NULL,
    carrier       TEXT COLLATE "C" NOT NULL,
    carrier_scac  TEXT COLLATE "C" NOT NULL DEFAULT '',
    trailer_id    TEXT COLLATE "C" NOT NULL,
    seal_number   TEXT COLLATE "C" NOT NULL,
    package_count BIGINT      NOT NULL CHECK (package_count >= 1),
    total_weight_g DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (total_weight_g >= 0),
    issued_at     TIMESTAMPTZ NOT NULL,
    voided_at     TIMESTAMPTZ,
    void_reason   TEXT        NOT NULL DEFAULT '',
    state         TEXT        NOT NULL CHECK (state IN ('ISSUED', 'VOIDED')),
    version       BIGINT      NOT NULL CHECK (version >= 1)
);

CREATE INDEX idx_bols_by_load ON bills_of_lading (load_id);

-- cpt_schedules: the CutoffCopy (model.yaml read model), one row per site,
-- replaced wholesale by every applied CPTScheduleChanged. cutoffs is the
-- schedule's cutoff array verbatim (id, local_time, days_of_week,
-- ship_method); version is the event's version (last writer wins per site).
CREATE TABLE cpt_schedules (
    site_id    TEXT COLLATE "C" PRIMARY KEY,
    timezone   TEXT        NOT NULL,
    cutoffs    JSONB       NOT NULL,
    version    BIGINT      NOT NULL CHECK (version >= 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- zones + locations: the StagingLocationCopy. A location joins its zone
-- through zone_id at read time, so the two may arrive in any order; the site
-- of a location is its zone's site (the site_key convention: never parse a
-- code). An empty role means Storage, so only role = 'Staging' can stage.
CREATE TABLE zones (
    zone_id    TEXT PRIMARY KEY,
    site_code  TEXT COLLATE "C" NOT NULL,
    zone_code  TEXT COLLATE "C" NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE locations (
    location_code TEXT COLLATE "C" PRIMARY KEY,
    zone_id       TEXT        NOT NULL DEFAULT '',
    role          TEXT        NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_locations_zone ON locations (zone_id);

-- dock_appointments: the local copy of the OUTBOUND appointments acted on
-- (DoorFromAppointment). The load keeps only the appointment id; the carrier
-- and SCAC the bill of lading needs are read back here at ship-confirm.
CREATE TABLE dock_appointments (
    appointment_id TEXT COLLATE "C" PRIMARY KEY,
    site_id        TEXT COLLATE "C" NOT NULL,
    direction      TEXT        NOT NULL,
    door_code      TEXT COLLATE "C" NOT NULL,
    cpt_id         TEXT COLLATE "C" NOT NULL DEFAULT '',
    carrier        TEXT COLLATE "C" NOT NULL DEFAULT '',
    carrier_scac   TEXT COLLATE "C" NOT NULL DEFAULT '',
    window_start   TIMESTAMPTZ,
    window_end     TIMESTAMPTZ,
    version        BIGINT      NOT NULL CHECK (version >= 1),
    booked_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- door_work_completions: the DoorWorkCompleted copy (yard fixture contract).
-- Recorded for the load-adherence read model; no state change in v1.
CREATE TABLE door_work_completions (
    trailer_id     TEXT COLLATE "C" NOT NULL,
    site_id        TEXT COLLATE "C" NOT NULL,
    door_code      TEXT COLLATE "C" NOT NULL,
    appointment_id TEXT COLLATE "C" NOT NULL DEFAULT '',
    completed_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (trailer_id, completed_at)
);

-- processed_events: idempotency guard of the inbound consumers (the inbox).
-- (consumer, event_id) is claimed with INSERT ... ON CONFLICT DO NOTHING in
-- the SAME transaction as the effect, so a rollback un-claims it.
CREATE TABLE processed_events (
    consumer     TEXT        NOT NULL,
    event_id     TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- outbox_events: the transactional outbox (same shape as the fleet's). One
-- row per already-encoded Kafka message, inserted in the SAME transaction as
-- the aggregate; the relay drains unpublished rows to Kafka.
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- idempotency_keys: the answers of POSTs made with an Idempotency-Key. Only
-- 2xx and 4xx answers are stored, never 5xx; the first writer of a key wins.
CREATE TABLE idempotency_keys (
    key          TEXT PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    status       INTEGER     NOT NULL CHECK (status >= 200 AND status < 500),
    content_type TEXT        NOT NULL DEFAULT '',
    body         BYTEA       NOT NULL DEFAULT ''::bytea,
    created_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);

-- load_board: the LoadBoardView projection (model.yaml read model), folded by
-- this service's own projector from the analytics topic -- one row per load
-- with the counts behind GET /reports/load-adherence. closed_on_time is set
-- when LoadClosed arrives at or before the load's cutoff.
CREATE TABLE load_board (
    load_id        TEXT COLLATE "C" PRIMARY KEY,
    site_id        TEXT COLLATE "C" NOT NULL,
    cpt_id         TEXT COLLATE "C" NOT NULL,
    operating_date TEXT COLLATE "C" NOT NULL,
    ship_method    TEXT COLLATE "C" NOT NULL,
    cutoff         TIMESTAMPTZ NOT NULL,
    staged_count   BIGINT      NOT NULL DEFAULT 0,
    loaded_count   BIGINT      NOT NULL DEFAULT 0,
    packages_missed BIGINT     NOT NULL DEFAULT 0,
    closed_at      TIMESTAMPTZ,
    closed_on_time BOOLEAN     NOT NULL DEFAULT false,
    confirmed_at   TIMESTAMPTZ,
    cancelled_at   TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_load_board_report
    ON load_board (site_id, operating_date, cpt_id);
