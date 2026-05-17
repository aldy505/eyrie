package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/guregu/null/v5"
	"golang.org/x/sync/semaphore"
)

func TestResolveDuckDBReadConcurrencyLimit(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		gomaxprocs int
		want       int64
	}{
		{
			name:       "uses configured limit when set",
			configured: 7,
			gomaxprocs: 8,
			want:       7,
		},
		{
			name:       "derives half of gomaxprocs when unset",
			configured: 0,
			gomaxprocs: 8,
			want:       4,
		},
		{
			name:       "keeps minimum of one permit",
			configured: 0,
			gomaxprocs: 1,
			want:       1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDuckDBReadConcurrencyLimit(tt.configured, tt.gomaxprocs); got != tt.want {
				t.Fatalf("resolveDuckDBReadConcurrencyLimit(%d, %d) = %d, want %d", tt.configured, tt.gomaxprocs, got, tt.want)
			}
		})
	}
}

func TestServerPprofEndpointIsDisabledByDefault(t *testing.T) {
	t.Setenv("ENABLE_PPROF_ENDPOINT", "")

	server, err := NewServer(ServerOptions{
		Database:      db,
		ServerConfig:  ServerConfig{},
		MonitorConfig: MonitorConfig{},
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(func() {
		if err := server.CloseCaches(); err != nil {
			t.Fatalf("failed to close caches: %v", err)
		}
	})

	ts := httptest.NewServer(server.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/debug/pprof/cmdline")
	if err != nil {
		t.Fatalf("failed to request pprof endpoint: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected spa fallback content type, got %q", got)
	}
}

func TestServerPprofEndpointIsEnabledWithEnvVar(t *testing.T) {
	t.Setenv("ENABLE_PPROF_ENDPOINT", "1")

	server, err := NewServer(ServerOptions{
		Database:      db,
		ServerConfig:  ServerConfig{},
		MonitorConfig: MonitorConfig{},
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(func() {
		if err := server.CloseCaches(); err != nil {
			t.Fatalf("failed to close caches: %v", err)
		}
	})

	ts := httptest.NewServer(server.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/debug/pprof/cmdline")
	if err != nil {
		t.Fatalf("failed to request pprof endpoint: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("expected pprof content type, got %q", got)
	}
}

func TestServerRootBrowserUserAgentServesHTML(t *testing.T) {
	server := newRootStatusTestServer(t, rootStatusTestSeedOptions{})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	recorder := httptest.NewRecorder()

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", got)
	}
}

func TestServerRootCLIUserAgentServesPrettyJSON(t *testing.T) {
	server := newRootStatusTestServer(t, rootStatusTestSeedOptions{
		MonitorID:     "root-json-monitor",
		MonitorName:   "CLI JSON Monitor",
		MonitorStatus: MonitorStatusDegraded,
		MonitorScope:  MonitorScopeLocal,
		Reason:        "Latency spike detected",
		SeedEvent:     true,
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "curl/8.7.1")
	recorder := httptest.NewRecorder()

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content type, got %q", got)
	}
	if !strings.Contains(recorder.Body.String(), "\n  \"format\": \"json\"") {
		t.Fatalf("expected pretty-printed JSON body, got %q", recorder.Body.String())
	}

	var payload RootStatusResponse
	if err := json.NewDecoder(strings.NewReader(recorder.Body.String())).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Format != "json" {
		t.Fatalf("expected format=json, got %q", payload.Format)
	}
	if payload.Summary.Services != 1 {
		t.Fatalf("expected 1 service in summary, got %d", payload.Summary.Services)
	}
	if len(payload.RecentEvents) != 1 {
		t.Fatalf("expected 1 recent event, got %d", len(payload.RecentEvents))
	}
	if payload.Services[0].Status != MonitorStatusDegraded {
		t.Fatalf("expected degraded service status, got %q", payload.Services[0].Status)
	}
}

func TestServerRootMissingUserAgentDefaultsToCLIJSON(t *testing.T) {
	server := newRootStatusTestServer(t, rootStatusTestSeedOptions{})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()

	server.Handler.ServeHTTP(recorder, request)

	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content type, got %q", got)
	}
}

func TestServerRootTextFormatCanBeRequestedByCLIClients(t *testing.T) {
	server := newRootStatusTestServer(t, rootStatusTestSeedOptions{
		MonitorID:     "root-text-monitor",
		MonitorName:   "CLI Text Monitor",
		MonitorStatus: MonitorStatusDown,
		MonitorScope:  MonitorScopeGlobal,
		Reason:        "Connection timeout",
	})

	request := httptest.NewRequest(http.MethodGet, "/?format=text", nil)
	request.Header.Set("User-Agent", "wget/1.21")
	recorder := httptest.NewRecorder()

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("expected text content type, got %q", got)
	}
	if !strings.Contains(recorder.Body.String(), "Summary") {
		t.Fatalf("expected summary section in text response, got %q", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Connection timeout") {
		t.Fatalf("expected incident reason in text response, got %q", recorder.Body.String())
	}
}

func TestServerRootNonGETMethodsStillUseSPAFallback(t *testing.T) {
	server := newRootStatusTestServer(t, rootStatusTestSeedOptions{})

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("User-Agent", "curl/8.7.1")
	recorder := httptest.NewRecorder()

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", got)
	}
}

func TestServerNonRootPathsStillUseSPAFallbackForCLIClients(t *testing.T) {
	server := newRootStatusTestServer(t, rootStatusTestSeedOptions{})

	request := httptest.NewRequest(http.MethodGet, "/status/overview", nil)
	request.Header.Set("User-Agent", "curl/8.7.1")
	recorder := httptest.NewRecorder()

	server.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %q", got)
	}
}

func TestTruncateRootTextPreservesUTF8(t *testing.T) {
	value := "東京サービス監視"
	truncated := truncateRootText(value, 6)

	if truncated != "東京サ..." {
		t.Fatalf("expected rune-safe truncation, got %q", truncated)
	}
	if !utf8.ValidString(truncated) {
		t.Fatalf("expected valid UTF-8 output, got %q", truncated)
	}
}

type rootStatusTestSeedOptions struct {
	MonitorID     string
	MonitorName   string
	MonitorStatus string
	MonitorScope  string
	Reason        string
	SeedEvent     bool
}

func newRootStatusTestServer(t *testing.T, options rootStatusTestSeedOptions) *Server {
	t.Helper()

	monitorID := options.MonitorID
	if monitorID == "" {
		monitorID = "root-status-monitor"
	}

	monitorName := options.MonitorName
	if monitorName == "" {
		monitorName = "Root Status Monitor"
	}

	now := time.Now().UTC().Truncate(time.Second)
	cleanupRootStatusTestData(t, monitorID)

	monitorConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:                  monitorID,
				Name:                monitorName,
				Description:         null.StringFrom("root endpoint test monitor"),
				ExpectedStatusCodes: []int{200},
				HTTP: &MonitorHTTPConfig{
					URL: "https://example.com",
				},
			},
		},
	}

	if options.MonitorStatus != "" {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()

		ingesterWorker := &IngesterWorker{
			db:            db,
			monitorConfig: monitorConfig,
			shutdown:      make(chan struct{}),
		}

		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			Success:    true,
			LatencyMs:  123,
			StatusCode: 200,
			Timestamp:  now,
			Timings:    CheckerTraceTimings{},
		}
		if err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1"); err != nil {
			t.Fatalf("failed to seed monitor historical: %v", err)
		}

		scope := options.MonitorScope
		if scope == "" {
			scope = MonitorScopeHealthy
		}
		if _, err := conn.ExecContext(t.Context(), `
			INSERT INTO monitor_incident_state (
				monitor_id, status, scope, affected_regions, reason, last_transition_at, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, monitorID, options.MonitorStatus, scope, `["us-east-1"]`, options.Reason, now, now); err != nil {
			t.Fatalf("failed to seed monitor_incident_state: %v", err)
		}

		if options.SeedEvent {
			if _, err := conn.ExecContext(t.Context(), `
				INSERT INTO monitor_incident_events (
					id, incident_id, monitor_id, event_type, source, lifecycle_state, impact, title, body, status, scope, affected_regions, created_at
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, monitorID+"-event", monitorID+"-incident", monitorID, IncidentEventTypeUpdated, IncidentSourceProgrammatic, IncidentLifecycleMonitoring, IncidentImpactPartialOutage, "Latency spike", options.Reason, options.MonitorStatus, scope, `["us-east-1"]`, now); err != nil {
				t.Fatalf("failed to seed monitor_incident_events: %v", err)
			}
		}
	}

	serverConfig := ServerConfig{}
	serverConfig.Metadata.Title = "Root Status Test"

	server, err := NewServer(ServerOptions{
		Database:      db,
		ServerConfig:  serverConfig,
		MonitorConfig: monitorConfig,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Cleanup(func() {
		if err := server.CloseCaches(); err != nil {
			t.Fatalf("failed to close caches: %v", err)
		}
		cleanupRootStatusTestData(t, monitorID)
	})

	return server
}

func cleanupRootStatusTestData(t *testing.T, monitorID string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	statements := []string{
		`DELETE FROM monitor_incident_events WHERE monitor_id = ?`,
		`DELETE FROM monitor_incidents WHERE monitor_id = ?`,
		`DELETE FROM monitor_incident_state WHERE monitor_id = ?`,
		`DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`,
		`DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`,
		`DELETE FROM monitor_historical WHERE monitor_id = ?`,
	}

	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement, monitorID); err != nil {
			t.Fatalf("failed to clean up root status test data: %v", err)
		}
	}
}

