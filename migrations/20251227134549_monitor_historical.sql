-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS monitor_historical (
    monitor_id VARCHAR(255) NOT NULL,
    region VARCHAR(255) NOT NULL,
    status_code SMALLINT NOT NULL,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    response_body TEXT,
    tls_version VARCHAR(255),
    tls_cipher VARCHAR(255),
    tls_expiry TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, created_at)
);

CREATE TABLE IF NOT EXISTS monitor_historical_daily_aggregate (
    monitor_id VARCHAR(255) NOT NULL,
    date DATE NOT NULL,
    avg_latency_ms INTEGER NOT NULL DEFAULT 0,
    min_latency_ms INTEGER NOT NULL DEFAULT 0,
    max_latency_ms INTEGER NOT NULL DEFAULT 0,
    success_rate SMALLINT NOT NULL DEFAULT 100,
    PRIMARY KEY (monitor_id, date)
)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS monitor_historical_daily_aggregate;
DROP TABLE IF EXISTS monitor_historical;
-- +goose StatementEnd
