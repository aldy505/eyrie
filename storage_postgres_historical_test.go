package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestInsertMonitorHistoricalRejectsPastData(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()

	row := MonitorHistorical{
		MonitorID:  "late-monitor",
		Region:     "us-east-1",
		ProbeType:  string(MonitorTypeHTTP),
		Success:    true,
		StatusCode: 200,
		LatencyMs:  10,
		CreatedAt:  time.Now().UTC().AddDate(0, 0, -1),
	}
	outcome, err := store.InsertMonitorHistorical(ctx, row, 7)
	if err != nil {
		t.Fatalf("inserting past row: %v", err)
	}
	if outcome != RawRejectedOld {
		t.Fatalf("expected RawRejectedOld, got %v", outcome)
	}

	var count int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = $1`, row.MonitorID).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected past row to be rejected, got %d rows", count)
	}
}

func TestInsertMonitorHistoricalRejectsDistantFutureData(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()

	row := MonitorHistorical{
		MonitorID:  "future-monitor",
		Region:     "us-east-1",
		ProbeType:  string(MonitorTypeHTTP),
		Success:    true,
		StatusCode: 200,
		LatencyMs:  10,
		CreatedAt:  time.Now().UTC().AddDate(0, 0, 9),
	}
	outcome, err := store.InsertMonitorHistorical(ctx, row, 7)
	if err != nil {
		t.Fatalf("inserting future row: %v", err)
	}
	if outcome != RawRejectedFuture {
		t.Fatalf("expected RawRejectedFuture, got %v", outcome)
	}
}

func TestInsertMonitorHistoricalBatchIsIdempotent(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()

	rows := make([]MonitorHistorical, 0, 50)
	for i := 0; i < 50; i++ {
		rows = append(rows, MonitorHistorical{
			MonitorID:  "batch-monitor",
			Region:     "us-east-1",
			ProbeType:  string(MonitorTypeHTTP),
			Success:    true,
			StatusCode: 200,
			LatencyMs:  100 + i,
			CreatedAt:  now.Add(time.Duration(i) * time.Microsecond),
		})
	}

	outcomes, err := store.InsertMonitorHistoricalBatch(ctx, rows, 7)
	if err != nil {
		t.Fatalf("inserting batch: %v", err)
	}
	for i, outcome := range outcomes {
		if outcome != RawInserted {
			t.Fatalf("expected row %d to be inserted, got %v", i, outcome)
		}
	}

	outcomes, err = store.InsertMonitorHistoricalBatch(ctx, rows, 7)
	if err != nil {
		t.Fatalf("re-inserting batch: %v", err)
	}
	for i, outcome := range outcomes {
		if outcome != RawDuplicate {
			t.Fatalf("expected row %d to be a duplicate, got %v", i, outcome)
		}
	}

	var count int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = $1`, "batch-monitor").Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != len(rows) {
		t.Fatalf("expected %d rows, got %d", len(rows), count)
	}
}

