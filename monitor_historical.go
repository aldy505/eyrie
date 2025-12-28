package main

import (
	"time"

	"github.com/guregu/null/v5"
)

type MonitorHistorical struct {
	MonitorID                 string      `db:"monitor_id"`
	Region                    string      `db:"region"`
	StatusCode                int         `db:"status_code"`
	LatencyMs                 int         `db:"latency_ms"`
	ResponseBody              null.String `db:"response_body"`
	TlsVersion                null.String `db:"tls_version"`
	TlsCipher                 null.String `db:"tls_cipher"`
	TlsExpiry                 null.Time   `db:"tls_expiry"`
	TimingConnAcquiredMs      null.Int    `db:"timing_conn_acquired_ms"`
	TimingFirstResponseByteMs null.Int    `db:"timing_first_response_byte_ms"`
	TimingDNSLookupStartMs    null.Int    `db:"timing_dns_lookup_start_ms"`
	TimingDNSLookupDoneMs     null.Int    `db:"timing_dns_lookup_done_ms"`
	TimingTLSHandshakeStartMs null.Int    `db:"timing_tls_handshake_start_ms"`
	TimingTLSHandshakeDoneMs  null.Int    `db:"timing_tls_handshake_done_ms"`
	CreatedAt                 time.Time   `db:"created_at"`
}

type MonitorHistoricalDailyAggregate struct {
	MonitorID    string    `db:"monitor_id"`
	Date         time.Time `db:"date"`
	AvgLatencyMs int       `db:"avg_latency_ms"`
	MinLatencyMs int       `db:"min_latency_ms"`
	MaxLatencyMs int       `db:"max_latency_ms"`
	SuccessRate  int       `db:"success_rate"`
}
