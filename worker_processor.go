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
			// TODO: Make the lookback duration configurable.
			lookbackSince := time.Now().Add(-5 * time.Minute)
			historicalSubmissions, err := w.lookback(ctx, request.MonitorID, lookbackSince)
			if err != nil {
				slog.ErrorContext(ctx, "looking back historical submissions", slog.String("error", err.Error()))
				message.Ack()
				cancel()
				continue
			}

			// Group submissions by minute buckets.
			minuteInterval := time.Minute
			bucketedSubmissions, latestTime, earliestTime := w.groupSubmissionByMinute(historicalSubmissions, minuteInterval)

			// Once we group the submission by minute, we can analyze whether there is a widespread failure.
			// We can start tracking from `latestTime` to `earliestTime` using the minute interval.
			// If a bucket is missing, we take that as zero submissions for that minute.
			// Later, if on a certain bucket, there are > 50% failures from multiple regions, we trigger an alert.

			// Analyze the historical submissions along with the current one to determine if an alert should be sent.
			shouldAlert, alertReason, err := w.analyzeSubmissions(monitor, bucketedSubmissions, latestTime, earliestTime, minuteInterval)
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

func (w *ProcessorWorker) groupSubmissionByMinute(submissions []MonitorHistorical, interval time.Duration) (buckets map[time.Time][]MonitorHistorical, earliestTime time.Time, latestTime time.Time) {
	// Earliest means the oldest time. Latest means the most recent time.
	earliestTime = time.Now().Add(100 * 365 * 24 * time.Hour) // Far future time
	latestTime = time.Now()
	buckets = make(map[time.Time][]MonitorHistorical)

	for _, submission := range submissions {
		bucketTime := submission.CreatedAt.Truncate(interval)
		buckets[bucketTime] = append(buckets[bucketTime], submission)

		// Track earliest and latest time
		// Say bucket time is 2024-01-01 10:15:00, the first comparison with the
		// initialized earliestTime and latestTime will always update both.
		if bucketTime.Before(earliestTime) {
			earliestTime = bucketTime
		}
		if bucketTime.After(latestTime) {
			latestTime = bucketTime
		}
	}

	return buckets, earliestTime, latestTime
}

