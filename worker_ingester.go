package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
	for {
		select {
		case <-w.shutdown:
			return nil
		default:
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			message, err := w.subscriber.Receive(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "receiving message for ingest queue", slog.String("error", err.Error()))
				time.Sleep(time.Millisecond * 10)
				continue
			}

			// Process the message here.
			var request CheckerSubmissionRequest
			if err := json.Unmarshal(message.Body, &request); err != nil {
				slog.ErrorContext(ctx, "unmarshaling ingest message", slog.String("error", err.Error()))
				if message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				continue
			}

			var region = "default"
			if message.Metadata != nil {
				if r, ok := message.Metadata["region"]; ok {
					region = r
				}
			}

			if err := w.ingestMonitorHistorical(ctx, request, region); err != nil {
				slog.ErrorContext(ctx, "ingesting monitor historical", slog.String("error", err.Error()))
			}

			message.Ack()
		}
	}
}

func (w *IngesterWorker) Stop() error {
	close(w.shutdown)
	return nil
}

func (w *IngesterWorker) ingestMonitorHistorical(ctx context.Context, submission CheckerSubmissionRequest, region string) error {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	// Insert raw monitor historical data
	_, err = conn.ExecContext(ctx, `
		INSERT INTO
			monitor_historical
			(
				monitor_id, 
				region, 
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
				?
			)
	`,
		submission.MonitorID,
		region,
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

	return nil
}

func (w *IngesterWorker) aggregateDailyMonitorHistorical(ctx context.Context, monitorID string, date time.Time) error {
	// FIXME: I don't think this is the correct logic. Might want to revisit this some other time.
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
			AVG(latency_ms) AS avg_latency_ms,
			MIN(latency_ms) AS min_latency_ms,
			MAX(latency_ms) AS max_latency_ms,
			(SUM(CASE WHEN status_code >= 200 AND status_code < 400 THEN 1 ELSE 0 END)::FLOAT / COUNT(*) * 100)::SMALLINT AS success_rate
		FROM monitor_historical
		WHERE monitor_id = $1 AND DATE(created_at) = $2
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

	return nil
}
