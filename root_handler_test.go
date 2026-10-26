package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRootHandlerSkipsStaleMonitorWithoutConfig(t *testing.T) {
	monitorID := "test-root-handler-stale-monitor"

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

	spa := &spaHandler{
		fileSystem: os.DirFS("frontend/dist"),
		indexFile:  "index.html",
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "curl/8.0")
	recorder := httptest.NewRecorder()

	server.RootHandler(spa).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var response RootStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	for _, service := range response.Services {
		if service.ID == monitorID {
			t.Fatalf("expected stale monitor %q to be skipped, but it was returned", monitorID)
		}
	}
}
