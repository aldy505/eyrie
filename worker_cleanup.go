package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
)

// CleanupWorker enforces the dataset retention policy. Raw, hourly, and daily
// data are removed by dropping expired partitions; incident records and events
// are removed by timestamp. It also creates the current and future partitions
// on every run so the service keeps writing after midnight.
type CleanupWorker struct {
	db            *PostgresStore
	datasetConfig DatasetConfig
	interval      time.Duration
	stopCh        chan struct{}
	doneCh        chan struct{}
	stopOnce      sync.Once
	started       atomic.Bool
}

func NewCleanupWorker(db *PostgresStore, datasetConfig DatasetConfig) *CleanupWorker {
	return &CleanupWorker{
		db:            db,
		datasetConfig: datasetConfig,
		interval:      time.Duration(datasetConfig.CleanupIntervalMinutes) * time.Minute,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// Start runs the cleanup loop until Stop is called: one cleanup pass
// immediately on startup, then one every configured interval. It is a blocking
// call matching the other workers; run it in its own goroutine. A context
// derived from Stop is cancelled so an in-flight pass stops early. Calls after
// the first return nil without starting another loop.
func (w *CleanupWorker) Start() error {
	if w.interval <= 0 {
		return fmt.Errorf("cleanup worker interval must be greater than 0")
	}
	if !w.started.CompareAndSwap(false, true) {
		return nil
	}
	defer close(w.doneCh)

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
// returns promptly. It is safe to call more than once, and on a worker that
// was never started.
func (w *CleanupWorker) Stop() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	if w.started.Load() {
		<-w.doneCh
	}
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
	var stats RetentionStats
	var databaseBytes int64

	defer func() {
		span.Finish()
		meter := sentry.NewMeter(context.Background()).WithCtx(ctx)
		meter.Count("eyrie.cleanup.runs", 1, sentry.WithAttributes(attribute.String("status", status)))
		meter.Count("eyrie.cleanup.rows_deleted", stats.IncidentsDeleted+stats.EventsDeleted)
		meter.Count("eyrie.cleanup.partitions_created", int64(stats.PartitionsCreated))
		meter.Count("eyrie.cleanup.partitions_dropped", int64(stats.PartitionsDropped))
		meter.Distribution("eyrie.cleanup.duration_ms", float64(time.Since(startedAt).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond))
		meter.Gauge("eyrie.database.size.total_bytes", float64(databaseBytes))
		slog.InfoContext(ctx, "cleanup run completed",
			slog.String("status", status),
			slog.Int64("rows_deleted", stats.IncidentsDeleted+stats.EventsDeleted),
			slog.Int("partitions_created", stats.PartitionsCreated),
			slog.Int("partitions_dropped", stats.PartitionsDropped),
			slog.Int64("incidents_deleted", stats.IncidentsDeleted),
			slog.Int64("events_deleted", stats.EventsDeleted),
			slog.Duration("duration", time.Since(startedAt)),
			slog.Int64("total_bytes", databaseBytes))
	}()

	var err error
	stats, err = w.db.RunRetention(ctx, time.Now().UTC(), w.datasetConfig.RetentionDays, w.datasetConfig.PartitionLookaheadDays)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, &status, "running retention cleanup", err)
		return
	}

	if ctx.Err() != nil {
		return
	}

	databaseBytes, err = w.db.DatabaseSizeBytes(ctx)
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
