package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"gocloud.dev/pubsub"
)

// IngesterWorker is responsible for ingesting raw `monitor_historical`
// and create daily aggregate.
type IngesterWorker struct {
	db         *sql.DB
	subscriber *pubsub.Subscription
	shutdown   chan struct{}
}

func NewIngesterWorker(db *sql.DB, subscriber *pubsub.Subscription) *IngesterWorker {
	return &IngesterWorker{
		db:         db,
		subscriber: subscriber,
		shutdown:   make(chan struct{}),
	}
}

// Start begins the ingestion process, listening for new messages.
// It is a blocking call.
func (w *IngesterWorker) Start() error {
	// Start periodic aggregation in a separate goroutine
	go w.runPeriodicAggregation()

	for {
		select {
		case <-w.shutdown:
			return nil
		default:
			ctx, cancel := context.WithCancel(sentry.SetHubOnContext(context.Background(), sentry.CurrentHub().Clone()))

			message, err := w.subscriber.Receive(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "receiving message for ingest queue", slog.String("error", err.Error()))
				time.Sleep(time.Millisecond * 10)
				cancel()
				continue
			}

			span := sentry.StartTransaction(ctx, "ingester.process_submission", sentry.WithOpName("task.ingester.process"), sentry.WithTransactionSource(sentry.SourceCustom))
			ctx = span.Context()

			// Process the message here.
			var request CheckerSubmissionRequest
			if err := json.Unmarshal(message.Body, &request); err != nil {
				slog.ErrorContext(ctx, "unmarshaling ingest message", slog.String("error", err.Error()))
				if message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				span.Finish()
				cancel()
				continue
			}

			var region = "default"
			if message.Metadata != nil {
				if r, ok := message.Metadata["region"]; ok {
					region = r
				}
			}

			span.SetData("eyrie.monitor_id", request.MonitorID)
			span.SetData("eyrie.region", region)

			if err := w.ingestMonitorHistorical(ctx, request, region); err != nil {
				if hub := sentry.GetHubFromContext(ctx); hub != nil {
					hub.Scope().SetTag("eyrie.monitor_id", request.MonitorID)
					hub.Scope().SetTag("eyrie.region", region)
					hub.CaptureException(fmt.Errorf("ingesting monitor historical: %w", err))
				}
				slog.ErrorContext(ctx, "ingesting monitor historical", slog.String("error", err.Error()))
			}

			message.Ack()
			span.Finish()
			cancel()
		}
	}
}

func (w *IngesterWorker) Stop() error {
	close(w.shutdown)
	return nil
}

// runPeriodicAggregation runs the aggregation process every minute
func (w *IngesterWorker) runPeriodicAggregation() {
	const aggregationInterval = 1 * time.Minute
	ticker := time.NewTicker(aggregationInterval)
	defer ticker.Stop()

	// Run once immediately on startup
	w.aggregateAllMonitors()

	for {
		select {
		case <-w.shutdown:
			return
		case <-ticker.C:
			w.aggregateAllMonitors()
		}
	}
}

// aggregateAllMonitors aggregates data for all monitors with recent data
func (w *IngesterWorker) aggregateAllMonitors() {
	const aggregationLookbackDays = 30
	ctx := sentry.SetHubOnContext(context.Background(), sentry.CurrentHub().Clone())
	aggregationStartedAt := time.Now()
	span := sentry.StartTransaction(ctx, "ingester.aggregate_all", sentry.WithOpName("task.ingester.aggregate"), sentry.WithTransactionSource(sentry.SourceCustom))
	ctx = span.Context()
	defer span.Finish()

	conn, err := w.db.Conn(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "getting db connection for aggregation", slog.String("error", err.Error()))
		return
	}
	defer conn.Close()

	// Get all distinct monitor IDs and dates from the last 30 days
	rows, err := conn.QueryContext(ctx, `
		SELECT DISTINCT 
			monitor_id, 
			CAST(created_at AS DATE) AS date
		FROM monitor_historical
		WHERE created_at >= ?
	`, time.Now().AddDate(0, 0, -aggregationLookbackDays))
	if err != nil {
		slog.ErrorContext(ctx, "querying monitor IDs and dates for aggregation", slog.String("error", err.Error()))
		return
	}
	defer rows.Close()

	type aggregationTask struct {
		monitorID string
		date      time.Time
	}

	var tasks []aggregationTask
	for rows.Next() {
		var monitorID string
		var dateStr string
		if err := rows.Scan(&monitorID, &dateStr); err != nil {
			slog.ErrorContext(ctx, "scanning monitor ID and date", slog.String("error", err.Error()))
			continue
		}

		date, err := parseAggregationDate(dateStr)
		if err != nil {
			slog.ErrorContext(ctx, "parsing date", slog.String("error", err.Error()), slog.String("date", dateStr))
			continue
		}

		tasks = append(tasks, aggregationTask{monitorID: monitorID, date: date})
	}

	slog.InfoContext(ctx, "starting aggregation", slog.Int("task_count", len(tasks)))
	sentry.NewMeter(context.Background()).WithCtx(ctx).Gauge("eyrie.ingester.aggregation.tasks", float64(len(tasks)))

	// Aggregate each monitor/date combination
	for _, task := range tasks {
		if err := w.aggregateDailyMonitorHistorical(ctx, task.monitorID, task.date); err != nil {
			if hub := sentry.GetHubFromContext(ctx); hub != nil {
				hub.Scope().SetTag("eyrie.monitor_id", task.monitorID)
				hub.Scope().SetTag("eyrie.date", task.date.Format("2006-01-02"))
				hub.CaptureException(fmt.Errorf("aggregating daily monitor historical: %w", err))
			}
			slog.ErrorContext(ctx, "aggregating daily monitor historical",
				slog.String("monitor_id", task.monitorID),
				slog.String("date", task.date.Format("2006-01-02")),
				slog.String("error", err.Error()))
		}
	}

	sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.ingester.aggregation.duration", float64(time.Since(aggregationStartedAt).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond))
	slog.InfoContext(ctx, "aggregation completed", slog.Int("task_count", len(tasks)))
}

