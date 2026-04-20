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

	"github.com/getsentry/sentry-go"
	"gocloud.dev/pubsub"
)

type ProcessorWorker struct {
	db              *sql.DB
	subscriber      *pubsub.Subscription
	alerterProducer *pubsub.Topic
	shutdown        chan struct{}
	monitorConfig   MonitorConfig
	datasetConfig   DatasetConfig
}

func NewProcessorWorker(db *sql.DB, subscriber *pubsub.Subscription, alerterProducer *pubsub.Topic, monitorConfig MonitorConfig, datasetConfig DatasetConfig) *ProcessorWorker {
	return &ProcessorWorker{
		db:              db,
		subscriber:      subscriber,
		alerterProducer: alerterProducer,
		shutdown:        make(chan struct{}),
		monitorConfig:   monitorConfig,
		datasetConfig:   datasetConfig,
	}
}

func (w *ProcessorWorker) Start() error {
	for {
		select {
		case <-w.shutdown:
			return nil
		default:
			ctx, cancel := context.WithCancel(sentry.SetHubOnContext(context.Background(), sentry.CurrentHub().Clone()))
			message, err := w.subscriber.Receive(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "receiving message for processor queue", slog.String("error", err.Error()))
				time.Sleep(10 * time.Millisecond)
				cancel()
				continue
			}

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

			monitor, ok := w.findMonitor(request.MonitorID)
			if !ok {
				slog.ErrorContext(ctx, "monitor not found for submission", slog.String("monitor_id", request.MonitorID))
				message.Ack()
				cancel()
				continue
			}

			lookbackSince := time.Now().Add(-time.Duration(w.datasetConfig.ProcessingLookbackMinutes) * time.Minute)
			historicalSubmissions, err := w.lookback(ctx, request.MonitorID, lookbackSince)
			if err != nil {
				slog.ErrorContext(ctx, "looking back historical submissions", slog.String("error", err.Error()))
				message.Ack()
				cancel()
				continue
			}

			buckets, earliestTime, latestTime := w.groupSubmissionByMinute(historicalSubmissions, time.Minute, monitor)
			evaluation := w.evaluateLatestIncident(buckets, latestTime, earliestTime, time.Minute)

			previous, err := w.loadIncidentState(ctx, request.MonitorID)
			if err != nil {
				slog.ErrorContext(ctx, "loading incident state", slog.String("error", err.Error()))
				message.Ack()
				cancel()
				continue
			}

			if !previous.Equal(evaluation) {
				if err := w.storeIncidentState(ctx, request.MonitorID, evaluation); err != nil {
					slog.ErrorContext(ctx, "storing incident state", slog.String("error", err.Error()))
					message.Ack()
					cancel()
					continue
				}

				if err := w.publishAlert(ctx, monitor, evaluation, previous); err != nil {
					slog.ErrorContext(ctx, "publishing alert", slog.String("error", err.Error()))
					message.Ack()
					cancel()
					continue
				}
			}

			message.Ack()
			cancel()
		}
	}
}

func (w *ProcessorWorker) Stop() error {
	close(w.shutdown)
	return nil
}

func (w *ProcessorWorker) findMonitor(monitorID string) (Monitor, bool) {
	for _, monitor := range w.monitorConfig.Monitors {
		if monitor.ID == monitorID {
			return monitor, true
		}
	}
	return Monitor{}, false
}

