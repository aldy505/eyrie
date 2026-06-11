-- +goose Up
-- +goose StatementBegin
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS tls_cert_not_before TIMESTAMPTZ;
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS tls_cert_issuer VARCHAR(512);
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS tls_cert_subject VARCHAR(512);
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS tls_cert_dn VARCHAR(512);
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS tls_cert_fingerprint VARCHAR(128);
ALTER TABLE monitor_historical ADD COLUMN IF NOT EXISTS tls_cert_is_expired BOOLEAN;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE monitor_historical DROP COLUMN IF EXISTS tls_cert_not_before;
ALTER TABLE monitor_historical DROP COLUMN IF EXISTS tls_cert_issuer;
ALTER TABLE monitor_historical DROP COLUMN IF EXISTS tls_cert_subject;
ALTER TABLE monitor_historical DROP COLUMN IF EXISTS tls_cert_dn;
ALTER TABLE monitor_historical DROP COLUMN IF EXISTS tls_cert_fingerprint;
ALTER TABLE monitor_historical DROP COLUMN IF EXISTS tls_cert_is_expired;
-- +goose StatementEnd