-- 0001_outbound_shipping_schema.down.sql: recreates the empty schema exactly
-- (every table and index of the up file is dropped).

DROP INDEX IF EXISTS idx_load_board_report;
DROP TABLE IF EXISTS load_board;
DROP INDEX IF EXISTS idx_idempotency_keys_created_at;
DROP TABLE IF EXISTS idempotency_keys;
DROP INDEX IF EXISTS idx_outbox_events_unpublished;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS processed_events;
DROP TABLE IF EXISTS door_work_completions;
DROP TABLE IF EXISTS dock_appointments;
DROP INDEX IF EXISTS idx_locations_zone;
DROP TABLE IF EXISTS locations;
DROP TABLE IF EXISTS zones;
DROP TABLE IF EXISTS cpt_schedules;
DROP INDEX IF EXISTS idx_bols_by_load;
DROP TABLE IF EXISTS bills_of_lading;
DROP INDEX IF EXISTS idx_packages_site_missed;
DROP INDEX IF EXISTS idx_packages_listing;
DROP INDEX IF EXISTS idx_packages_load_state;
DROP INDEX IF EXISTS idx_packages_by_load;
DROP TABLE IF EXISTS shipping_packages;
DROP INDEX IF EXISTS idx_loads_by_site_trailer;
DROP INDEX IF EXISTS idx_loads_open_by_site_door;
DROP INDEX IF EXISTS idx_loads_open_by_site_cpt;
DROP INDEX IF EXISTS idx_loads_site_state;
DROP INDEX IF EXISTS idx_loads_listing;
DROP INDEX IF EXISTS uq_open_load_per_cutoff;
DROP TABLE IF EXISTS outbound_loads;