func (w *ProcessorWorker) analyzeSubmissions(monitor Monitor, bucketedSubmissions map[time.Time][]MonitorHistorical, latestTime time.Time, earliestTime time.Time, minuteInterval time.Duration) (shouldAlert bool, alertReason string, err error) {
	// We group each submission by date time bucket (e.g., minute).
	// For each bucket, we can see if the failures is coming from multiple regions.
	// If yes, we check whether the current submission is also a failure.
	// If yes, we trigger an alert.

	type SubmissionBucket struct {
		TimestampMinute time.Time
		// Regions keeps track of unique regions in this bucket.
		// The value of the map represents whether the region has a successful check.
		Regions map[string]bool
		// FailureCount counts the number of failures in this bucket
		FailureCount int
		// TotalCount counts the total number of submissions in this bucket
		TotalCount int
	}

	buckets := make(map[time.Time]*SubmissionBucket)

	// We start from earliestTime to latestTime, increasing by minuteInterval
	for t := earliestTime; !t.After(latestTime); t = t.Add(minuteInterval) {
		bucketedSubmission, exists := bucketedSubmissions[t]
		if !exists {
			// TotalCount and FailureCount of zero means no submissions during this minute
			buckets[t] = &SubmissionBucket{
				TimestampMinute: t,
				Regions:         make(map[string]bool),
			}
			continue
		}

		// Process submissions in this bucket
		submissionBucket := &SubmissionBucket{
			TimestampMinute: t,
			Regions:         make(map[string]bool),
			FailureCount:    0,
			TotalCount:      0,
		}

		// We want to accumulate the regions and failure counts.
		// We also want to make sure that for a bucket that has multiple
		// submissions from the same region would be counted correctly.
		type RegionSubmission struct {
			TotalCount int
			Failures   int
		}
		var regionSubmissionCount = make(map[string]RegionSubmission)
		for _, submission := range bucketedSubmission {
			isSuccessful := slices.Contains(monitor.ExpectedStatusCodes, submission.StatusCode)
			oldRegionSubmission, exists := regionSubmissionCount[submission.Region]

			if !exists {
				failures := 0
				if !isSuccessful {
					failures = 1
				}
				regionSubmissionCount[submission.Region] = RegionSubmission{TotalCount: 1, Failures: failures}
			} else {
				failures := oldRegionSubmission.Failures
				if !isSuccessful {
					failures++
				}
				regionSubmissionCount[submission.Region] = RegionSubmission{
					TotalCount: oldRegionSubmission.TotalCount + 1,
					Failures:   failures,
				}
			}
		}

		for region, rs := range regionSubmissionCount {
			// Shall we have a little margin of error here? Like if there are 10 submissions from a region,
			// and only 1 failed, we can still consider the region as successful?
			if rs.Failures < rs.TotalCount {
				// At least one success in this region
				submissionBucket.Regions[region] = true
				submissionBucket.TotalCount += 1
				continue
			} else if rs.Failures == rs.TotalCount {
				// All failed in this region
				submissionBucket.Regions[region] = false
				submissionBucket.FailureCount += 1
				submissionBucket.TotalCount += 1
				continue
			}

			var perRegionFailureRate = float64(rs.Failures) / float64(rs.TotalCount)
			// TODO: Make threshold configurable
			if perRegionFailureRate >= 0.4 {
				// Consider this region as failed if more than 40% of submissions failed
				submissionBucket.Regions[region] = false
				submissionBucket.FailureCount += 1
			} else {
				// Consider this region as successful
				submissionBucket.Regions[region] = true
			}
			submissionBucket.TotalCount += 1
		}

		buckets[t] = submissionBucket
	}

	// Now we analyze the buckets to see if there is a widespread failure,
	// starting from the latestTime to earliestTime.
	// If the state changes from healthy to unhealthy for the latest bucket,
	// we trigger an alert. And vice versa, if the state changes from unhealthy
	// to healthy for the latest bucket, we can also trigger a recovery alert.
	// But, if there is no state change, we do nothing.
	//
	// We still need to iterate through all buckets from the latestTime because
	// we can't know for sure if there are buckets with 0 TotalCount in between.

	var stateHealthy bool = true
	for t := latestTime; !t.Before(earliestTime); t = t.Add(-minuteInterval) {
		currentBucketTime := t

		submissionBucket, exists := buckets[currentBucketTime]
		if !exists {
			// No submissions in this bucket, skip
			// TODO: We should log this case for visibility
			continue
		}

		if submissionBucket.TotalCount == 0 {
			// No submissions in this bucket, skip
			continue
		}

		// TODO: Make failure rate threshold configurable
		failureRate := float64(submissionBucket.FailureCount) / float64(submissionBucket.TotalCount)
		if len(submissionBucket.Regions) > 1 && failureRate >= 0.5 {
			// Unhealthy state
			if currentBucketTime.Equal(latestTime) {
				// State changed from healthy to unhealthy on the latest bucket, trigger alert
				// List out regions that reported failures
				var regionNames []string
				for region, success := range submissionBucket.Regions {
					if !success {
						regionNames = append(regionNames, region)
					}
				}
				shouldAlert = true
				alertReason = fmt.Sprintf("High failure rate detected: %.2f%% failures from %d regions (%s)", failureRate*100, len(submissionBucket.Regions), strings.Join(regionNames, ", "))
				return shouldAlert, alertReason, nil
			}

			if !stateHealthy {
				shouldAlert = false
				return shouldAlert, "", nil
			} else {
				// Current state is healthy. Let's trigger a recovery alert.
				shouldAlert = true
				alertReason = "Monitor has recovered and is now healthy"
				return shouldAlert, alertReason, nil
			}
		} else if len(submissionBucket.Regions) == 1 && failureRate >= 0.5 {
			// Unhealthy state (only one region with failures)
			// If this is the latest bucket, don't want to trigger an alert immediately here,
			// but we want to check if the next bucket is also unhealthy.
			if currentBucketTime.Equal(latestTime) {
				stateHealthy = false
				continue
			}

			// Entering this block means we are not in the latest bucket.
			// For whatever reason, let's trigger the alert.
			if !stateHealthy {
				shouldAlert = true
				alertReason = fmt.Sprintf("High failure rate detected: %.2f%% failures from single region", failureRate*100)
				return shouldAlert, alertReason, nil
			} else {
				// Current state is healthy. Let's trigger a recovery alert.
				shouldAlert = true
				alertReason = "Monitor has recovered and is now healthy"
				return shouldAlert, alertReason, nil
			}
		} else if failureRate < 0.5 {
			// Healthy state
			if currentBucketTime.Equal(latestTime) {
				stateHealthy = true
				continue
			}

			if !stateHealthy {
				// State changed from unhealthy to healthy on the latest bucket, trigger recovery alert
				shouldAlert = true
				alertReason = "Monitor has recovered and is now healthy"
				return shouldAlert, alertReason, nil
			}

			stateHealthy = true
		}
	}

	// No state change detected on the latest bucket
	return false, "", nil
}
