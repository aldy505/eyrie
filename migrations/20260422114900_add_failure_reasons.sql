-- +goose Up
-- +goose StatementBegin
ALTER TABLE monitor_incident_state ADD COLUMN IF NOT EXISTS failure_reasons_json TEXT DEFAULT '{}';
ALTER TABLE monitor_incidents ADD COLUMN IF NOT EXISTS failure_reasons_json TEXT DEFAULT '{}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- DuckDB does not reliably support dropping columns, leaving columns in place on down migration.
-- +goose StatementEnd
