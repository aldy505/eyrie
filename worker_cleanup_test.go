package main

import (
	"context"
	"testing"
	"time"
)

// cleanupWorkerTestData registers cleanup of all rows belonging to monitorID
// across the tables the cleanup worker touches.
func cleanupWorkerTestData(t *testing.T, monitorID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()

		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection for cleanup: %v", err)
		}
		defer conn.Close()

		for _, query := range []string{
			`DELETE FROM monitor_historical WHERE monitor_id = ?`,
			`DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`,
			`DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`,
		} {
			if _, err := conn.ExecContext(ctx, query, monitorID); err != nil {
				t.Fatalf("failed to clean up test data: %v", err)
			}
		}
	})
}

func TestCleanupWorker_DeletesExpiredHistoricalRows(t *testing.T) {
	monitorID := "cleanup-test-expired-rows"
	cleanupWorkerTestData(t, monitorID)

	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	now := time.Now().UTC()
	today := utcDayStart(now)
	retentionDays := 3

	// expired sits strictly before the cutoff day and must be deleted.
	// boundary sits on the cutoff day itself (older than the cutoff instant)
	// and must be kept until the next pass, per the day-granularity loop.
	rows := map[string]time.Time{
		"expired":  today.AddDate(0, 0, -(retentionDays + 1)),
		"boundary": today.AddDate(0, 0, -retentionDays).Add(5 * time.Hour),
		"fresh":    today.AddDate(0, 0, -1).Add(10 * time.Hour),
		"now":      now,
	}
	for name, createdAt := range rows {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO monitor_historical (monitor_id, region, status_code, latency_ms, created_at)
			VALUES (?, 'us-east-1', 200, 10, ?)
		`, monitorID, createdAt); err != nil {
			t.Fatalf("failed to insert %s row: %v", name, err)
		}
	}

	worker := &CleanupWorker{
		db:            db,
		datasetConfig: DatasetConfig{RetentionDays: retentionDays, CleanupIntervalMinutes: 60, CleanupBatchSize: 1000},
	}
	worker.runCleanup(ctx)

	var expiredCount int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = ? AND created_at = ?
	`, monitorID, rows["expired"]).Scan(&expiredCount); err != nil {
		t.Fatalf("failed to query expired row: %v", err)
	}
	if expiredCount != 0 {
		t.Errorf("expected expired row to be deleted, got %d rows remaining", expiredCount)
	}

	var keptCount int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = ?
	`, monitorID).Scan(&keptCount); err != nil {
		t.Fatalf("failed to query remaining rows: %v", err)
	}
	if keptCount != 3 {
		t.Errorf("expected 3 rows to remain (boundary, fresh, now), got %d", keptCount)
	}
}

func TestCleanupWorker_PrunesExpiredAggregates(t *testing.T) {
	monitorID := "cleanup-test-expired-aggregates"
	cleanupWorkerTestData(t, monitorID)

	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	now := time.Now().UTC()
	today := utcDayStart(now)
	retentionDays := 3
	oldDate := today.AddDate(0, 0, -(retentionDays + 1)) // before cutoff day, pruned
	freshDate := today.AddDate(0, 0, -1)                 // within retention, kept

	for _, date := range []time.Time{oldDate, freshDate} {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO monitor_historical_daily_aggregate (monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate)
			VALUES (?, ?, 50, 10, 100, 100)
		`, monitorID, date); err != nil {
			t.Fatalf("failed to insert daily aggregate for %v: %v", date, err)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO monitor_historical_region_daily_aggregate (monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate)
			VALUES (?, 'us-east-1', ?, 50, 10, 100, 100)
		`, monitorID, date); err != nil {
			t.Fatalf("failed to insert region aggregate for %v: %v", date, err)
		}
	}

	worker := &CleanupWorker{
		db:            db,
		datasetConfig: DatasetConfig{RetentionDays: retentionDays, CleanupIntervalMinutes: 60, CleanupBatchSize: 1000},
	}
	worker.runCleanup(ctx)

	for _, table := range []string{
		"monitor_historical_daily_aggregate",
		"monitor_historical_region_daily_aggregate",
	} {
		var oldCount int
		if err := conn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM `+table+` WHERE monitor_id = ? AND date = ?
		`, monitorID, oldDate).Scan(&oldCount); err != nil {
			t.Fatalf("failed to query %s for old date: %v", table, err)
		}
		if oldCount != 0 {
			t.Errorf("expected %s rows before cutoff to be pruned, got %d", table, oldCount)
		}

		var freshCount int
		if err := conn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM `+table+` WHERE monitor_id = ? AND date = ?
		`, monitorID, freshDate).Scan(&freshCount); err != nil {
			t.Fatalf("failed to query %s for fresh date: %v", table, err)
		}
		if freshCount != 1 {
			t.Errorf("expected %s row within retention to remain, got %d", table, freshCount)
		}
	}
}

func TestCleanupWorker_NoExpiredRowsNoDeletion(t *testing.T) {
	monitorID := "cleanup-test-no-expired"
	cleanupWorkerTestData(t, monitorID)

	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO monitor_historical (monitor_id, region, status_code, latency_ms, created_at)
		VALUES (?, 'us-east-1', 200, 10, ?)
	`, monitorID, time.Now().UTC()); err != nil {
		t.Fatalf("failed to insert fresh row: %v", err)
	}

	worker := &CleanupWorker{
		db:            db,
		datasetConfig: DatasetConfig{RetentionDays: 3, CleanupIntervalMinutes: 60, CleanupBatchSize: 1000},
	}

	deleted, err := worker.deleteExpiredHistorical(ctx, conn)
	if err != nil {
		t.Fatalf("expected no error deleting expired rows, got %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 rows deleted, got %d", deleted)
	}

	// End-to-end pass with no expired rows must complete without error.
	worker.runCleanup(ctx)

	var keptCount int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = ?
	`, monitorID).Scan(&keptCount); err != nil {
		t.Fatalf("failed to query remaining rows: %v", err)
	}
	if keptCount != 1 {
		t.Errorf("expected fresh row to remain, got %d rows", keptCount)
	}
}

func TestCleanupWorker_StopReturnsPromptly(t *testing.T) {
	worker := &CleanupWorker{
		db:            db,
		datasetConfig: DatasetConfig{RetentionDays: 90, CleanupIntervalMinutes: 60, CleanupBatchSize: 1000},
		interval:      10 * time.Millisecond,
		stopCh:        make(chan struct{}),
	}

	started := make(chan error, 1)
	go func() {
		started <- worker.Start()
	}()

	// Give the immediate run and at least one tick time to start.
	time.Sleep(30 * time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		worker.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup worker Stop did not return promptly")
	}

	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("cleanup worker Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup worker Start did not return after Stop")
	}

	// Stop is idempotent and must not panic or block.
	if err := worker.Stop(); err != nil {
		t.Fatalf("second Stop returned error: %v", err)
	}
}