func TestServerWithDuckDBReadPermitWaitsForPermit(t *testing.T) {
	server := &Server{
		duckDBReadLimiter: semaphore.NewWeighted(1),
	}

	if err := server.duckDBReadLimiter.Acquire(t.Context(), 1); err != nil {
		t.Fatalf("failed to acquire initial permit: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/uptime-data", nil)
	handlerStarted := make(chan struct{})
	handlerFinished := make(chan struct{})

	go func() {
		server.withDuckDBReadPermit(func(w http.ResponseWriter, r *http.Request) {
			close(handlerStarted)
			w.WriteHeader(http.StatusNoContent)
		})(recorder, request)
		close(handlerFinished)
	}()

	select {
	case <-handlerStarted:
		t.Fatal("handler should wait for permit before running")
	case <-time.After(100 * time.Millisecond):
	}

	server.duckDBReadLimiter.Release(1)

	select {
	case <-handlerFinished:
	case <-time.After(time.Second):
		t.Fatal("handler did not complete after permit release")
	}

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
}

func TestServer_FetchFromAggregateMonitorHistorical(t *testing.T) {
	monitorID := "test-aggregate-server-monitor"
	testDate1 := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	testDate2 := time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_daily_aggregate table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_region_daily_aggregate table: %v", err)
		}
	})

	monitorConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:                  monitorID,
				Name:                "Test Monitor",
				Description:         null.StringFrom("Test description"),
				ExpectedStatusCodes: []int{200},
			},
		},
	}

	// First, insert some raw data and create aggregates
	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: monitorConfig,
		shutdown:      make(chan struct{}),
	}

	// Insert test data for date 1
	for i := 0; i < 10; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64((i + 1) * 100), // 100, 200, ..., 1000
			StatusCode: 200,
			Timestamp:  testDate1.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical: %v", err)
		}
	}

	// Insert test data for date 2 with some failures
	for i := 0; i < 10; i++ {
		statusCode := 200
		if i >= 8 { // Last 2 are failures
			statusCode = 500
		}
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64((i + 1) * 50), // 50, 100, ..., 500
			StatusCode: statusCode,
			Timestamp:  testDate2.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical: %v", err)
		}
	}

	// Run aggregation for both dates
	err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, testDate1)
	if err != nil {
		t.Fatalf("failed to aggregate daily monitor historical for date 1: %v", err)
	}

	err = ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, testDate2)
	if err != nil {
		t.Fatalf("failed to aggregate daily monitor historical for date 2: %v", err)
	}

	// Now test the server's fetch method
	server := &Server{
		db:            db,
		serverConfig:  ServerConfig{},
		monitorConfig: monitorConfig,
	}

	result, err := server.fetchFromAggregateMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to fetch from aggregate monitor historical: %v", err)
	}

	// Verify the results
	if result.MonitorAge != 2 {
		t.Errorf("expected monitor age = 2 (2 days of data), got %d", result.MonitorAge)
	}

	// Expected average latency:
	// Day 1: (100+200+300+400+500+600+700+800+900+1000)/10 = 550
	// Day 2: (50+100+150+200+250+300+350+400+450+500)/10 = 275
	// Average: (550 + 275) / 2 = 412.5 -> 412
	expectedAvgLatency := int64(412)
	if result.LatencyMs < expectedAvgLatency-10 || result.LatencyMs > expectedAvgLatency+10 {
		t.Errorf("expected average latency around %d, got %d", expectedAvgLatency, result.LatencyMs)
	}

	// Verify DailyDowntimes exists
	if len(result.DailyDowntimes) != 2 {
		t.Errorf("expected 2 daily downtime entries, got %d", len(result.DailyDowntimes))
	}

	// For day 1 (all success), downtime should be 0
	// For day 2 (80% success), downtime should be approximately 20% of 1440 minutes = 288 minutes
	// We need to find which index corresponds to which date
	// The dates in the test are Jan 15 and Jan 16, 2025
	// We need to calculate how many days ago they are from "now"
	// Since we can't control "now" in the test, we'll just check that we have reasonable downtime values
	foundZeroDowntime := false
	foundNonZeroDowntime := false
	for _, dt := range result.DailyDowntimes {
		if dt.DurationMinutes == 0 {
			foundZeroDowntime = true
		}
		if dt.DurationMinutes > 0 {
			foundNonZeroDowntime = true
		}
	}

	if !foundZeroDowntime {
		t.Error("expected to find at least one day with zero downtime (day 1 with 100% success)")
	}
	if !foundNonZeroDowntime {
		t.Error("expected to find at least one day with non-zero downtime (day 2 with 80% success)")
	}
}

