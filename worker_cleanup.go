package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
)

// CleanupWorker enforces the dataset retention policy online by deleting raw
// `monitor_historical` rows older than `dataset.retention_days`, pruning the
// derived daily aggregates that can no longer be recalculated, and running a
// CHECKPOINT so freed blocks are reclaimed inside the file. It emits size and
// deletion metrics via Sentry on every run.
type CleanupWorker struct {
	db            *sql.DB
	datasetConfig DatasetConfig
	interval      time.Duration
	stopCh        chan struct{}
	stopOnce      sync.Once
	wg            sync.WaitGroup
}

func NewCleanupWorker(db *sql.DB, datasetConfig DatasetConfig) *CleanupWorker {
	return &CleanupWorker{
		db:            db,
		datasetConfig: datasetConfig,
		interval:      time.Duration(datasetConfig.CleanupIntervalMinutes) * time.Minute,
		stopCh:        make(chan struct{}),
	}
}

// Start runs the cleanup loop until Stop is called: one cleanup pass
// immediately on startup, then one every configured interval. It is a blocking
// call matching the other workers; run it in its own goroutine. A context
// derived from Stop is cancelled so an in-flight pass stops early.
func (w *CleanupWorker) Start() error {
	if w.interval <= 0 {
		return fmt.Errorf("cleanup worker interval must be greater than 0")
	}

	w.wg.Add(1)
	defer w.wg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-w.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	if ctx.Err() == nil {
		w.runCleanup(ctx)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return nil
		case <-ticker.C:
			w.runCleanup(ctx)
		}
	}
}

// Stop signals the worker to stop, waits for the cleanup loop to exit, and
// returns promptly. It is safe to call more than once.
func (w *CleanupWorker) Stop() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.wg.Wait()
	return nil
}

// runCleanup performs one retention cleanup pass and reports the outcome via
// Sentry metrics. Errors are logged, reported as exceptions, and counted as
// status=error. A cancelled context stops the pass early without an error
// status so shutdown is not reported as a failure.
func (w *CleanupWorker) runCleanup(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	startedAt := time.Now()
	ctx = sentry.SetHubOnContext(ctx, sentry.CurrentHub().Clone())
	span := sentry.StartTransaction(ctx, "cleanup.run", sentry.WithOpName("task.cleanup.run"), sentry.WithTransactionSource(sentry.SourceCustom))
	ctx = span.Context()

	status := "success"
	var rowsDeleted int64
	var totalBytes, freeBytes uint64

	defer func() {
		span.Finish()
		meter := sentry.NewMeter(context.Background()).WithCtx(ctx)
		meter.Count("eyrie.cleanup.runs", 1, sentry.WithAttributes(attribute.String("status", status)))
		meter.Count("eyrie.cleanup.rows_deleted", rowsDeleted)
		meter.Distribution("eyrie.cleanup.duration_ms", float64(time.Since(startedAt).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond))
		slog.InfoContext(ctx, "cleanup run completed",
			slog.String("status", status),
			slog.Int64("rows_deleted", rowsDeleted),
			slog.Duration("duration", time.Since(startedAt)),
			slog.Uint64("total_bytes", totalBytes),
			slog.Uint64("free_bytes", freeBytes))
	}()

	conn, err := w.db.Conn(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "acquiring database connection for cleanup", err)
		return
	}
	defer conn.Close()

	totalBytes, freeBytes, err = w.captureDatabaseSize(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "capturing pre-cleanup database size", err)
		return
	}

	if ctx.Err() != nil {
		return
	}

	deleted, err := w.deleteExpiredHistorical(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "deleting expired monitor_historical rows", err)
		return
	}
	rowsDeleted += deleted

	if ctx.Err() != nil {
		return
	}

	pruned, err := w.pruneAggregates(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "pruning expired aggregate rows", err)
		return
	}
	rowsDeleted += pruned

	// CHECKPOINT failure is reported but does not block the worker: the deletes
	// are already committed and the next tick retries the remaining work.
	if err := w.checkpoint(ctx, conn); err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "checkpointing database", err)
	}

	if ctx.Err() != nil {
		return
	}

	totalBytes, freeBytes, err = w.captureDatabaseSize(ctx, conn)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "capturing post-cleanup database size", err)
	}
}

func (w *CleanupWorker) fail(ctx context.Context, status *string, message string, err error) {
	*status = "error"
	if hub := sentry.GetHubFromContext(ctx); hub != nil {
		hub.CaptureException(fmt.Errorf("%s: %w", message, err))
	}
	slog.ErrorContext(ctx, message, slog.String("error", err.Error()))
}

