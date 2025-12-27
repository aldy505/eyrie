package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"gocloud.dev/pubsub"
)

type ProcessorWorker struct {
	db              *sql.DB
	subscriber      *pubsub.Subscription
	alerterProducer *pubsub.Topic
	shutdown        chan struct{}
	monitorConfig   MonitorConfig
}

func NewProcessorWorker(db *sql.DB, subscriber *pubsub.Subscription, alerterProducer *pubsub.Topic, monitorConfig MonitorConfig) *ProcessorWorker {
	return &ProcessorWorker{
		db:              db,
		subscriber:      subscriber,
		alerterProducer: alerterProducer,
		shutdown:        make(chan struct{}),
		monitorConfig:   monitorConfig,
	}
}

// Start begins the processing of incoming tasks.
// It is a blocking call.
func (w *ProcessorWorker) Start() error {
	for {
		select {
		case <-w.shutdown:
			return nil
		default:
			ctx, cancel := context.WithCancel(context.Background())

			message, err := w.subscriber.Receive(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "receiving message for processor queue", slog.String("error", err.Error()))
				time.Sleep(time.Millisecond * 10)
				cancel()
				continue
			}

			// Process the message here.
			var request CheckerSubmissionRequest
			if err := json.Unmarshal(message.Body, &request); err != nil {
				slog.ErrorContext(ctx, "unmarshaling processor message", slog.String("error", err.Error()))
				if message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				cancel()
				continue
			}

			var region = "default"
			if message.Metadata != nil {
				if r, ok := message.Metadata["region"]; ok {
					region = r
				}
			}

			var monitor Monitor
			var monitorFound bool
			for _, m := range w.monitorConfig.Monitors {
				if m.ID == request.MonitorID {
					monitor = m
					monitorFound = true
					break
				}
			}

			if !monitorFound {
				slog.ErrorContext(ctx, "monitor not found for submission", slog.String("region", region), slog.String("monitor_id", request.MonitorID))
				message.Ack()
				cancel()
				continue
			}

			slog.InfoContext(ctx, "processing submission", slog.String("region", region), slog.String("submission_id", message.LoggableID))

			// We look at the recent submissions, from similar time period (e.g., last 5 minutes) but from different regions.
			// If we see a significant number of failures from multiple regions, we trigger an alert.
			lookbackSince := time.Now().Add(-5 * time.Minute)
			historicalSubmissions, err := w.lookback(ctx, request.MonitorID, lookbackSince)
			if err != nil {
				slog.ErrorContext(ctx, "looking back historical submissions", slog.String("error", err.Error()))
				message.Ack()
				cancel()
				continue
			}

			// Analyze the historical submissions along with the current one to determine if an alert should be sent.
			shouldAlert, alertReason, err := w.analyzeSubmissions(monitor, historicalSubmissions, request, region)
			if err != nil {
				slog.ErrorContext(ctx, "analyzing submissions for alerting", slog.String("error", err.Error()))
				message.Ack()
				cancel()
				continue
			}

			if shouldAlert {
				slog.InfoContext(ctx, "triggering alert for monitor", slog.String("monitor_id", request.MonitorID), slog.String("reason", alertReason))

				alert := AlertMessage{
					MonitorID:  request.MonitorID,
					Name:       monitor.Name,
					Reason:     alertReason,
					OccurredAt: time.Now().UTC(),
				}

				alertBody, err := json.Marshal(alert)
				if err != nil {
					slog.ErrorContext(ctx, "marshaling alert message", slog.String("error", err.Error()))
					message.Ack()
					cancel()
					continue
				}

				err = w.alerterProducer.Send(ctx, &pubsub.Message{
					Body: alertBody,
					Metadata: map[string]string{
						"monitor_id": request.MonitorID,
					},
				})
				if err != nil {
					slog.ErrorContext(ctx, "sending alert message", slog.String("error", err.Error()))
					message.Ack()
					cancel()
					continue
				}
			}

			// Acknowledge the message after processing.
			message.Ack()
			cancel()
		}
	}
}

