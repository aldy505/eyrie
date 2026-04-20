-- +goose Up
-- +goose StatementBegin
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS probe_type VARCHAR DEFAULT 'http';
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS success BOOLEAN DEFAULT false;
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS failure_reason TEXT;

UPDATE monitor_historical
SET probe_type = 'http'
WHERE probe_type IS NULL OR probe_type = '';

UPDATE monitor_historical
SET success = status_code BETWEEN 200 AND 399
WHERE probe_type = 'http';

CREATE TABLE IF NOT EXISTS monitor_historical_region_daily_aggregate (
    monitor_id VARCHAR(255) NOT NULL,
    region VARCHAR(255) NOT NULL,
    date DATE NOT NULL,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    min_latency_ms INTEGER NOT NULL DEFAULT 0,
    max_latency_ms INTEGER NOT NULL DEFAULT 0,
    success_rate SMALLINT NOT NULL DEFAULT 100,
    PRIMARY KEY (monitor_id, region, date)
);

CREATE TABLE IF NOT EXISTS monitor_incident_state (
    monitor_id VARCHAR(255) NOT NULL PRIMARY KEY,
    status VARCHAR(32) NOT NULL DEFAULT 'healthy',
    scope VARCHAR(32) NOT NULL DEFAULT 'healthy',
    affected_regions TEXT NOT NULL DEFAULT '[]',
    reason TEXT NOT NULL DEFAULT '',
    last_transition_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS monitor_incident_state;
DROP TABLE IF EXISTS monitor_historical_region_daily_aggregate;
-- DuckDB does not reliably support dropping columns across all versions in use here.
-- Leave monitor_historical column additions in place on down migration.
-- +goose StatementEnd