func TestAggregateHourlyAndDailyRollups(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	hour := now.Truncate(time.Hour)

	if _, err := store.EnsurePartitions(ctx, now, 7); err != nil {
		t.Fatalf("ensuring partitions: %v", err)
	}

	rows := make([]MonitorHistorical, 0, 10)
	for i := 0; i < 10; i++ {
		statusCode := 200
		if i >= 8 {
			statusCode = 500
		}
		region := "us-east-1"
		if i%2 == 1 {
			region = "eu-west-1"
		}
		rows = append(rows, MonitorHistorical{
			MonitorID:  "rollup-monitor",
			Region:     region,
			ProbeType:  string(MonitorTypeHTTP),
			Success:    statusCode == 200,
			StatusCode: statusCode,
			LatencyMs:  (i + 1) * 100,
			CreatedAt:  hour.Add(time.Duration(i) * time.Minute),
		})
	}

	if _, err := store.InsertMonitorHistoricalBatch(ctx, rows, 7); err != nil {
		t.Fatalf("inserting raw rows: %v", err)
	}

	criteria := SuccessCriteria{ExpectedStatusCodes: []int{200}}
	if err := store.AggregateHourly(ctx, "rollup-monitor", hour, hour.Add(time.Hour), criteria); err != nil {
		t.Fatalf("aggregating hourly rollups: %v", err)
	}
	if err := store.AggregateDaily(ctx, "rollup-monitor", now); err != nil {
		t.Fatalf("aggregating daily rollups: %v", err)
	}

	var avg, min, max, successRate int
	var checkCount, successCount int64
	if err := store.pool.QueryRow(ctx, `
		SELECT avg_latency_ms, min_latency_ms, max_latency_ms, success_rate, check_count, success_count
		FROM monitor_historical_hourly_aggregate
		WHERE monitor_id = $1 AND bucket_start = $2
	`, "rollup-monitor", hour).Scan(&avg, &min, &max, &successRate, &checkCount, &successCount); err != nil {
		t.Fatalf("querying hourly rollup: %v", err)
	}
	if avg != 550 || min != 100 || max != 1000 || successRate != 80 || checkCount != 10 || successCount != 8 {
		t.Fatalf("unexpected hourly rollup: avg=%d min=%d max=%d success=%d checks=%d successes=%d", avg, min, max, successRate, checkCount, successCount)
	}

	if err := store.pool.QueryRow(ctx, `
		SELECT avg_latency_ms, min_latency_ms, max_latency_ms, success_rate, check_count, success_count
		FROM monitor_historical_daily_aggregate
		WHERE monitor_id = $1 AND date = $2
	`, "rollup-monitor", utcDayStart(now).Format("2006-01-02")).Scan(&avg, &min, &max, &successRate, &checkCount, &successCount); err != nil {
		t.Fatalf("querying daily rollup: %v", err)
	}
	if avg != 550 || min != 100 || max != 1000 || successRate != 80 || checkCount != 10 || successCount != 8 {
		t.Fatalf("unexpected daily rollup: avg=%d min=%d max=%d success=%d checks=%d successes=%d", avg, min, max, successRate, checkCount, successCount)
	}

	var regionRows int
	if err := store.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM monitor_historical_hourly_region_aggregate
		WHERE monitor_id = $1 AND bucket_start = $2
	`, "rollup-monitor", hour).Scan(&regionRows); err != nil {
		t.Fatalf("querying hourly region rollups: %v", err)
	}
	if regionRows != 2 {
		t.Fatalf("expected 2 hourly region rollups, got %d", regionRows)
	}
}

func TestPostgresConcurrentWritesAndReads(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()

	if _, err := store.EnsurePartitions(ctx, now, 7); err != nil {
		t.Fatalf("ensuring partitions: %v", err)
	}

	const writers = 8
	const rowsPerWriter = 100
	const readers = 8

	errCh := make(chan error, writers+readers)
	var wg sync.WaitGroup

	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			rows := make([]MonitorHistorical, 0, rowsPerWriter)
			for i := 0; i < rowsPerWriter; i++ {
				rows = append(rows, MonitorHistorical{
					MonitorID:  fmt.Sprintf("concurrent-monitor-%d", writer%4),
					Region:     fmt.Sprintf("region-%d", writer%3),
					ProbeType:  string(MonitorTypeHTTP),
					Success:    true,
					StatusCode: 200,
					LatencyMs:  10 + i,
					CreatedAt:  now.Add(time.Duration(writer*rowsPerWriter+i) * time.Microsecond),
				})
			}
			if _, err := store.InsertMonitorHistoricalBatch(ctx, rows, 7); err != nil {
				errCh <- fmt.Errorf("writer %d: %w", writer, err)
			}
		}(writer)
	}

	for reader := 0; reader < readers; reader++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				monitorIDs, err := store.MonitorIDsSince(ctx, now.Add(-time.Hour))
				if err != nil {
					errCh <- fmt.Errorf("reader %d: %w", reader, err)
					return
				}
				_ = monitorIDs
			}
		}(reader)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM monitor_historical WHERE monitor_id LIKE 'concurrent-monitor-%'`).Scan(&count); err != nil {
		t.Fatalf("counting concurrent rows: %v", err)
	}
	if count != writers*rowsPerWriter {
		t.Fatalf("expected %d rows, got %d", writers*rowsPerWriter, count)
	}
}

func TestEnsureRawPartitionRejectsPastWithoutTestSeam(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()

	// The test store normally allows historical rows for fixed-date fixtures;
	// disable that seam to exercise the production policy.
	store.allowHistoricalData = false
	err := store.EnsureRawPartition(ctx, time.Now().UTC().AddDate(0, 0, -1), 7)
	if !errors.Is(err, ErrHistoricalDataTooOld) {
		t.Fatalf("expected ErrHistoricalDataTooOld, got %v", err)
	}
}

func BenchmarkPostgresInsertMonitorHistoricalBatch(b *testing.B) {
	store := newPostgresTestStore(b)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.EnsurePartitions(ctx, now, 7); err != nil {
		b.Fatalf("ensuring partitions: %v", err)
	}

	const batchSize = 100
	rows := make([]MonitorHistorical, batchSize)
	for i := range rows {
		rows[i] = MonitorHistorical{
			MonitorID:  "benchmark-monitor",
			Region:     "us-east-1",
			ProbeType:  string(MonitorTypeHTTP),
			Success:    true,
			StatusCode: 200,
			LatencyMs:  100,
			CreatedAt:  now.Add(time.Duration(i) * time.Microsecond),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range rows {
			rows[j].CreatedAt = now.Add(time.Duration(i*batchSize+j) * time.Microsecond)
		}
		if _, err := store.InsertMonitorHistoricalBatch(ctx, rows, 7); err != nil {
			b.Fatalf("inserting batch: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*batchSize)/b.Elapsed().Seconds(), "rows/s")
}