func TestServer_FetchFromAggregateMonitorHistoricalUsesExpectedStatusCodes(t *testing.T) {
	monitorID := "test-aggregate-server-http-semantics"
	testDate := time.Date(2025, 1, 19, 0, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_daily_aggregate table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_region_daily_aggregate table: %v", err)
		}
	})

	monitorConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:                  monitorID,
				Name:                "HTTP Semantics Monitor",
				Description:         null.StringFrom("HTTP semantics test"),
				ExpectedStatusCodes: []int{418},
			},
		},
	}

	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: monitorConfig,
		shutdown:      make(chan struct{}),
	}

	for i := 0; i < 6; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			Success:    false,
			LatencyMs:  120,
			StatusCode: 418,
			Timestamp:  testDate.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		if err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1"); err != nil {
			t.Fatalf("failed to ingest monitor historical: %v", err)
		}
	}

	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, testDate); err != nil {
		t.Fatalf("failed to aggregate daily monitor historical: %v", err)
	}

	server := &Server{
		db:            db,
		serverConfig:  ServerConfig{},
		monitorConfig: monitorConfig,
	}

	result, err := server.fetchFromAggregateMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to fetch from aggregate monitor historical: %v", err)
	}

	if result.MonitorAge != 1 {
		t.Fatalf("expected monitor age = 1, got %d", result.MonitorAge)
	}

	if len(result.DailyDowntimes) != 1 {
		t.Fatalf("expected 1 daily downtime entry, got %d", len(result.DailyDowntimes))
	}

	for _, downtime := range result.DailyDowntimes {
		if downtime.DurationMinutes != 0 {
			t.Fatalf("expected zero downtime when custom expected status codes are healthy, got %d", downtime.DurationMinutes)
		}
	}
}

