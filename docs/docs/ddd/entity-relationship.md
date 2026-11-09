---
id: entity-relationship
title: Entity relationship
sidebar_label: Entity relationship
description: "The PostgreSQL schema of outbound-shipping: aggregate tables, local copies, reliability tables and the load board."
---

# Entity relationship

One PostgreSQL schema, created by `migrations/0001_outbound_shipping_schema.up.sql` (embedded in the binaries, applied at boot). There are no foreign keys: aggregates reference each other by id and the local copies are joined in Go or by key. The relationships drawn below are logical.

```mermaid
erDiagram
    outbound_loads ||--o{ shipping_packages : "load_id"
    outbound_loads ||--o| bills_of_lading : "load_id unique"
    outbound_loads }o--o| dock_appointments : "appointment_id"
    outbound_loads ||--o| load_board : "load_id"
    outbound_loads }o--|| cpt_schedules : "site_id, cpt_id in cutoffs"
    shipping_packages }o--o| locations : "staging_location"
    locations }o--o| zones : "zone_id"

    outbound_loads {
        text load_id PK
        text site_id
        text cpt_id
        text operating_date
        text ship_method
        timestamptz cutoff
        text state
        text door_code
        text trailer_id
        text appointment_id
        jsonb short_ships
        bigint package_count
        bigint version
    }
    shipping_packages {
        text package_id PK
        text site_id
        text source_order_id
        text work_unit_ref
        text load_id
        text staging_location
        double weight_g
        text state
        boolean missed_flagged
        bigint version
    }
    bills_of_lading {
        text bol_number PK
        text load_id UK
        text site_id
        text carrier
        text carrier_scac
        text trailer_id
        text seal_number
        bigint package_count
        double total_weight_g
        timestamptz issued_at
        timestamptz voided_at
        text state
        bigint version
    }
    cpt_schedules {
        text site_id PK
        text timezone
        jsonb cutoffs
        bigint version
    }
    zones {
        text zone_id PK
        text site_code
        text zone_code
    }
    locations {
        text location_code PK
        text zone_id
        text role
    }
    dock_appointments {
        text appointment_id PK
        text site_id
        text direction
        text door_code
        text cpt_id
        text carrier
        text carrier_scac
        bigint version
    }
    door_work_completions {
        text trailer_id PK
        timestamptz completed_at PK
        text site_id
        text door_code
        text appointment_id
    }
    load_board {
        text load_id PK
        text site_id
        text cpt_id
        text operating_date
        bigint staged_count
        bigint loaded_count
        bigint packages_missed
        timestamptz closed_at
        boolean closed_on_time
        timestamptz confirmed_at
        timestamptz cancelled_at
    }
    outbox_events {
        bigserial id PK
        text event_id
        text topic
        text event_type
        text subject
        bytea value
        jsonb headers
        timestamptz published_at
        integer attempts
        text last_error
    }
    processed_events {
        text consumer PK
        text event_id PK
        timestamptz processed_at
    }
    idempotency_keys {
        text key PK
        text fingerprint
        integer status
        bytea body
        timestamptz created_at
    }
```

Source: `migrations/0001_outbound_shipping_schema.up.sql`, `internal/adapters/outbound/postgres/*.go`.

Some columns are omitted for readability (`created_at`/`updated_at`, `dataschema`, `key`, `content_type`, `window_start`, `window_end`, `booked_at`, `void_reason`). The migration has the full list.

## Tables

| Table | Role | Key and constraints | Written by |
| --- | --- | --- | --- |
| `outbound_loads` | `OutboundLoad` aggregate | PK `load_id`; `state` check (5 values); `version >= 1`; **partial unique index `uq_open_load_per_cutoff`** on `(site_id, cpt_id, operating_date, ship_method)` where `state IN ('PLANNED','LOADING')`; indexes for listing, site and state, open loads by site and cpt, open loads by site and door, loads by site and trailer | `ExpectPackage`, door, dock, close, reopen, cancel, confirm |
| `shipping_packages` | `ShippingPackage` aggregate | PK `package_id`; `state` check (5 values); `weight_g >= 0`; indexes by load, by load and state, listing, site and missed | `ExpectPackage`, scans, close, confirm, sweep |
| `bills_of_lading` | `BillOfLading` aggregate | PK `bol_number`; **`UNIQUE (load_id)`** (one document per load); `package_count >= 1`; `state` check | `OnDeparture`, `VoidBillOfLading` |
| `cpt_schedules` | local copy of the cutoff schedule | PK `site_id`; the whole schedule replaced per event | cpt consumer |
| `zones`, `locations` | local copy of staging locations | PK `zone_id`, `location_code`; a location's site is its zone's site; empty `role` means Storage | layout consumer |
| `dock_appointments` | local copy of appointments acted on | PK `appointment_id`; carrier and SCAC read back at ship-confirm | dock consumer |
| `door_work_completions` | copy of `DoorWorkCompleted` | PK `(trailer_id, completed_at)` | yard consumer |
| `load_board` | LoadBoardView projection | PK `load_id`; index `(site_id, operating_date, cpt_id)` for the report | analytics consumer |
| `outbox_events` | transactional outbox | PK `id`; `UNIQUE (event_id, topic)`; partial index of unpublished rows | use cases (insert), relay (drain) |
| `processed_events` | inbox | PK `(consumer, event_id)` | every consumer |
| `idempotency_keys` | stored answers of REST POSTs | PK `key`; `status` between 200 and 499; index on `created_at` | the Idempotency-Key middleware |

Text keys that are compared or ordered in Go use the `"C"` collation so ordering and keyset comparisons follow byte order. The migration has a `.down.sql`; there is no other migration.

There is no analytics database and no retention job: `outbox_events`, `processed_events` and `idempotency_keys` grow without a purge (see the [runbook](/docs/operations/runbook)).
