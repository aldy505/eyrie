package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
)

func TestMonitorEffectiveHTTPExplicitFalseOverride(t *testing.T) {
	var monitorConfig MonitorConfig
	err := yaml.Unmarshal([]byte(`
monitors:
  - id: test-monitor
    name: Test Monitor
    skip_tls_verify: true
    http:
      url: https://example.com
      skip_tls_verify: false
`), &monitorConfig)
	if err != nil {
		t.Fatalf("failed to unmarshal monitor config: %v", err)
	}

	if got := monitorConfig.Monitors[0].EffectiveHTTP().SkipTLSVerifyValue(); got {
		t.Fatalf("expected nested http.skip_tls_verify=false to override legacy true")
	}
}

func TestMonitorEffectiveHTTPLegacyFallback(t *testing.T) {
	var monitorConfig MonitorConfig
	err := yaml.Unmarshal([]byte(`
monitors:
  - id: test-monitor
    name: Test Monitor
    skip_tls_verify: true
    http:
      url: https://example.com
`), &monitorConfig)
	if err != nil {
		t.Fatalf("failed to unmarshal monitor config: %v", err)
	}

	if got := monitorConfig.Monitors[0].EffectiveHTTP().SkipTLSVerifyValue(); !got {
		t.Fatalf("expected legacy skip_tls_verify=true to be used when nested value is unset")
	}
}

func TestServerNameForAddress(t *testing.T) {
	tests := map[string]string{
		"example.com:443":   "example.com",
		"[::1]:443":         "::1",
		"[2001:db8::1]:443": "2001:db8::1",
		"db.internal":       "db.internal",
	}

	for address, expected := range tests {
		if got := serverNameForAddress(address); got != expected {
			t.Fatalf("serverNameForAddress(%q) = %q, want %q", address, got, expected)
		}
	}
}

func TestMonitorIncidentsHandlerReturnsServerErrorWhenConfigIsMissing(t *testing.T) {
	monitorID := "test-incidents-handler-missing-config"

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
	})

	ingesterWorker := &IngesterWorker{
		db:       db,
		shutdown: make(chan struct{}),
	}

	if err := ingesterWorker.ingestMonitorHistorical(t.Context(), CheckerSubmissionRequest{
		MonitorID:  monitorID,
		LatencyMs:  123,
		StatusCode: 200,
		Timestamp:  time.Now().UTC(),
		Timings:    CheckerTraceTimings{},
	}, "us-east-1"); err != nil {
		t.Fatalf("failed to seed monitor history: %v", err)
	}

	server := &Server{
		db:            db,
		serverConfig:  ServerConfig{},
		monitorConfig: MonitorConfig{},
	}

	request := httptest.NewRequest(http.MethodGet, "/monitor-incidents", nil)
	recorder := httptest.NewRecorder()

	server.MonitorIncidentsHandler(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, recorder.Code)
	}

	var response CommonErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if response.Error != "failed to load incident state" {
		t.Fatalf("expected incident state error message, got %q", response.Error)
	}
}

func TestMonitorIncidentsHandlerReturnsActiveIncidentMetadata(t *testing.T) {
	monitorID := "test-incidents-handler-active"
	incidentID := "incident-" + monitorID
	now := time.Now().UTC()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_incident_events WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_incident_events table: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_incidents WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_incidents table: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_incident_state WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_incident_state table: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
	})

	ingesterWorker := &IngesterWorker{
		db:       db,
		shutdown: make(chan struct{}),
	}

	if err := ingesterWorker.ingestMonitorHistorical(t.Context(), CheckerSubmissionRequest{
		MonitorID:  monitorID,
		LatencyMs:  123,
		StatusCode: 200,
		Timestamp:  now,
		Timings:    CheckerTraceTimings{},
	}, "us-east-1"); err != nil {
		t.Fatalf("failed to seed monitor history: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO monitor_incidents (
			id, monitor_id, source, lifecycle_state, auto_impact, impact, auto_title, auto_body, title, body, status, scope, affected_regions, started_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, incidentID, monitorID, IncidentSourceProgrammatic, IncidentLifecycleMonitoring, IncidentImpactMajorOutage, IncidentImpactPartialOutage, "auto title", "auto body", "manual title", "manual body", MonitorStatusDown, MonitorScopeGlobal, `["us-east-1"]`, now, now, now); err != nil {
		t.Fatalf("failed to seed monitor_incidents: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO monitor_incident_state (
			monitor_id, status, scope, affected_regions, reason, last_transition_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, monitorID, MonitorStatusDown, MonitorScopeGlobal, `["us-east-1"]`, "state body", now, now); err != nil {
		t.Fatalf("failed to seed monitor_incident_state: %v", err)
	}

	server := &Server{
		db:           db,
		serverConfig: ServerConfig{},
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{
					ID:   monitorID,
					Name: "Test Incident Monitor",
					HTTP: &MonitorHTTPConfig{
						URL: "https://example.com",
					},
				},
			},
		},
	}

	request := httptest.NewRequest(http.MethodGet, "/monitor-incidents", nil)
	recorder := httptest.NewRecorder()

	server.MonitorIncidentsHandler(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var response MonitorIncidentsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(response.Incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(response.Incidents))
	}

	incident := response.Incidents[0]
	if incident.IncidentID != incidentID {
		t.Fatalf("expected incident_id %q, got %q", incidentID, incident.IncidentID)
	}
	if incident.IncidentSource != IncidentSourceProgrammatic {
		t.Fatalf("expected incident_source %q, got %q", IncidentSourceProgrammatic, incident.IncidentSource)
	}
	if incident.IncidentLifecycleState != IncidentLifecycleMonitoring {
		t.Fatalf("expected incident_lifecycle_state %q, got %q", IncidentLifecycleMonitoring, incident.IncidentLifecycleState)
	}
	if incident.IncidentImpact != IncidentImpactPartialOutage {
		t.Fatalf("expected incident_impact %q, got %q", IncidentImpactPartialOutage, incident.IncidentImpact)
	}
	if incident.IncidentTitle != "manual title" {
		t.Fatalf("expected incident_title %q, got %q", "manual title", incident.IncidentTitle)
	}
	if incident.Reason != "manual body" {
		t.Fatalf("expected reason to use incident body, got %q", incident.Reason)
	}
	if got := incident.AffectedRegions; len(got) != 1 || got[0] != "us-east-1" {
		t.Fatalf("expected affected regions %v, got %v", []string{"us-east-1"}, got)
	}

	if incident.LastTransitionAt.IsZero() {
		t.Fatal("expected last_transition_at to be set")
	}
	if incident.UpdatedAt.IsZero() {
		t.Fatal("expected updated_at to be set")
	}
	if incident.Status != MonitorStatusDown {
		t.Fatalf("expected status %q, got %q", MonitorStatusDown, incident.Status)
	}
	if incident.Scope != MonitorScopeGlobal {
		t.Fatalf("expected scope %q, got %q", MonitorScopeGlobal, incident.Scope)
	}
}
