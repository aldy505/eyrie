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
