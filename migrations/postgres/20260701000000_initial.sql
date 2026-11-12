-- +goose Up
-- +goose StatementBegin
CREATE TABLE monitor_historical (
    monitor_id TEXT NOT NULL,
    region TEXT NOT NULL,
    probe_type TEXT NOT NULL DEFAULT 'http',
    success BOOLEAN NOT NULL DEFAULT FALSE,
    failure_reason TEXT,
    status_code SMALLINT NOT NULL,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    response_body TEXT,
    tls_version TEXT,
    tls_cipher TEXT,
    tls_expiry TIMESTAMPTZ,
    tls_cert_not_before TIMESTAMPTZ,
    tls_cert_issuer TEXT,
    tls_cert_subject TEXT,
    tls_cert_dn TEXT,
    tls_cert_fingerprint TEXT,
    tls_cert_is_expired BOOLEAN NOT NULL DEFAULT FALSE,
    timing_conn_acquired_ms INTEGER,
    timing_first_response_byte_ms INTEGER,
    timing_dns_lookup_start_ms INTEGER,
    timing_dns_lookup_done_ms INTEGER,
    timing_tls_handshake_start_ms INTEGER,
    timing_tls_handshake_done_ms INTEGER,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (monitor_id, region, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX idx_monitor_historical_monitor_created_at
    ON monitor_historical (monitor_id, created_at);

CREATE INDEX idx_monitor_historical_created_at_brin
    ON monitor_historical USING BRIN (created_at);

CREATE TABLE monitor_historical_hourly_aggregate (
    monitor_id TEXT NOT NULL,
    bucket_start TIMESTAMPTZ NOT NULL,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    min_latency_ms INTEGER NOT NULL DEFAULT 0,
    max_latency_ms INTEGER NOT NULL DEFAULT 0,
    success_rate SMALLINT NOT NULL DEFAULT 100,
    check_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, bucket_start)
) PARTITION BY RANGE (bucket_start);

CREATE TABLE monitor_historical_hourly_region_aggregate (
    monitor_id TEXT NOT NULL,
    region TEXT NOT NULL,
    bucket_start TIMESTAMPTZ NOT NULL,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    min_latency_ms INTEGER NOT NULL DEFAULT 0,
    max_latency_ms INTEGER NOT NULL DEFAULT 0,
    success_rate SMALLINT NOT NULL DEFAULT 100,
    check_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, region, bucket_start)
) PARTITION BY RANGE (bucket_start);

CREATE TABLE monitor_historical_daily_aggregate (
    monitor_id TEXT NOT NULL,
    date DATE NOT NULL,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    min_latency_ms INTEGER NOT NULL DEFAULT 0,
    max_latency_ms INTEGER NOT NULL DEFAULT 0,
    success_rate SMALLINT NOT NULL DEFAULT 100,
    check_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, date)
) PARTITION BY RANGE (date);

CREATE TABLE monitor_historical_region_daily_aggregate (
    monitor_id TEXT NOT NULL,
    region TEXT NOT NULL,
    date DATE NOT NULL,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    min_latency_ms INTEGER NOT NULL DEFAULT 0,
    max_latency_ms INTEGER NOT NULL DEFAULT 0,
    success_rate SMALLINT NOT NULL DEFAULT 100,
    check_count BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, region, date)
) PARTITION BY RANGE (date);

CREATE TABLE monitor_incident_state (
    monitor_id TEXT NOT NULL PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'healthy',
    scope TEXT NOT NULL DEFAULT 'healthy',
    affected_regions TEXT NOT NULL DEFAULT '[]',
    reason TEXT NOT NULL DEFAULT '',
    failure_reasons_json TEXT NOT NULL DEFAULT '{}',
    last_transition_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE monitor_incidents (
    id TEXT NOT NULL PRIMARY KEY,
    monitor_id TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'programmatic',
    lifecycle_state TEXT NOT NULL DEFAULT 'investigating',
    auto_impact TEXT NOT NULL DEFAULT 'unknown',
    impact TEXT NOT NULL DEFAULT 'unknown',
    auto_title TEXT NOT NULL DEFAULT '',
    auto_body TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'healthy',
    scope TEXT NOT NULL DEFAULT 'healthy',
    affected_regions TEXT NOT NULL DEFAULT '[]',
    failure_reasons_json TEXT NOT NULL DEFAULT '{}',
    started_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_monitor_incidents_monitor_id
    ON monitor_incidents (monitor_id);

CREATE INDEX idx_monitor_incidents_monitor_id_resolved_at
    ON monitor_incidents (monitor_id, resolved_at);

CREATE INDEX idx_monitor_incidents_started_at
    ON monitor_incidents (started_at);

CREATE TABLE monitor_incident_events (
    id TEXT NOT NULL PRIMARY KEY,
    incident_id TEXT NOT NULL,
    monitor_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'programmatic',
    lifecycle_state TEXT NOT NULL DEFAULT 'investigating',
    impact TEXT NOT NULL DEFAULT 'unknown',
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'healthy',
    scope TEXT NOT NULL DEFAULT 'healthy',
    affected_regions TEXT NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_monitor_incident_events_incident_id
    ON monitor_incident_events (incident_id);

CREATE INDEX idx_monitor_incident_events_created_at
    ON monitor_incident_events (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS monitor_incident_events;
DROP TABLE IF EXISTS monitor_incidents;
DROP TABLE IF EXISTS monitor_incident_state;
DROP TABLE IF EXISTS monitor_historical_region_daily_aggregate;
DROP TABLE IF EXISTS monitor_historical_daily_aggregate;
DROP TABLE IF EXISTS monitor_historical_hourly_region_aggregate;
DROP TABLE IF EXISTS monitor_historical_hourly_aggregate;
DROP TABLE IF EXISTS monitor_historical;
-- +goose StatementEnd
