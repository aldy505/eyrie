package main

import (
	"time"

	"github.com/guregu/null/v5"
)

type MonitorHistorical struct {
	MonitorID                 string      `db:"monitor_id"`
	Region                    string      `db:"region"`
	ProbeType                 string      `db:"probe_type"`
	Success                   bool        `db:"success"`
	FailureReason             null.String `db:"failure_reason"`
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
	Region       string    `db:"region"`
	Date         time.Time `db:"date"`
	AvgLatencyMs int       `db:"avg_latency_ms"`
	MinLatencyMs int       `db:"min_latency_ms"`
	MaxLatencyMs int       `db:"max_latency_ms"`
	SuccessRate  int       `db:"success_rate"`
}

type MonitorHistoricalRegionDailyAggregate struct {
	MonitorID    string    `db:"monitor_id"`
	Region       string    `db:"region"`
	Date         time.Time `db:"date"`
	AvgLatencyMs int       `db:"avg_latency_ms"`
	MinLatencyMs int       `db:"min_latency_ms"`
	MaxLatencyMs int       `db:"max_latency_ms"`
	SuccessRate  int       `db:"success_rate"`
}

type MonitorIncidentState struct {
	MonitorID          string    `db:"monitor_id"`
	Status             string    `db:"status"`
	Scope              string    `db:"scope"`
	AffectedRegions    string    `db:"affected_regions"`
	Reason             string    `db:"reason"`
	FailureReasonsJson string    `db:"failure_reasons_json"`
	LastTransitionAt   time.Time `db:"last_transition_at"`
	UpdatedAt          time.Time `db:"updated_at"`
}

type MonitorIncident struct {
	ID              string    `db:"id"`
	MonitorID       string    `db:"monitor_id"`
	Source          string    `db:"source"`
	LifecycleState  string    `db:"lifecycle_state"`
	AutoImpact      string    `db:"auto_impact"`
	Impact          string    `db:"impact"`
	AutoTitle       string    `db:"auto_title"`
	AutoBody        string    `db:"auto_body"`
	Title           string    `db:"title"`
	Body            string    `db:"body"`
	Status          string    `db:"status"`
	Scope           string    `db:"scope"`
	AffectedRegions string    `db:"affected_regions"`
	StartedAt       time.Time `db:"started_at"`
	ResolvedAt      null.Time `db:"resolved_at"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

type MonitorIncidentEvent struct {
	ID              string    `db:"id"`
	IncidentID      string    `db:"incident_id"`
	MonitorID       string    `db:"monitor_id"`
	EventType       string    `db:"event_type"`
	Source          string    `db:"source"`
	LifecycleState  string    `db:"lifecycle_state"`
	Impact          string    `db:"impact"`
	Title           string    `db:"title"`
	Body            string    `db:"body"`
	Status          string    `db:"status"`
	Scope           string    `db:"scope"`
	AffectedRegions string    `db:"affected_regions"`
	CreatedAt       time.Time `db:"created_at"`
}
