package main

import (
	"context"
	"testing"
	"time"
)

func cleanupWorkerTestData(t *testing.T, monitorID string) {
	t.Helper()
	requireTestDB(t)

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
			`DELETE FROM monitor_historical_hourly_aggregate WHERE monitor_id = ?`,
			`DELETE FROM monitor_historical_hourly_region_aggregate WHERE monitor_id = ?`,
			`DELETE FROM monitor_historical_daily_aggregate WHERE monitor_id = ?`,
			`DELETE FROM monitor_historical_region_daily_aggregate WHERE monitor_id = ?`,
			`DELETE FROM monitor_incident_events WHERE monitor_id = ?`,
			`DELETE FROM monitor_incidents WHERE monitor_id = ?`,
			`DELETE FROM monitor_incident_state WHERE monitor_id = ?`,
		} {
			if _, err := conn.ExecContext(ctx, query, monitorID); err != nil {
				t.Fatalf("failed to clean up test data: %v", err)
			}
		}
	})
}

func createCleanupTestPartition(t *testing.T, spec partitionSpec, day time.Time) {
	t.Helper()
	ctx := t.Context()
	if err := db.ensurePartitionForDay(ctx, spec, day); err != nil {
		t.Fatalf("failed to create partition %s: %v", partitionName(spec.table, day), err)
	}
}

func cleanupTestPartitionExists(t *testing.T, name string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRowContext(t.Context(), `SELECT to_regclass(?) IS NOT NULL`, name).Scan(&exists); err != nil {
		t.Fatalf("failed to check partition %s: %v", name, err)
	}
	return exists
}

func TestCleanupWorker_DropsExpiredRawPartitions(t *testing.T) {
	requireTestDB(t)
	monitorID := "cleanup-test-expired-raw-partitions"
	cleanupWorkerTestData(t, monitorID)

	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	now := time.Now().UTC()
	today := utcDayStart(now)
	retentionDays := 90
	expiredDay := today.AddDate(0, 0, -(retentionDays + 10))
	boundaryDay := today.AddDate(0, 0, -retentionDays)
	freshDay := today.AddDate(0, 0, -1)

	createCleanupTestPartition(t, partitionSpecs[0], expiredDay)
	createCleanupTestPartition(t, partitionSpecs[0], boundaryDay)

	rows := map[string]time.Time{
		"expired":  expiredDay.Add(5 * time.Hour),
		"boundary": boundaryDay.Add(5 * time.Hour),
		"fresh":    freshDay.Add(10 * time.Hour),
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
		datasetConfig: DatasetConfig{RetentionDays: retentionDays, CleanupIntervalMinutes: 60, PartitionLookaheadDays: 7},
	}
	worker.runCleanup(ctx)

	if cleanupTestPartitionExists(t, partitionName(partitionSpecs[0].table, expiredDay)) {
		t.Fatal("expected expired raw partition to be dropped")
	}
	if !cleanupTestPartitionExists(t, partitionName(partitionSpecs[0].table, boundaryDay)) {
		t.Fatal("expected cutoff-day raw partition to be kept")
	}

	var expiredCount int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = ? AND created_at = ?
	`, monitorID, rows["expired"]).Scan(&expiredCount); err != nil {
		t.Fatalf("failed to query expired row: %v", err)
	}
	if expiredCount != 0 {
		t.Errorf("expected expired row to be dropped with its partition, got %d rows", expiredCount)
	}

	var keptCount int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitor_historical WHERE monitor_id = ?
	`, monitorID).Scan(&keptCount); err != nil {
		t.Fatalf("failed to query remaining rows: %v", err)
	}
	if keptCount != 2 {
		t.Errorf("expected 2 rows to remain (boundary, fresh), got %d", keptCount)
	}
}

func TestCleanupWorker_DropsExpiredRollupPartitions(t *testing.T) {
	requireTestDB(t)
	monitorID := "cleanup-test-expired-rollup-partitions"
	cleanupWorkerTestData(t, monitorID)

	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	now := time.Now().UTC()
	today := utcDayStart(now)
	retentionDays := 90
	expiredDay := today.AddDate(0, 0, -(retentionDays + 10))
	boundaryDay := today.AddDate(0, 0, -retentionDays)

	for _, spec := range []partitionSpec{partitionSpecs[3], partitionSpecs[4]} {
		createCleanupTestPartition(t, spec, expiredDay)
		createCleanupTestPartition(t, spec, boundaryDay)
	}

	for _, date := range []time.Time{expiredDay, boundaryDay} {
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
		datasetConfig: DatasetConfig{RetentionDays: retentionDays, CleanupIntervalMinutes: 60, PartitionLookaheadDays: 7},
	}
	worker.runCleanup(ctx)

	for _, spec := range []partitionSpec{partitionSpecs[3], partitionSpecs[4]} {
		if cleanupTestPartitionExists(t, partitionName(spec.table, expiredDay)) {
			t.Errorf("expected expired %s partition to be dropped", spec.table)
		}
		if !cleanupTestPartitionExists(t, partitionName(spec.table, boundaryDay)) {
			t.Errorf("expected cutoff-day %s partition to be kept", spec.table)
		}
	}

	for _, table := range []string{
		"monitor_historical_daily_aggregate",
		"monitor_historical_region_daily_aggregate",
	} {
		var oldCount int
		if err := conn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM `+table+` WHERE monitor_id = ? AND date = ?
		`, monitorID, expiredDay).Scan(&oldCount); err != nil {
			t.Fatalf("failed to query %s for old date: %v", table, err)
		}
		if oldCount != 0 {
			t.Errorf("expected %s rows before cutoff to be dropped, got %d", table, oldCount)
		}

		var boundaryCount int
		if err := conn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM `+table+` WHERE monitor_id = ? AND date = ?
		`, monitorID, boundaryDay).Scan(&boundaryCount); err != nil {
			t.Fatalf("failed to query %s for boundary date: %v", table, err)
		}
		if boundaryCount != 1 {
			t.Errorf("expected %s cutoff-day row to remain, got %d", table, boundaryCount)
		}
	}
}