func TestServer_FetchFromAggregateMonitorHistoricalCachesHistoricalRows(t *testing.T) {
	monitorID := "test-aggregate-server-cache"
	today := utcDayStart(time.Now().UTC())
	yesterday := today.AddDate(0, 0, -1)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_daily_aggregate table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_region_daily_aggregate table: %v", err)
		}
	})

	monitorConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:                  monitorID,
				Name:                "Cache Monitor",
				ExpectedStatusCodes: []int{200},
			},
		},
	}

	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: monitorConfig,
		shutdown:      make(chan struct{}),
	}

	for i := 0; i < 2; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64(100 + (i * 100)),
			StatusCode: 200,
			Timestamp:  yesterday.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		if err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1"); err != nil {
			t.Fatalf("failed to ingest yesterday monitor historical: %v", err)
		}
	}

	for i := 0; i < 2; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64(300 + (i * 100)),
			StatusCode: 200,
			Timestamp:  today.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		if err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1"); err != nil {
			t.Fatalf("failed to ingest today monitor historical: %v", err)
		}
	}

	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, yesterday); err != nil {
		t.Fatalf("failed to aggregate yesterday monitor historical: %v", err)
	}
	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, today); err != nil {
		t.Fatalf("failed to aggregate today monitor historical: %v", err)
	}

	server := &Server{
		db:                            db,
		serverConfig:                  ServerConfig{},
		monitorConfig:                 monitorConfig,
		historicalDailyAggregateCache: newTTLCache[[]MonitorHistoricalDailyAggregate](time.Hour),
	}

	first, err := server.fetchFromAggregateMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to fetch from aggregate monitor historical: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection for cache test: %v", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ? AND date = ?`, monitorID, yesterday.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("failed to delete yesterday aggregate row: %v", err)
	}

	second, err := server.fetchFromAggregateMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("expected cached historical aggregates to satisfy second fetch: %v", err)
	}

	if first.MonitorAge != second.MonitorAge {
		t.Fatalf("expected cached monitor age %d, got %d", first.MonitorAge, second.MonitorAge)
	}
	if first.LatencyMs != second.LatencyMs {
		t.Fatalf("expected cached latency %d, got %d", first.LatencyMs, second.LatencyMs)
	}
	if len(first.DailyDowntimes) != len(second.DailyDowntimes) {
		t.Fatalf("expected cached downtime count %d, got %d", len(first.DailyDowntimes), len(second.DailyDowntimes))
	}
}

