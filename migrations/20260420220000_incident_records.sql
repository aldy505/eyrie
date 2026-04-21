-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS monitor_incidents (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    monitor_id VARCHAR(255) NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'programmatic',
    lifecycle_state VARCHAR(32) NOT NULL DEFAULT 'investigating',
    auto_impact VARCHAR(32) NOT NULL DEFAULT 'unknown',
    impact VARCHAR(32) NOT NULL DEFAULT 'unknown',
    auto_title TEXT NOT NULL DEFAULT '',
    auto_body TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'healthy',
    scope VARCHAR(32) NOT NULL DEFAULT 'healthy',
    affected_regions TEXT NOT NULL DEFAULT '[]',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_monitor_incidents_monitor_id
    ON monitor_incidents (monitor_id);

CREATE INDEX IF NOT EXISTS idx_monitor_incidents_monitor_id_resolved_at
    ON monitor_incidents (monitor_id, resolved_at);

CREATE TABLE IF NOT EXISTS monitor_incident_events (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    incident_id VARCHAR(255) NOT NULL,
    monitor_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    source VARCHAR(32) NOT NULL DEFAULT 'programmatic',
    lifecycle_state VARCHAR(32) NOT NULL DEFAULT 'investigating',
    impact VARCHAR(32) NOT NULL DEFAULT 'unknown',
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'healthy',
    scope VARCHAR(32) NOT NULL DEFAULT 'healthy',
    affected_regions TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_monitor_incident_events_incident_id
    ON monitor_incident_events (incident_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS monitor_incident_events;
DROP TABLE IF EXISTS monitor_incidents;
-- +goose StatementEnd