func (w *ProcessorWorker) Stop() error {
	w.shutdown <- struct{}{}
	close(w.shutdown)
	return nil
}

func (w *ProcessorWorker) lookback(ctx context.Context, monitorId string, since time.Time) ([]MonitorHistorical, error) {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, region, status_code, latency_ms, response_body, tls_version, tls_cipher, tls_expiry, created_at
		FROM monitor_historical
		WHERE monitor_id = ? AND created_at >= ? AND created_at <= ?
	`, monitorId, since, time.Now())
	if err != nil {
		return nil, fmt.Errorf("querying monitor historical: %w", err)
	}
	defer rows.Close()

	var results []MonitorHistorical
	for rows.Next() {
		var mh MonitorHistorical
		if err := rows.Scan(&mh.MonitorID, &mh.Region, &mh.StatusCode, &mh.LatencyMs, &mh.ResponseBody, &mh.TlsVersion, &mh.TlsCipher, &mh.TlsExpiry, &mh.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning monitor historical: %w", err)
		}
		results = append(results, mh)
	}

	return results, nil
}

func (w *ProcessorWorker) analyzeSubmissions(monitor Monitor, historicalSubmissions []MonitorHistorical, currentSubmission CheckerSubmissionRequest, currentRegion string) (bool, string, error) {
	// We group each submission by date time bucket (e.g., minute).
	// For each bucket, we can see if the failures is coming from multiple regions.
	// If yes, we check whether the current submission is also a failure.
	// If yes, we trigger an alert.

	type SubmissionBucket struct {
		TimestampMinute time.Time
		Regions         map[string]bool
		FailureCount    int
		TotalCount      int
	}

	buckets := make(map[time.Time]*SubmissionBucket)

	// Process historical submissions
	for _, submission := range historicalSubmissions {
		bucketTime := submission.CreatedAt.Truncate(time.Minute)
		if _, exists := buckets[bucketTime]; !exists {
			buckets[bucketTime] = &SubmissionBucket{
				TimestampMinute: bucketTime,
				Regions:         make(map[string]bool),
			}
		}
		bucket := buckets[bucketTime]
		bucket.TotalCount++
		bucket.Regions[submission.Region] = true
		if !slices.Contains(monitor.ExpectedStatusCodes, submission.StatusCode) {
			bucket.FailureCount++
		}
	}

	// Process current submission
	currentBucketTime := currentSubmission.Timestamp.Truncate(time.Minute)
	if _, exists := buckets[currentBucketTime]; !exists {
		buckets[currentBucketTime] = &SubmissionBucket{
			TimestampMinute: currentBucketTime,
			Regions:         make(map[string]bool),
		}
	}
	currentBucket := buckets[currentBucketTime]
	currentBucket.TotalCount++
	currentBucket.Regions[currentRegion] = true
	if !slices.Contains(monitor.ExpectedStatusCodes, currentSubmission.StatusCode) {
		currentBucket.FailureCount++
	}

	// Analyze buckets to determine if an alert should be sent
	// Also handle if there is only one region on a bucket, it should not trigger an alert.
	// Only react to the latest bucket (current submission's bucket)
	latestBucket, exists := buckets[currentBucketTime]
	if !exists {
		return false, "", nil
	}

	if latestBucket.TotalCount == 0 {
		return false, "", nil
	}

	failureRate := float64(latestBucket.FailureCount) / float64(latestBucket.TotalCount)
	if len(latestBucket.Regions) > 1 && failureRate >= 0.5 {
		regionNames := make([]string, 0, len(latestBucket.Regions))
		for region := range latestBucket.Regions {
			regionNames = append(regionNames, region)
		}
		alertReason := fmt.Sprintf("High failure rate detected: %.2f%% failures from %d regions (%s)", failureRate*100, len(latestBucket.Regions), strings.Join(regionNames, ", "))
		return true, alertReason, nil
	}

	return false, "", nil
}