func TestServer_FetchFromAggregateMonitorHistoricalRefreshesTodayRows(t *testing.T) {
	monitorID := "test-aggregate-server-refresh-today"
	today := utcDayStart(time.Now().UTC())
	yesterday := today.AddDate(0, 0, -1)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_daily_aggregate table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_region_daily_aggregate table: %v", err)
		}
	})

	monitorConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:                  monitorID,
				Name:                "Today Refresh Monitor",
				ExpectedStatusCodes: []int{200},
			},
		},
	}

	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: monitorConfig,
		shutdown:      make(chan struct{}),
	}

	if err := ingesterWorker.ingestMonitorHistorical(t.Context(), CheckerSubmissionRequest{
		MonitorID:  monitorID,
		LatencyMs:  100,
		StatusCode: 200,
		Timestamp:  yesterday,
		Timings:    CheckerTraceTimings{},
	}, "us-east-1"); err != nil {
		t.Fatalf("failed to ingest yesterday monitor historical: %v", err)
	}

	if err := ingesterWorker.ingestMonitorHistorical(t.Context(), CheckerSubmissionRequest{
		MonitorID:  monitorID,
		LatencyMs:  200,
		StatusCode: 200,
		Timestamp:  today,
		Timings:    CheckerTraceTimings{},
	}, "us-east-1"); err != nil {
		t.Fatalf("failed to ingest today monitor historical: %v", err)
	}

	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, yesterday); err != nil {
		t.Fatalf("failed to aggregate yesterday monitor historical: %v", err)
	}
	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, today); err != nil {
		t.Fatalf("failed to aggregate today monitor historical: %v", err)
	}

	server := &Server{
		db:                            db,
		serverConfig:                  ServerConfig{},
		monitorConfig:                 monitorConfig,
		historicalDailyAggregateCache: newTTLCache[[]MonitorHistoricalDailyAggregate](time.Hour),
	}

	first, err := server.fetchFromAggregateMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to fetch aggregate monitor historical: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection for cache refresh test: %v", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `
		UPDATE monitor_historical_daily_aggregate
		SET avg_latency_ms = ?, success_rate = ?
		WHERE monitor_id = ? AND date = ?
	`, 400, 0, monitorID, today.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("failed to update today's aggregate row: %v", err)
	}

	second, err := server.fetchFromAggregateMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to refresh today's aggregate data: %v", err)
	}

	if first.DailyDowntimes[0].DurationMinutes == second.DailyDowntimes[0].DurationMinutes {
		t.Fatalf("expected today's downtime to refresh, but it stayed at %d", first.DailyDowntimes[0].DurationMinutes)
	}
}

func TestServer_UptimeDataByRegionHandlerCachesHistoricalAggregates(t *testing.T) {
	monitorID := "test-region-aggregate-cache"
	today := utcDayStart(time.Now().UTC())
	yesterday := today.AddDate(0, 0, -1)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_daily_aggregate table: %v", err)
		}
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical_region_daily_aggregate table: %v", err)
		}
	})

	monitorConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:                  monitorID,
				Name:                "Region Cache Monitor",
				ExpectedStatusCodes: []int{200},
			},
		},
	}

	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: monitorConfig,
		shutdown:      make(chan struct{}),
	}

	for _, item := range []struct {
		date    time.Time
		latency int64
		status  int
	}{
		{date: yesterday, latency: 100, status: 200},
		{date: today, latency: 200, status: 200},
	} {
		if err := ingesterWorker.ingestMonitorHistorical(t.Context(), CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  item.latency,
			StatusCode: item.status,
			Timestamp:  item.date,
			Timings:    CheckerTraceTimings{},
		}, "us-east-1"); err != nil {
			t.Fatalf("failed to ingest monitor historical: %v", err)
		}
	}

	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, yesterday); err != nil {
		t.Fatalf("failed to aggregate yesterday monitor historical: %v", err)
	}
	if err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, today); err != nil {
		t.Fatalf("failed to aggregate today monitor historical: %v", err)
	}

	server := &Server{
		db:                                  db,
		serverConfig:                        ServerConfig{},
		monitorConfig:                       monitorConfig,
		historicalRegionDailyAggregateCache: newTTLCache[[]MonitorHistoricalRegionDailyAggregate](time.Hour),
	}

	getResponse := func() UptimeDataByRegionResponse {
		request := httptest.NewRequest(http.MethodGet, "/uptime-data-by-region?monitorId="+monitorID, nil)
		recorder := httptest.NewRecorder()
		server.UptimeDataByRegionHandler(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", recorder.Code, recorder.Body.String())
		}

		var response UptimeDataByRegionResponse
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("failed to decode region response: %v", err)
		}

		return response
	}

	first := getResponse()
	if len(first.Monitors) != 1 {
		t.Fatalf("expected one region, got %d", len(first.Monitors))
	}
	firstRegion := first.Monitors[0]

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection for cache test: %v", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, `
		DELETE FROM monitor_historical_region_daily_aggregate
		WHERE monitor_id = ? AND date = ? AND region = ?
	`, monitorID, yesterday.Format("2006-01-02"), "us-east-1")
	if err != nil {
		t.Fatalf("failed to delete yesterday region aggregate row: %v", err)
	}

	second := getResponse()
	if len(second.Monitors) != 1 {
		t.Fatalf("expected one region after cache hit, got %d", len(second.Monitors))
	}
	if second.Monitors[0].ResponseTimeMs != firstRegion.ResponseTimeMs {
		t.Fatalf("expected cached historical region response time %d, got %d", firstRegion.ResponseTimeMs, second.Monitors[0].ResponseTimeMs)
	}

	_, err = conn.ExecContext(ctx, `
		UPDATE monitor_historical_region_daily_aggregate
		SET avg_latency_ms = ?, success_rate = ?
		WHERE monitor_id = ? AND date = ? AND region = ?
	`, 400, 0, monitorID, today.Format("2006-01-02"), "us-east-1")
	if err != nil {
		t.Fatalf("failed to update today's region aggregate row: %v", err)
	}

	third := getResponse()
	if len(third.Monitors) != 1 {
		t.Fatalf("expected one region after refresh, got %d", len(third.Monitors))
	}
	if third.Monitors[0].Downtimes[0].DurationMinutes == second.Monitors[0].Downtimes[0].DurationMinutes {
		t.Fatalf("expected today's region downtime to refresh, but it stayed at %d", second.Monitors[0].Downtimes[0].DurationMinutes)
	}
}

func TestServer_FetchFromRawMonitorHistoricalFallback(t *testing.T) {
	monitorID := "test-fallback-monitor"
	testDate := time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
	})

	// Insert raw data WITHOUT creating aggregates
	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: MonitorConfig{},
		shutdown:      make(chan struct{}),
	}

	for i := 0; i < 5; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64(100),
			StatusCode: 200,
			Timestamp:  testDate.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical: %v", err)
		}
	}

	// Test the server's fetch method - it should fall back to raw data
	server := &Server{
		db:           db,
		serverConfig: ServerConfig{},
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{
					ID:                  monitorID,
					Name:                "Test Fallback Monitor",
					Description:         null.StringFrom("Test fallback description"),
					ExpectedStatusCodes: []int{200},
				},
			},
		},
	}

	result, err := server.fetchFromRawMonitorHistorical(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to fetch from raw monitor historical (fallback): %v", err)
	}

	// Verify the results
	if result.MonitorAge != 1 {
		t.Errorf("expected monitor age = 1 (1 day of data), got %d", result.MonitorAge)
	}

	if result.LatencyMs != 100 {
		t.Errorf("expected average latency = 100, got %d", result.LatencyMs)
	}
}

func TestServer_UptimeDataByRegionHandler(t *testing.T) {
	monitorID := "test-region-monitor"
	testDate := time.Date(2025, 1, 20, 0, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical WHERE monitor_id = ?`, monitorID)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
	})

	// Insert test data for multiple regions
	ingesterWorker := &IngesterWorker{
		db:            db,
		subscriber:    nil,
		monitorConfig: MonitorConfig{},
		shutdown:      make(chan struct{}),
	}

	// Insert data for us-east-1
	for i := 0; i < 5; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64(100),
			StatusCode: 200,
			Timestamp:  testDate.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		err := ingesterWorker.ingestMonitorHistorical(context.Background(), submission, "us-east-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical for us-east-1: %v", err)
		}
	}

	// Insert data for us-west-2
	for i := 0; i < 5; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64(150),
			StatusCode: 200,
			Timestamp:  testDate.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		err := ingesterWorker.ingestMonitorHistorical(context.Background(), submission, "us-west-2")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical for us-west-2: %v", err)
		}
	}

	// Insert data for eu-west-1 with some failures
	for i := 0; i < 5; i++ {
		statusCode := 200
		if i >= 3 {
			statusCode = 500
		}
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  int64(200),
			StatusCode: statusCode,
			Timestamp:  testDate.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}
		err := ingesterWorker.ingestMonitorHistorical(context.Background(), submission, "eu-west-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical for eu-west-1: %v", err)
		}
	}

	// Create a test server
	server := &Server{
		db:           db,
		serverConfig: ServerConfig{},
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{
					ID:                  monitorID,
					Name:                "Test Region Monitor",
					Description:         null.StringFrom("Test description for region endpoint"),
					ExpectedStatusCodes: []int{200},
				},
			},
		},
	}

	// Test the handler by simulating an HTTP request
	req := httptest.NewRequest(http.MethodGet, "/uptime-data-by-region?monitorId="+monitorID, nil)
	w := httptest.NewRecorder()

	server.UptimeDataByRegionHandler(w, req)

	// Check response status
	if w.Code != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, w.Code)
		t.Logf("Response body: %s", w.Body.String())
	}

	// Parse response
	var response UptimeDataByRegionResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify metadata
	if response.Metadata.Name != "Test Region Monitor" {
		t.Errorf("expected name 'Test Region Monitor', got '%s'", response.Metadata.Name)
	}

	// Verify we have data for 3 regions
	if len(response.Monitors) != 3 {
		t.Errorf("expected 3 regions, got %d", len(response.Monitors))
	}

	// Find each region and verify data
	regionMap := make(map[string]UptimeDataByRegionMonitor)
	for _, m := range response.Monitors {
		regionMap[m.Region] = m
	}

	// Verify us-east-1
	if usEast1, ok := regionMap["us-east-1"]; ok {
		if usEast1.ResponseTimeMs != 100 {
			t.Errorf("expected us-east-1 response time 100ms, got %d", usEast1.ResponseTimeMs)
		}
		if usEast1.Age != 1 {
			t.Errorf("expected us-east-1 age 1 day, got %d", usEast1.Age)
		}
	} else {
		t.Error("expected us-east-1 in response")
	}

	// Verify us-west-2
	if usWest2, ok := regionMap["us-west-2"]; ok {
		if usWest2.ResponseTimeMs != 150 {
			t.Errorf("expected us-west-2 response time 150ms, got %d", usWest2.ResponseTimeMs)
		}
	} else {
		t.Error("expected us-west-2 in response")
	}

	// Verify eu-west-1 (should have downtime)
	if euWest1, ok := regionMap["eu-west-1"]; ok {
		if euWest1.ResponseTimeMs != 200 {
			t.Errorf("expected eu-west-1 response time 200ms, got %d", euWest1.ResponseTimeMs)
		}
		// Should have some downtime since 2 out of 5 checks failed
		if len(euWest1.Downtimes) == 0 {
			t.Error("expected eu-west-1 to have downtime data")
		}
	} else {
		t.Error("expected eu-west-1 in response")
	}
}