func parseAggregationDate(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported aggregation date format %q", raw)
}

func (w *IngesterWorker) ingestMonitorHistorical(ctx context.Context, submission CheckerSubmissionRequest, region string) error {
	span := sentry.StartSpan(ctx, "db.write", sentry.WithDescription("Ingest Monitor Historical"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()

	conn, err := w.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	probeType := submission.ProbeType
	if probeType == "" {
		probeType = string(MonitorTypeHTTP)
	}
	success := submission.Success
	if !success && (probeType == string(MonitorTypeHTTP) || probeType == "") {
		success = submission.StatusCode >= 200 && submission.StatusCode < 400
	}

	// Insert raw monitor historical data
	_, err = conn.ExecContext(ctx, `
		INSERT INTO
			monitor_historical
			(
				monitor_id, 
				region, 
				probe_type,
				success,
				failure_reason,
				status_code, 
				latency_ms, 
				response_body, 
				tls_version, 
				tls_cipher, 
				tls_expiry,
				timing_conn_acquired_ms,
				timing_first_response_byte_ms,
				timing_dns_lookup_start_ms,
				timing_dns_lookup_done_ms,
				timing_tls_handshake_start_ms,
				timing_tls_handshake_done_ms, 
				created_at
			)
		VALUES
			(
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?,
				?
			)
	`,
		submission.MonitorID,
		region,
		probeType,
		success,
		submission.FailureReason,
		submission.StatusCode,
		submission.LatencyMs,
		submission.ResponseBody,
		submission.TlsVersion,
		submission.TlsCipher,
		submission.TlsExpiry,
		submission.Timings.ConnAcquiredMs,
		submission.Timings.FirstResponseByteMs,
		submission.Timings.DNSLookupStartMs,
		submission.Timings.DNSLookupDoneMs,
		submission.Timings.TLSHandshakeStartMs,
		submission.Timings.TLSHandshakeDoneMs,
		submission.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("inserting monitor historical: %w", err)
	}

	sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.ingested", 1, sentry.WithAttributes(attribute.String("probe_type", probeType), attribute.String("region", region)))
	if submission.LatencyMs > 0 {
		sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.monitor.submissions.ingested_latency", float64(submission.LatencyMs), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", probeType), attribute.String("region", region)))
	}
	return nil
}

func (w *IngesterWorker) aggregateDailyMonitorHistorical(ctx context.Context, monitorID string, date time.Time) error {
	span := sentry.StartSpan(ctx, "db.aggregate", sentry.WithDescription("Aggregate Daily Monitor Historical"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()

	conn, err := w.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	// Aggregate daily data
	_, err = conn.ExecContext(ctx, `
		INSERT INTO monitor_historical_daily_aggregate (monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate)
		SELECT
			monitor_id,
			DATE(created_at) AS date,
			CAST(AVG(latency_ms) AS INTEGER) AS avg_latency_ms,
			MIN(latency_ms) AS min_latency_ms,
			MAX(latency_ms) AS max_latency_ms,
			CAST(CAST(SUM(CASE WHEN success THEN 1 ELSE 0 END) AS FLOAT) / COUNT(*) * 100 AS SMALLINT) AS success_rate
		FROM monitor_historical
		WHERE monitor_id = ? AND CAST(created_at AS DATE) = ?
		GROUP BY monitor_id, DATE(created_at)
		ON CONFLICT (monitor_id, date) DO UPDATE
		SET
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			min_latency_ms = EXCLUDED.min_latency_ms,
			max_latency_ms = EXCLUDED.max_latency_ms,
			success_rate = EXCLUDED.success_rate
	`, monitorID, date.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("aggregating daily monitor historical: %w", err)
	}

	_, err = conn.ExecContext(ctx, `
		INSERT INTO monitor_historical_region_daily_aggregate (monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate)
		SELECT
			monitor_id,
			region,
			DATE(created_at) AS date,
			CAST(AVG(latency_ms) AS INTEGER) AS avg_latency_ms,
			MIN(latency_ms) AS min_latency_ms,
			MAX(latency_ms) AS max_latency_ms,
			CAST(CAST(SUM(CASE WHEN success THEN 1 ELSE 0 END) AS FLOAT) / COUNT(*) * 100 AS SMALLINT) AS success_rate
		FROM monitor_historical
		WHERE monitor_id = ? AND CAST(created_at AS DATE) = ?
		GROUP BY monitor_id, region, DATE(created_at)
		ON CONFLICT (monitor_id, region, date) DO UPDATE
		SET
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			min_latency_ms = EXCLUDED.min_latency_ms,
			max_latency_ms = EXCLUDED.max_latency_ms,
			success_rate = EXCLUDED.success_rate
	`, monitorID, date.Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("aggregating region daily monitor historical: %w", err)
	}

	return nil
}
