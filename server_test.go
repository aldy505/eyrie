package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guregu/null/v5"
)

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
	})

	// First, insert some raw data and create aggregates
	ingesterWorker := &IngesterWorker{
		db:         db,
		subscriber: nil,
		shutdown:   make(chan struct{}),
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
		monitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{
					ID:                  monitorID,
					Name:                "Test Monitor",
					Description:         null.StringFrom("Test description"),
					ExpectedStatusCodes: []int{200},
				},
			},
		},
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
		db:         db,
		subscriber: nil,
		shutdown:   make(chan struct{}),
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
		db:            db,
		serverConfig:  ServerConfig{},
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
		db:         db,
		subscriber: nil,
		shutdown:   make(chan struct{}),
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
		db:            db,
		serverConfig:  ServerConfig{},
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
		db:            db,
		serverConfig:  ServerConfig{},
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