func TestCleanupWorker_DeletesExpiredIncidentsByTimestamp(t *testing.T) {
	requireTestDB(t)
	monitorID := "cleanup-test-expired-incidents"
	cleanupWorkerTestData(t, monitorID)

	ctx := t.Context()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to get db connection: %v", err)
	}
	defer conn.Close()

	now := time.Now().UTC()
	oldTime := now.AddDate(0, 0, -100)
	recentTime := now.Add(-time.Hour)

	insertIncident := func(id string, startedAt time.Time, resolvedAt *time.Time) {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO monitor_incidents (id, monitor_id, started_at, resolved_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, id, monitorID, startedAt, resolvedAt, startedAt, startedAt); err != nil {
			t.Fatalf("failed to insert incident %s: %v", id, err)
		}
	}
	insertEvent := func(id string, incidentID string, createdAt time.Time) {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO monitor_incident_events (id, incident_id, monitor_id, event_type, created_at)
			VALUES (?, ?, ?, 'updated', ?)
		`, id, incidentID, monitorID, createdAt); err != nil {
			t.Fatalf("failed to insert event %s: %v", id, err)
		}
	}

	insertIncident("old-incident", oldTime, nil)
	insertEvent("old-event", "old-incident", oldTime)
	insertEvent("old-incident-recent-event", "old-incident", recentTime)
	insertIncident("recent-incident", recentTime, nil)
	insertEvent("recent-event", "recent-incident", recentTime)

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO monitor_incident_state (monitor_id, last_transition_at, updated_at)
		VALUES (?, ?, ?)
	`, monitorID, oldTime, oldTime); err != nil {
		t.Fatalf("failed to insert incident state: %v", err)
	}

	worker := &CleanupWorker{
		db:            db,
		datasetConfig: DatasetConfig{RetentionDays: 90, CleanupIntervalMinutes: 60, PartitionLookaheadDays: 7},
	}
	worker.runCleanup(ctx)

	var incidentCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incidents WHERE monitor_id = ?`, monitorID).Scan(&incidentCount); err != nil {
		t.Fatalf("failed to query incidents: %v", err)
	}
	if incidentCount != 1 {
		t.Errorf("expected only the recent incident to remain, got %d", incidentCount)
	}

	var oldIncidentCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incidents WHERE id = 'old-incident'`).Scan(&oldIncidentCount); err != nil {
		t.Fatalf("failed to query old incident: %v", err)
	}
	if oldIncidentCount != 0 {
		t.Error("expected old incident to be deleted")
	}

	var oldEventCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incident_events WHERE id IN ('old-event', 'old-incident-recent-event')`).Scan(&oldEventCount); err != nil {
		t.Fatalf("failed to query old events: %v", err)
	}
	if oldEventCount != 0 {
		t.Error("expected events belonging to the old incident to be deleted")
	}

	var recentEventCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incident_events WHERE id = 'recent-event'`).Scan(&recentEventCount); err != nil {
		t.Fatalf("failed to query recent event: %v", err)
	}
	if recentEventCount != 1 {
		t.Error("expected recent incident event to remain")
	}

	var stateCount int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incident_state WHERE monitor_id = ?`, monitorID).Scan(&stateCount); err != nil {
		t.Fatalf("failed to query incident state: %v", err)
	}
	if stateCount != 1 {
		t.Error("expected current incident state to remain")
	}
}

func TestCleanupWorker_NoExpiredDataNoDeletion(t *testing.T) {
	requireTestDB(t)
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
		datasetConfig: DatasetConfig{RetentionDays: 90, CleanupIntervalMinutes: 60, PartitionLookaheadDays: 7},
	}
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
	requireTestDB(t)
	worker := NewCleanupWorker(db, DatasetConfig{RetentionDays: 90, CleanupIntervalMinutes: 60, PartitionLookaheadDays: 7})
	worker.interval = 10 * time.Millisecond

	started := make(chan error, 1)
	go func() {
		started <- worker.Start()
	}()

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

	if err := worker.Stop(); err != nil {
		t.Fatalf("second Stop returned error: %v", err)
	}
}
