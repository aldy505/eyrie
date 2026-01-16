package main

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/guregu/null/v5"
)

func TestIngesterWorker_IngestMonitorHistorical(t *testing.T) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()
		_, err = conn.ExecContext(ctx, `DELETE FROM monitor_historical`)
		if err != nil {
			t.Fatalf("failed to clean up monitor_historical table: %v", err)
		}
	})

	ingesterWorker := &IngesterWorker{
		db:         db,
		subscriber: nil,
		shutdown:   make(chan struct{}),
	}

	for i := 0; i < 1000; i++ {
		latencyMs := rand.Int64N(30_000)

		responseBody := null.String{}
		if rand.Int()%2 == 0 {
			responseBody = null.StringFrom("Test response body content")
		}
		tlsVersion := null.String{}
		tlsCipher := null.String{}
		TlsExpiry := null.Time{}
		if rand.Int()%2 == 0 {
			tlsVersion = null.StringFrom("TLS 1.3")
			tlsCipher = null.StringFrom("TLS_AES_256_GCM_SHA384")
			TlsExpiry = null.TimeFrom(time.Now().Add(time.Hour * 24 * 30))
		}

		submission := CheckerSubmissionRequest{
			MonitorID:  "test-monitor",
			LatencyMs:  latencyMs,
			StatusCode: 200,
			ResponseHeaders: map[string]string{
				"Content-Type":  "application/json",
				"Cache-Control": "no-cache",
				"Server":        "EyrieTestServer",
				"X-Test-Header": "TestValue",
			},
			ResponseBody: responseBody,
			TlsVersion:   tlsVersion,
			TlsCipher:    tlsCipher,
			TlsExpiry:    TlsExpiry,
			Timestamp:    time.Now().Add(time.Millisecond * time.Duration(rand.Uint())),
			Timings: CheckerTraceTimings{
				ConnAcquiredMs:      int64(rand.Uint64N(10_000)),
				FirstResponseByteMs: int64(rand.Uint64N(5_000)),
				DNSLookupStartMs:    int64(rand.Uint64N(5_000)),
				DNSLookupDoneMs:     int64(rand.Uint64N(5_000)),
				TLSHandshakeStartMs: int64(rand.Uint64N(5_000)),
				TLSHandshakeDoneMs:  int64(rand.Uint64N(5_000)),
			},
		}

		err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1")
		if err != nil {
			t.Errorf("failed to ingest monitor historical on iteration %d: %v", i, err)
		}
	}

	time.Sleep(time.Second) // Wait a moment to ensure all inserts are done.

	// Verify the number of records inserted.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection for verification: %v", err)
	}
	defer conn.Close()

	var count int
	err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = ?`, "test-monitor").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query count from monitor_historical: %v", err)
	}
	if count != 1000 {
		t.Errorf("expected 1000 records in monitor_historical, got %d", count)
	}
}

func TestIngesterWorker_AggregateDailyMonitorHistorical(t *testing.T) {
	monitorID := "test-aggregate-monitor"
	testDate := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

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

	ingesterWorker := &IngesterWorker{
		db:         db,
		subscriber: nil,
		shutdown:   make(chan struct{}),
	}

	// Insert test data with known values
	// Insert 10 records: 8 with status 200 (success), 2 with status 500 (failure)
	// Latencies: 100, 200, 300, 400, 500, 600, 700, 800, 900, 1000
	for i := 0; i < 10; i++ {
		latencyMs := int64((i + 1) * 100)
		statusCode := 200
		if i >= 8 {
			statusCode = 500 // Last 2 are failures
		}

		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  latencyMs,
			StatusCode: statusCode,
			Timestamp:  testDate.Add(time.Minute * time.Duration(i)),
			Timings:    CheckerTraceTimings{},
		}

		err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical on iteration %d: %v", i, err)
		}
	}

	// Run the aggregation
	err := ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, testDate)
	if err != nil {
		t.Fatalf("failed to aggregate daily monitor historical: %v", err)
	}

	// Verify the aggregated data
	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection for verification: %v", err)
	}
	defer conn.Close()

	var avgLatencyMs, minLatencyMs, maxLatencyMs, successRate int
	err = conn.QueryRowContext(ctx, `
		SELECT avg_latency_ms, min_latency_ms, max_latency_ms, success_rate 
		FROM monitor_historical_daily_aggregate 
		WHERE monitor_id = ? AND date = ?
	`, monitorID, testDate.Format("2006-01-02")).Scan(&avgLatencyMs, &minLatencyMs, &maxLatencyMs, &successRate)
	if err != nil {
		t.Fatalf("failed to query aggregate data: %v", err)
	}

	// Verify values
	// AVG: (100+200+300+400+500+600+700+800+900+1000)/10 = 550
	// MIN: 100
	// MAX: 1000
	// Success rate: 8/10 * 100 = 80%
	if avgLatencyMs != 550 {
		t.Errorf("expected avg_latency_ms = 550, got %d", avgLatencyMs)
	}
	if minLatencyMs != 100 {
		t.Errorf("expected min_latency_ms = 100, got %d", minLatencyMs)
	}
	if maxLatencyMs != 1000 {
		t.Errorf("expected max_latency_ms = 1000, got %d", maxLatencyMs)
	}
	if successRate != 80 {
		t.Errorf("expected success_rate = 80, got %d", successRate)
	}

	// Test upsert by running aggregation again
	// Add more records to update the aggregate
	for i := 0; i < 5; i++ {
		submission := CheckerSubmissionRequest{
			MonitorID:  monitorID,
			LatencyMs:  50, // Lower latency
			StatusCode: 200,
			Timestamp:  testDate.Add(time.Minute * time.Duration(10+i)),
			Timings:    CheckerTraceTimings{},
		}

		err := ingesterWorker.ingestMonitorHistorical(t.Context(), submission, "us-east-1")
		if err != nil {
			t.Fatalf("failed to ingest monitor historical on upsert iteration %d: %v", i, err)
		}
	}

	// Run the aggregation again
	err = ingesterWorker.aggregateDailyMonitorHistorical(t.Context(), monitorID, testDate)
	if err != nil {
		t.Fatalf("failed to aggregate daily monitor historical on upsert: %v", err)
	}

	// Verify the updated aggregated data
	err = conn.QueryRowContext(ctx, `
		SELECT avg_latency_ms, min_latency_ms, max_latency_ms, success_rate 
		FROM monitor_historical_daily_aggregate 
		WHERE monitor_id = ? AND date = ?
	`, monitorID, testDate.Format("2006-01-02")).Scan(&avgLatencyMs, &minLatencyMs, &maxLatencyMs, &successRate)
	if err != nil {
		t.Fatalf("failed to query aggregate data after upsert: %v", err)
	}

	// Verify updated values
	// New total: 15 records
	// Latencies: 100,200,300,400,500,600,700,800,900,1000,50,50,50,50,50 = 5750
	// AVG: 5750/15 = 383.33... -> 383 (integer)
	// MIN: 50
	// MAX: 1000
	// Success rate: 13/15 * 100 = 86.67... -> 86 (truncated to smallint)
	if minLatencyMs != 50 {
		t.Errorf("expected min_latency_ms = 50 after upsert, got %d", minLatencyMs)
	}
	if maxLatencyMs != 1000 {
		t.Errorf("expected max_latency_ms = 1000 after upsert, got %d", maxLatencyMs)
	}
	// Success rate should be approximately 86-87 depending on rounding
	if successRate < 86 || successRate > 87 {
		t.Errorf("expected success_rate between 86-87 after upsert, got %d", successRate)
	}
}
