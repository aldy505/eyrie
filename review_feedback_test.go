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