func TestServer_UptimeDataByRegionHandler_MissingMonitorId(t *testing.T) {
	server := &Server{
		db:            db,
		serverConfig:  ServerConfig{},
		monitorConfig: MonitorConfig{},
	}

	req := httptest.NewRequest(http.MethodGet, "/uptime-data-by-region", nil)
	w := httptest.NewRecorder()

	server.UptimeDataByRegionHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response CommonErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if response.Error != "monitorId query parameter is required" {
		t.Errorf("expected error 'monitorId query parameter is required', got '%s'", response.Error)
	}
}

func TestServer_UptimeDataByRegionHandler_NotFound(t *testing.T) {
	server := &Server{
		db:           db,
		serverConfig: ServerConfig{},
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{
					ID:                  "existing-monitor",
					Name:                "Existing Monitor",
					ExpectedStatusCodes: []int{200},
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/uptime-data-by-region?monitorId=non-existent", nil)
	w := httptest.NewRecorder()

	server.UptimeDataByRegionHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var response CommonErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if response.Error != "monitor not found" {
		t.Errorf("expected error 'monitor not found', got '%s'", response.Error)
	}
}

func TestServer_CheckerRegistrationFiltersMonitorsByCheckerName(t *testing.T) {
	server := &Server{
		serverConfig: ServerConfig{
			RegisteredCheckers: []RegisteredChecker{
				{Name: "public-east", Region: "us-east-1", ApiKey: "east-key"},
			},
		},
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{ID: "global-http", Type: MonitorTypeHTTP, HTTP: &MonitorHTTPConfig{URL: "https://example.com"}},
				{ID: "east-only", Type: MonitorTypeTCP, CheckerNames: []string{"public-east"}, TCP: MonitorTCPConfig{Address: "example.com:443"}},
				{ID: "west-only", Type: MonitorTypeTCP, CheckerNames: []string{"public-west"}, TCP: MonitorTCPConfig{Address: "example.net:443"}},
			},
			Groups: []Group{
				{ID: "group-1", Name: "Important", MonitorIDs: []string{"global-http", "east-only", "west-only"}},
			},
		},
	}

	requestBody := strings.NewReader(`{"name":"public-east","region":"us-east-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/checker/register", requestBody)
	req.Header.Set("X-API-Key", "east-key")
	w := httptest.NewRecorder()

	server.CheckerRegistration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status code %d, got %d", http.StatusOK, w.Code)
	}

	var response MonitorConfig
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(response.Monitors) != 2 {
		t.Fatalf("expected 2 monitors, got %d", len(response.Monitors))
	}
	if response.Monitors[0].ID != "global-http" || response.Monitors[1].ID != "east-only" {
		t.Fatalf("unexpected monitors returned: %#v", response.Monitors)
	}
	if len(response.Groups) != 1 || len(response.Groups[0].MonitorIDs) != 2 {
		t.Fatalf("expected filtered group with 2 monitor IDs, got %#v", response.Groups)
	}
}

func TestServer_CheckerRegistrationAllowsLegacyRegionOnlyRequest(t *testing.T) {
	server := &Server{
		serverConfig: ServerConfig{
			RegisteredCheckers: []RegisteredChecker{
				{Name: "public-east", Region: "us-east-1", ApiKey: "east-key"},
			},
		},
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{ID: "east-only", Type: MonitorTypeTCP, CheckerNames: []string{"public-east"}, TCP: MonitorTCPConfig{Address: "example.com:443"}},
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/checker/register", strings.NewReader(`{"region":"us-east-1"}`))
	req.Header.Set("X-API-Key", "east-key")
	w := httptest.NewRecorder()

	server.CheckerRegistration(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status code %d, got %d", http.StatusOK, w.Code)
	}

	var response MonitorConfig
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(response.Monitors) != 1 || response.Monitors[0].ID != "east-only" {
		t.Fatalf("unexpected legacy registration response: %#v", response.Monitors)
	}
}
