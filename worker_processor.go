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

			// Group submissions by minute buckets and pre-process statistics.
			minuteInterval := time.Minute
			buckets, earliestTime, latestTime := w.groupSubmissionByMinute(historicalSubmissions, minuteInterval, monitor)

			// Once we group the submission by minute, we can analyze whether there is a widespread failure.
			// We can start tracking from `latestTime` to `earliestTime` using the minute interval.
			// If a bucket is missing, we take that as zero submissions for that minute.
			// Later, if on a certain bucket, there are > 50% failures from multiple regions, we trigger an alert.

			// Analyze the pre-processed buckets to determine if an alert should be sent.
			shouldAlert, alertReason, err := w.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)
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

// SubmissionBucket represents aggregated submission data for a time bucket
type SubmissionBucket struct {
	TimestampMinute time.Time
	// Regions keeps track of unique regions in this bucket.
	// The value of the map represents whether the region has a successful check.
	Regions map[string]bool
	// FailureCount counts the number of regions that are considered failed
	FailureCount int
	// TotalCount counts the total number of regions in this bucket
	TotalCount int
}

func (w *ProcessorWorker) groupSubmissionByMinute(submissions []MonitorHistorical, interval time.Duration, monitor Monitor) (buckets map[time.Time]SubmissionBucket, earliestTime time.Time, latestTime time.Time) {
	// Earliest means the oldest time. Latest means the most recent time.
	earliestTime = time.Now().UTC().Add(100 * 365 * 24 * time.Hour) // Far future time
	latestTime = time.Now().UTC().Add(-100 * 365 * 24 * time.Hour)

	// First, group raw submissions by time bucket
	rawBuckets := make(map[time.Time][]MonitorHistorical)

	for _, submission := range submissions {
		bucketTime := submission.CreatedAt.Truncate(interval)
		rawBuckets[bucketTime] = append(rawBuckets[bucketTime], submission)

		// Track earliest and latest time.
		// Since earliestTime is initialized to a far future time, the first bucketTime
		// will always update earliestTime. latestTime starts at time.Now() and is only
		// updated when bucketTime is more recent than the current latestTime.
		if bucketTime.Before(earliestTime) {
			earliestTime = bucketTime
		}
		if bucketTime.After(latestTime) {
			latestTime = bucketTime
		}
	}

	// Now pre-process each bucket to calculate statistics
	buckets = make(map[time.Time]SubmissionBucket)

	for bucketTime, bucketSubmissions := range rawBuckets {
		submissionBucket := SubmissionBucket{
			TimestampMinute: bucketTime,
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
		regionSubmissionCount := make(map[string]RegionSubmission)

		for _, submission := range bucketSubmissions {
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

		// Now process each region's aggregated data
		for region, rs := range regionSubmissionCount {
			if rs.Failures == 0 {
				// All successful submissions in this region
				submissionBucket.Regions[region] = true
			} else if rs.Failures == rs.TotalCount {
				// All failed submissions in this region
				submissionBucket.Regions[region] = false
				submissionBucket.FailureCount += 1
			} else {
				// Mixed successes and failures in this region; use a failure-rate threshold.
				perRegionFailureRate := float64(rs.Failures) / float64(rs.TotalCount)
				// TODO: Make threshold configurable
				if perRegionFailureRate >= 0.4 {
					// Consider this region as failed if 40% or more of submissions failed
					submissionBucket.Regions[region] = false
					submissionBucket.FailureCount += 1
				} else {
					// Consider this region as successful
					submissionBucket.Regions[region] = true
				}
			}
			submissionBucket.TotalCount += 1
		}

		buckets[bucketTime] = submissionBucket
	}

	return buckets, earliestTime, latestTime
}

func (w *ProcessorWorker) analyzeSubmissions(buckets map[time.Time]SubmissionBucket, latestTime time.Time, earliestTime time.Time, minuteInterval time.Duration) (shouldAlert bool, alertReason string, err error) {
	// We analyze the buckets to see if there is a widespread failure,
	// starting from the latestTime to earliestTime.
	// If the state changes from healthy to unhealthy for the latest bucket,
	// we trigger an alert. And vice versa, if the state changes from unhealthy
	// to healthy for the latest bucket, we can also trigger a recovery alert.
	// But, if there is no state change, we do nothing.
	//
	// We still need to iterate through all buckets from the latestTime because
	// we can't know for sure if there are buckets with 0 TotalCount in between.

	type HealthState int
	const (
		StateHealthy HealthState = iota
		StateUnhealthyMultiRegion
		StateUnhealthySingleRegion
	)

	// Track state transitions through the time series
	type BucketAnalysis struct {
		bucketTime  time.Time
		state       HealthState
		failureRate float64
		regionCount int
		regionNames []string
		isLatest    bool
	}

	var analyses []BucketAnalysis

	// Analyze all buckets from latest to earliest
	for t := latestTime; !t.Before(earliestTime); t = t.Add(-minuteInterval) {
		submissionBucket, exists := buckets[t]

		// Skip buckets with no data
		if !exists {
			continue
		}

		if submissionBucket.TotalCount == 0 {
			continue
		}

		isLatest := t.Equal(latestTime)
		failureRate := float64(submissionBucket.FailureCount) / float64(submissionBucket.TotalCount)
		regionCount := len(submissionBucket.Regions)

		var currentState HealthState
		var failedRegions []string

		// Determine the health state for this bucket
		if regionCount > 1 && failureRate >= 0.5 {
			currentState = StateUnhealthyMultiRegion
			for region, success := range submissionBucket.Regions {
				if !success {
					failedRegions = append(failedRegions, region)
				}
			}
		} else if regionCount == 1 && failureRate >= 0.5 {
			currentState = StateUnhealthySingleRegion
		} else {
			currentState = StateHealthy
		}

		analyses = append(analyses, BucketAnalysis{
			bucketTime:  t,
			state:       currentState,
			failureRate: failureRate,
			regionCount: regionCount,
			regionNames: failedRegions,
			isLatest:    isLatest,
		})
	}

	// If no data was analyzed, return no alert
	if len(analyses) == 0 {
		return false, "", nil
	}

	// Now evaluate the latest bucket state against the historical state
	latestAnalysis := analyses[0] // First element is the latest because we iterate backwards

	// Determine the previous state (the state immediately before the latest bucket)
	var previousState HealthState = StateHealthy
	if len(analyses) > 1 {
		previousState = analyses[1].state
	}

	switch latestAnalysis.state {
	case StateUnhealthyMultiRegion:
		// Multi-region failure in latest bucket
		if previousState == StateHealthy {
			// State changed from healthy to unhealthy - trigger alert
			return true, fmt.Sprintf("High failure rate detected: %.2f%% failures from %d regions (%s)",
				latestAnalysis.failureRate*100,
				latestAnalysis.regionCount,
				strings.Join(latestAnalysis.regionNames, ", ")), nil
		}
		// Already unhealthy, no alert
		return false, "", nil

	case StateUnhealthySingleRegion:
		// Single-region failure in latest bucket
		// We only trigger alert for consecutive single-region failures to avoid
		// false positives from transient single-region issues
		if previousState == StateUnhealthySingleRegion {
			// Consecutive single-region failure detected, trigger alert
			return true, fmt.Sprintf("High failure rate detected: %.2f%% failures from single region",
				latestAnalysis.failureRate*100), nil
		}
		// First occurrence of single-region failure or previous state was different
		return false, "", nil

	case StateHealthy:
		// Latest bucket is healthy
		if previousState != StateHealthy {
			// State changed from unhealthy to healthy - trigger recovery alert
			return true, "Monitor has recovered and is now healthy", nil
		}
		// Was already healthy, no alert
		return false, "", nil
	}

	// Default: no alert
	return false, "", nil
}
