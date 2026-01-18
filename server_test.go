package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/guregu/null/v5"
	"gocloud.dev/pubsub"
	_ "gocloud.dev/pubsub/mempubsub"
)

func TestUptimeDataByRegionHandler(t *testing.T) {
	ctx := context.Background()

	// Setup test database with test data
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	// Insert test monitor data
	now := time.Now()
	testMonitorID := "test-monitor-1"
	_, err = conn.ExecContext(ctx, `
		INSERT INTO monitor_historical (monitor_id, region, status_code, latency_ms, created_at)
		VALUES 
			(?, 'us-east-1', 200, 100, ?),
			(?, 'us-east-1', 200, 120, ?),
			(?, 'eu-west-1', 200, 150, ?),
			(?, 'eu-west-1', 200, 180, ?),
			(?, 'ap-southeast-1', 200, 200, ?)
	`, testMonitorID, now,
		testMonitorID, now.Add(-1*time.Hour),
		testMonitorID, now,
		testMonitorID, now.Add(-1*time.Hour),
		testMonitorID, now)
	if err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	// Setup test server
	processorTopic, err := pubsub.OpenTopic(ctx, "mem://processor")
	if err != nil {
		t.Fatalf("failed to open processor topic: %v", err)
	}
	defer processorTopic.Shutdown(ctx)

	ingesterTopic, err := pubsub.OpenTopic(ctx, "mem://ingester")
	if err != nil {
		t.Fatalf("failed to open ingester topic: %v", err)
	}
	defer ingesterTopic.Shutdown(ctx)

	serverConfig := ServerConfig{}
	serverConfig.Server.Host = "localhost"
	serverConfig.Server.Port = 8080

	server, err := NewServer(ServerOptions{
		Database:     db,
		ServerConfig: serverConfig,
		MonitorConfig: MonitorConfig{
			Monitors: []Monitor{
				{
					ID:                  testMonitorID,
					Name:                "Test Monitor",
					Description:         null.StringFrom("Test Description"),
					ExpectedStatusCodes: []int{200},
				},
			},
		},
		ProcessorProducer: processorTopic,
		IngesterProducer:  ingesterTopic,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	tests := []struct {
		name           string
		queryParams    map[string]string
		expectedStatus int
		validateBody   func(t *testing.T, body []byte)
	}{
		{
			name: "valid request",
			queryParams: map[string]string{
				"monitorId": testMonitorID,
			},
			expectedStatus: http.StatusOK,
			validateBody: func(t *testing.T, body []byte) {
				var response UptimeDataByRegionResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}

				if response.Metadata.Name != "Test Monitor" {
					t.Errorf("expected name 'Test Monitor', got '%s'", response.Metadata.Name)
				}

				if len(response.Monitors) == 0 {
					t.Fatal("expected monitors data, got empty array")
				}

				// Check if we have data for all regions
				regions := make(map[string]bool)
				for _, monitor := range response.Monitors {
					regions[monitor.Region] = true
					if monitor.ResponseTimeMs <= 0 {
						t.Errorf("expected positive response time for region %s, got %d", monitor.Region, monitor.ResponseTimeMs)
					}
				}

				// We should have 3 regions
				if len(regions) != 3 {
					t.Errorf("expected 3 regions, got %d", len(regions))
				}
			},
		},
		{
			name: "missing monitorId",
			queryParams: map[string]string{
				"monitorId": "",
			},
			expectedStatus: http.StatusBadRequest,
			validateBody: func(t *testing.T, body []byte) {
				var response CommonErrorResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}
				if response.Error != "monitorId is required" {
					t.Errorf("expected error 'monitorId is required', got '%s'", response.Error)
				}
			},
		},
		{
			name: "non-existent monitor",
			queryParams: map[string]string{
				"monitorId": "non-existent-monitor",
			},
			expectedStatus: http.StatusBadRequest,
			validateBody: func(t *testing.T, body []byte) {
				var response CommonErrorResponse
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}
				if response.Error != "monitor not found" {
					t.Errorf("expected error 'monitor not found', got '%s'", response.Error)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build URL with query parameters
			url := "/uptime-data-by-region"
			if monitorId, ok := tt.queryParams["monitorId"]; ok && monitorId != "" {
				url += "?monitorId=" + monitorId
			}

			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()

			server.UptimeDataByRegionHandler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.validateBody != nil {
				tt.validateBody(t, w.Body.Bytes())
			}
		})
	}

	// Cleanup test data
	_, err = conn.ExecContext(ctx, "DELETE FROM monitor_historical WHERE monitor_id = ?", testMonitorID)
	if err != nil {
		t.Fatalf("failed to cleanup test data: %v", err)
	}
}