func (w *CleanupWorker) cutoffTime() time.Time {
	return time.Now().UTC().AddDate(0, 0, -w.datasetConfig.RetentionDays)
}

// captureDatabaseSize reads the current database file sizes from
// pragma_database_size and emits them as Sentry gauges.
func (w *CleanupWorker) captureDatabaseSize(ctx context.Context, conn *sql.Conn) (uint64, uint64, error) {
	var blockSize, totalBlocks, freeBlocks uint64
	if err := conn.QueryRowContext(ctx, `
		SELECT block_size, total_blocks, free_blocks
		FROM pragma_database_size()
	`).Scan(&blockSize, &totalBlocks, &freeBlocks); err != nil {
		return 0, 0, err
	}

	totalBytes := blockSize * totalBlocks
	freeBytes := blockSize * freeBlocks

	meter := sentry.NewMeter(context.Background()).WithCtx(ctx)
	meter.Gauge("eyrie.database.size.total_bytes", float64(totalBytes))
	meter.Gauge("eyrie.database.size.free_bytes", float64(freeBytes))

	return totalBytes, freeBytes, nil
}

// deleteExpiredHistorical deletes raw rows older than the retention window,
// one full day at a time. Days are deleted in chunks of at most
// CleanupBatchSize rows per statement. Only days strictly before the day
// containing the cutoff are removed, so rows from the current cutoff day are
// kept until the next pass; this keeps in-flight writes safe.
func (w *CleanupWorker) deleteExpiredHistorical(ctx context.Context, conn *sql.Conn) (int64, error) {
	cutoff := w.cutoffTime()
	cutoffDay := utcDayStart(cutoff)

	var oldestRaw sql.NullTime
	if err := conn.QueryRowContext(ctx, `
		SELECT MIN(CAST(created_at AS DATE))
		FROM monitor_historical
		WHERE created_at < ?
	`, cutoff).Scan(&oldestRaw); err != nil {
		return 0, err
	}
	if !oldestRaw.Valid {
		return 0, nil
	}

	var total int64
	for day := utcDayStart(oldestRaw.Time); day.Before(cutoffDay); day = day.AddDate(0, 0, 1) {
		if err := ctx.Err(); err != nil {
			return total, nil
		}

		deleted, err := w.deleteDayRange(ctx, conn, day, day.AddDate(0, 0, 1))
		if err != nil {
			return total, err
		}
		total += deleted
	}

	return total, nil
}

// deleteDayRange deletes all rows in [start, end), chunked by
// CleanupBatchSize rows per statement so no single statement removes more than
// the configured maximum.
func (w *CleanupWorker) deleteDayRange(ctx context.Context, conn *sql.Conn, start, end time.Time) (int64, error) {
	batchSize := int64(w.datasetConfig.CleanupBatchSize)

	var total int64
	for {
		res, err := conn.ExecContext(ctx, `
			DELETE FROM monitor_historical
			WHERE created_at >= ? AND created_at < ?
			  AND rowid IN (
				SELECT rowid FROM monitor_historical
				WHERE created_at >= ? AND created_at < ?
				LIMIT ?
			  )
		`, start, end, start, end, batchSize)
		if err != nil {
			return total, err
		}

		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n

		if n < batchSize {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, nil
		}
	}
}

// pruneAggregates removes daily aggregate rows that fall before the cutoff day.
// They can no longer be recalculated once their raw rows are gone.
func (w *CleanupWorker) pruneAggregates(ctx context.Context, conn *sql.Conn) (int64, error) {
	cutoffDay := utcDayStart(w.cutoffTime())

	var total int64
	for _, query := range []string{
		`DELETE FROM monitor_historical_daily_aggregate WHERE date < ?`,
		`DELETE FROM monitor_historical_region_daily_aggregate WHERE date < ?`,
	} {
		res, err := conn.ExecContext(ctx, query, cutoffDay)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}

	return total, nil
}

// checkpoint reclaims free blocks inside the database file. It does not shrink
// the file on disk; that requires the offline compaction script.
func (w *CleanupWorker) checkpoint(ctx context.Context, conn *sql.Conn) error {
	// Concurrent workers (ingester aggregation, submission ingestion) hold
	// short write transactions, and DuckDB CHECKPOINT fails while any is
	// active. Retry briefly so a transient collision does not fail the run;
	// only a persistent failure is reported as an error.
	const attempts = 3
	var err error
	for i := range attempts {
		if _, err = conn.ExecContext(ctx, `CHECKPOINT`); err == nil {
			return nil
		}
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return err
}