func (w *ProcessorWorker) lookback(ctx context.Context, monitorID string, since time.Time) ([]MonitorHistorical, error) {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, region, probe_type, success, failure_reason, status_code, latency_ms, response_body, tls_version, tls_cipher, tls_expiry, created_at
		FROM monitor_historical
		WHERE monitor_id = ? AND created_at >= ? AND created_at <= ?
	`, monitorID, since, time.Now())
	if err != nil {
		return nil, fmt.Errorf("querying monitor historical: %w", err)
	}
	defer rows.Close()

	var results []MonitorHistorical
	for rows.Next() {
		var historical MonitorHistorical
		if err := rows.Scan(
			&historical.MonitorID,
			&historical.Region,
			&historical.ProbeType,
			&historical.Success,
			&historical.FailureReason,
			&historical.StatusCode,
			&historical.LatencyMs,
			&historical.ResponseBody,
			&historical.TlsVersion,
			&historical.TlsCipher,
			&historical.TlsExpiry,
			&historical.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning monitor historical: %w", err)
		}
		results = append(results, historical)
	}

	return results, nil
}

type SubmissionBucket struct {
	TimestampMinute time.Time
	Regions         map[string]bool
	FailureCount    int
	TotalCount      int
}

func (w *ProcessorWorker) groupSubmissionByMinute(submissions []MonitorHistorical, interval time.Duration, monitor Monitor) (buckets map[time.Time]SubmissionBucket, earliestTime time.Time, latestTime time.Time) {
	earliestTime = time.Now().UTC().Add(100 * 365 * 24 * time.Hour)
	latestTime = time.Now().UTC().Add(-100 * 365 * 24 * time.Hour)

	rawBuckets := make(map[time.Time][]MonitorHistorical)
	for _, submission := range submissions {
		bucketTime := submission.CreatedAt.Truncate(interval)
		rawBuckets[bucketTime] = append(rawBuckets[bucketTime], submission)
		if bucketTime.Before(earliestTime) {
			earliestTime = bucketTime
		}
		if bucketTime.After(latestTime) {
			latestTime = bucketTime
		}
	}

	buckets = make(map[time.Time]SubmissionBucket)
	for bucketTime, bucketSubmissions := range rawBuckets {
		submissionBucket := SubmissionBucket{
			TimestampMinute: bucketTime,
			Regions:         make(map[string]bool),
		}

		type regionSubmission struct {
			totalCount int
			failures   int
		}
		regionSubmissionCount := make(map[string]regionSubmission)

		for _, submission := range bucketSubmissions {
			isSuccessful := monitor.IsSuccessfulStatus(submission.StatusCode, submission.Success)
			existing := regionSubmissionCount[submission.Region]
			if !isSuccessful {
				existing.failures++
			}
			existing.totalCount++
			regionSubmissionCount[submission.Region] = existing
		}

		for region, regionStats := range regionSubmissionCount {
			perRegionFailureRate := float64(regionStats.failures) / float64(regionStats.totalCount)
			success := perRegionFailureRate < (w.datasetConfig.PerRegionFailureThresholdPercent / 100.0)
			submissionBucket.Regions[region] = success
			submissionBucket.TotalCount++
			if !success {
				submissionBucket.FailureCount++
			}
		}

		buckets[bucketTime] = submissionBucket
	}

	return buckets, earliestTime, latestTime
}

func (w *ProcessorWorker) evaluateLatestIncident(buckets map[time.Time]SubmissionBucket, latestTime time.Time, earliestTime time.Time, minuteInterval time.Duration) IncidentEvaluation {
	if len(buckets) == 0 || earliestTime.After(latestTime) || minuteInterval <= 0 {
		return HealthyIncidentEvaluation()
	}

	for t := latestTime; !t.Before(earliestTime); t = t.Add(-minuteInterval) {
		bucket, ok := buckets[t]
		if !ok || bucket.TotalCount == 0 {
			continue
		}

		failedRegions := make([]string, 0, len(bucket.Regions))
		for region, success := range bucket.Regions {
			if !success {
				failedRegions = append(failedRegions, region)
			}
		}
		slices.Sort(failedRegions)

		if len(failedRegions) == 0 {
			return HealthyIncidentEvaluation()
		}

		failureRate := float64(len(failedRegions)) / float64(bucket.TotalCount)
		switch {
		case bucket.TotalCount == len(failedRegions):
			scope := MonitorScopeLocal
			if bucket.TotalCount > 1 {
				scope = MonitorScopeGlobal
			}
			return IncidentEvaluation{
				Status:          MonitorStatusDown,
				Scope:           scope,
				Reason:          fmt.Sprintf("Probe failures detected in all reporting regions (%s)", strings.Join(failedRegions, ", ")),
				AffectedRegions: failedRegions,
			}
		case bucket.TotalCount > 1 && failureRate >= w.datasetConfig.FailureThresholdPercent/100.0:
			return IncidentEvaluation{
				Status:          MonitorStatusDown,
				Scope:           MonitorScopeGlobal,
				Reason:          fmt.Sprintf("Multi-region outage detected across %d/%d regions (%s)", len(failedRegions), bucket.TotalCount, strings.Join(failedRegions, ", ")),
				AffectedRegions: failedRegions,
			}
		default:
			return IncidentEvaluation{
				Status:          MonitorStatusDegraded,
				Scope:           MonitorScopeLocal,
				Reason:          fmt.Sprintf("Regional degradation detected in %s", strings.Join(failedRegions, ", ")),
				AffectedRegions: failedRegions,
			}
		}
	}

	return HealthyIncidentEvaluation()
}

func (w *ProcessorWorker) analyzeSubmissions(buckets map[time.Time]SubmissionBucket, latestTime time.Time, earliestTime time.Time, minuteInterval time.Duration) (bool, string, error) {
	if earliestTime.After(latestTime) || minuteInterval <= 0 {
		return false, "", nil
	}

	type HealthState int
	const (
		StateHealthy HealthState = iota
		StateUnhealthyMultiRegion
		StateUnhealthySingleRegion
	)

	type BucketAnalysis struct {
		state       HealthState
		failureRate float64
		regionCount int
		regionNames []string
	}

	var analyses []BucketAnalysis

	for t := latestTime; !t.Before(earliestTime); t = t.Add(-minuteInterval) {
		submissionBucket, exists := buckets[t]
		if !exists || submissionBucket.TotalCount == 0 {
			continue
		}

		failureRate := float64(submissionBucket.FailureCount) / float64(submissionBucket.TotalCount)
		regionCount := len(submissionBucket.Regions)

		var currentState HealthState
		var failedRegions []string
		for region, success := range submissionBucket.Regions {
			if !success {
				failedRegions = append(failedRegions, region)
			}
		}
		slices.Sort(failedRegions)

		if regionCount > 1 && failureRate >= w.datasetConfig.FailureThresholdPercent/100.0 {
			currentState = StateUnhealthyMultiRegion
		} else if regionCount == 1 && failureRate >= w.datasetConfig.FailureThresholdPercent/100.0 {
			currentState = StateUnhealthySingleRegion
		} else {
			currentState = StateHealthy
		}

		analyses = append(analyses, BucketAnalysis{
			state:       currentState,
			failureRate: failureRate,
			regionCount: regionCount,
			regionNames: failedRegions,
		})
	}

	if len(analyses) == 0 {
		return false, "", nil
	}

	latestAnalysis := analyses[0]
	previousState := StateHealthy
	if len(analyses) > 1 {
		previousState = analyses[1].state
	}

	switch latestAnalysis.state {
	case StateUnhealthyMultiRegion:
		if previousState == StateHealthy {
			return true, fmt.Sprintf("High failure rate detected: %.2f%% failures from %d regions (%s)",
				latestAnalysis.failureRate*100,
				latestAnalysis.regionCount,
				strings.Join(latestAnalysis.regionNames, ", ")), nil
		}
		return false, "", nil
	case StateUnhealthySingleRegion:
		if previousState == StateUnhealthySingleRegion {
			return true, fmt.Sprintf("High failure rate detected: %.2f%% failures from single region",
				latestAnalysis.failureRate*100), nil
		}
		return false, "", nil
	case StateHealthy:
		if previousState != StateHealthy {
			return true, "Monitor has recovered and is now healthy", nil
		}
		return false, "", nil
	default:
		return false, "", nil
	}
}

func (w *ProcessorWorker) loadIncidentState(ctx context.Context, monitorID string) (IncidentEvaluation, error) {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return HealthyIncidentEvaluation(), fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	var status string
	var scope string
	var regions string
	var reason string
	err = conn.QueryRowContext(ctx, `
		SELECT status, scope, affected_regions, reason
		FROM monitor_incident_state
		WHERE monitor_id = ?
	`, monitorID).Scan(&status, &scope, &regions, &reason)
	if err != nil {
		if err == sql.ErrNoRows {
			return HealthyIncidentEvaluation(), nil
		}
		return HealthyIncidentEvaluation(), fmt.Errorf("querying incident state: %w", err)
	}

	return IncidentEvaluation{
		Status:          status,
		Scope:           scope,
		Reason:          reason,
		AffectedRegions: ParseRegionsJSON(regions),
	}, nil
}

func (w *ProcessorWorker) storeIncidentState(ctx context.Context, monitorID string, evaluation IncidentEvaluation) error {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	now := time.Now().UTC()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO monitor_incident_state (monitor_id, status, scope, affected_regions, reason, last_transition_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (monitor_id) DO UPDATE
		SET
			status = EXCLUDED.status,
			scope = EXCLUDED.scope,
			affected_regions = EXCLUDED.affected_regions,
			reason = EXCLUDED.reason,
			last_transition_at = EXCLUDED.last_transition_at,
			updated_at = EXCLUDED.updated_at
	`, monitorID, evaluation.Status, evaluation.Scope, evaluation.RegionsJSON(), evaluation.Reason, now, now)
	if err != nil {
		return fmt.Errorf("upserting incident state: %w", err)
	}

	return nil
}

func (w *ProcessorWorker) publishAlert(ctx context.Context, monitor Monitor, current IncidentEvaluation, previous IncidentEvaluation) error {
	alertReason := current.Reason
	if current.Status == MonitorStatusHealthy && previous.Status != MonitorStatusHealthy {
		alertReason = fmt.Sprintf("Monitor recovered from %s %s", previous.Scope, previous.Status)
	}

	alert := AlertMessage{
		MonitorID:       monitor.ID,
		Name:            monitor.Name,
		Status:          current.Status,
		Scope:           current.Scope,
		Reason:          alertReason,
		OccurredAt:      time.Now().UTC(),
		AffectedRegions: current.AffectedRegions,
	}

	alertBody, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("marshaling alert message: %w", err)
	}

	return w.alerterProducer.Send(ctx, &pubsub.Message{
		Body: alertBody,
		Metadata: map[string]string{
			"monitor_id": monitor.ID,
			"status":     current.Status,
			"scope":      current.Scope,
		},
	})
}
