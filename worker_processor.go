package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"github.com/google/uuid"
	"github.com/guregu/null/v5"
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
			processingStartedAt := time.Now()
			span := sentry.StartTransaction(ctx, "processor.process_submission", sentry.WithOpName("task.processor.process"), sentry.WithTransactionSource(sentry.SourceCustom))
			ctx = span.Context()
			span.SetData("eyrie.monitor_id", request.MonitorID)
			span.SetData("eyrie.probe_type", request.ProbeType)

			monitor, ok := w.findMonitor(request.MonitorID)
			if !ok {
				slog.ErrorContext(ctx, "monitor not found for submission", slog.String("monitor_id", request.MonitorID))
				message.Ack()
				span.Finish()
				cancel()
				continue
			}

			lookbackSince := time.Now().Add(-time.Duration(w.datasetConfig.ProcessingLookbackMinutes) * time.Minute)
			historicalSubmissions, err := w.lookback(ctx, request.MonitorID, lookbackSince)
			if err != nil {
				slog.ErrorContext(ctx, "looking back historical submissions", slog.String("error", err.Error()))
				message.Ack()
				span.Finish()
				cancel()
				continue
			}

			buckets, earliestTime, latestTime := w.groupSubmissionByMinute(historicalSubmissions, time.Minute, monitor)
			evaluation := w.evaluateLatestIncident(buckets, latestTime, earliestTime, time.Minute)

			previous, err := w.loadIncidentState(ctx, request.MonitorID)
			if err != nil {
				slog.ErrorContext(ctx, "loading incident state", slog.String("error", err.Error()))
				message.Ack()
				span.Finish()
				cancel()
				continue
			}

			if !previous.Equal(evaluation) {
				if err := w.storeIncidentState(ctx, request.MonitorID, evaluation); err != nil {
					slog.ErrorContext(ctx, "storing incident state", slog.String("error", err.Error()))
					message.Ack()
					span.Finish()
					cancel()
					continue
				}

				if err := w.syncProgrammaticIncident(ctx, monitor, evaluation); err != nil {
					slog.ErrorContext(ctx, "syncing programmatic incident", slog.String("error", err.Error()))
					message.Ack()
					span.Finish()
					cancel()
					continue
				}

				if err := w.publishAlert(ctx, monitor, evaluation, previous); err != nil {
					slog.ErrorContext(ctx, "publishing alert", slog.String("error", err.Error()))
					message.Ack()
					span.Finish()
					cancel()
					continue
				}

				sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.incident.transitions", 1, sentry.WithAttributes(attribute.String("previous_status", previous.Status), attribute.String("next_status", evaluation.Status), attribute.String("next_scope", evaluation.Scope), attribute.String("probe_type", string(monitor.EffectiveType()))))
			}

			message.Ack()
			sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.processor.processing.duration", float64(time.Since(processingStartedAt).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", string(monitor.EffectiveType()))))
			span.Finish()
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

func (w *ProcessorWorker) syncProgrammaticIncident(ctx context.Context, monitor Monitor, evaluation IncidentEvaluation) error {
	activeIncident, found, err := w.loadActiveProgrammaticIncident(ctx, monitor.ID)
	if err != nil {
		return err
	}

	switch {
	case !evaluation.HasActiveIncident():
		if !found {
			return nil
		}
		return w.resolveProgrammaticIncident(ctx, activeIncident)
	case !found:
		return w.createProgrammaticIncident(ctx, monitor, evaluation)
	default:
		return w.updateProgrammaticIncident(ctx, activeIncident, monitor, evaluation)
	}
}

func (w *ProcessorWorker) loadActiveProgrammaticIncident(ctx context.Context, monitorID string) (MonitorIncident, bool, error) {
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return MonitorIncident{}, false, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	var incident MonitorIncident
	err = conn.QueryRowContext(ctx, `
		SELECT id, monitor_id, source, lifecycle_state, auto_impact, impact, auto_title, auto_body, title, body, status, scope, affected_regions, started_at, resolved_at, created_at, updated_at
		FROM monitor_incidents
		WHERE monitor_id = ? AND source = ? AND resolved_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1
	`, monitorID, IncidentSourceProgrammatic).Scan(
		&incident.ID,
		&incident.MonitorID,
		&incident.Source,
		&incident.LifecycleState,
		&incident.AutoImpact,
		&incident.Impact,
		&incident.AutoTitle,
		&incident.AutoBody,
		&incident.Title,
		&incident.Body,
		&incident.Status,
		&incident.Scope,
		&incident.AffectedRegions,
		&incident.StartedAt,
		&incident.ResolvedAt,
		&incident.CreatedAt,
		&incident.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorIncident{}, false, nil
		}
		return MonitorIncident{}, false, fmt.Errorf("querying active incident: %w", err)
	}

	return incident, true, nil
}

func (w *ProcessorWorker) createProgrammaticIncident(ctx context.Context, monitor Monitor, evaluation IncidentEvaluation) error {
	now := time.Now().UTC()
	incident := MonitorIncident{
		ID:              uuid.NewString(),
		MonitorID:       monitor.ID,
		Source:          IncidentSourceProgrammatic,
		LifecycleState:  IncidentLifecycleInvestigating,
		AutoImpact:      evaluation.Impact(),
		Impact:          evaluation.Impact(),
		AutoTitle:       evaluation.ProgrammaticTitle(monitor),
		AutoBody:        evaluation.Reason,
		Title:           evaluation.ProgrammaticTitle(monitor),
		Body:            evaluation.Reason,
		Status:          evaluation.Status,
		Scope:           evaluation.Scope,
		AffectedRegions: evaluation.RegionsJSON(),
		StartedAt:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("beginning incident transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO monitor_incidents (
			id, monitor_id, source, lifecycle_state, auto_impact, impact, auto_title, auto_body, title, body, status, scope, affected_regions, started_at, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, incident.ID, incident.MonitorID, incident.Source, incident.LifecycleState, incident.AutoImpact, incident.Impact, incident.AutoTitle, incident.AutoBody, incident.Title, incident.Body, incident.Status, incident.Scope, incident.AffectedRegions, incident.StartedAt, incident.CreatedAt, incident.UpdatedAt)
	if err != nil {
		return fmt.Errorf("inserting programmatic incident: %w", err)
	}

	if err := insertIncidentEventTx(ctx, tx, incident, IncidentEventTypeCreated, incident.Title, incident.Body, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing incident transaction: %w", err)
	}
	return nil
}

func (w *ProcessorWorker) updateProgrammaticIncident(ctx context.Context, incident MonitorIncident, monitor Monitor, evaluation IncidentEvaluation) error {
	autoImpact := evaluation.Impact()
	autoTitle := evaluation.ProgrammaticTitle(monitor)
	autoBody := evaluation.Reason
	affectedRegions := evaluation.RegionsJSON()

	updatedImpact := preserveManualString(incident.Impact, incident.AutoImpact, autoImpact)
	updatedTitle := preserveManualString(incident.Title, incident.AutoTitle, autoTitle)
	updatedBody := preserveManualString(incident.Body, incident.AutoBody, autoBody)
	lifecycleState := incident.LifecycleState
	if lifecycleState == "" {
		lifecycleState = IncidentLifecycleInvestigating
	}

	now := time.Now().UTC()
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("beginning incident transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE monitor_incidents
		SET
			auto_impact = ?,
			impact = ?,
			auto_title = ?,
			auto_body = ?,
			title = ?,
			body = ?,
			status = ?,
			scope = ?,
			affected_regions = ?,
			updated_at = ?
		WHERE id = ?
	`, autoImpact, updatedImpact, autoTitle, autoBody, updatedTitle, updatedBody, evaluation.Status, evaluation.Scope, affectedRegions, now, incident.ID)
	if err != nil {
		return fmt.Errorf("updating programmatic incident: %w", err)
	}

	incident.AutoImpact = autoImpact
	incident.Impact = updatedImpact
	incident.AutoTitle = autoTitle
	incident.AutoBody = autoBody
	incident.Title = updatedTitle
	incident.Body = updatedBody
	incident.Status = evaluation.Status
	incident.Scope = evaluation.Scope
	incident.AffectedRegions = affectedRegions
	incident.UpdatedAt = now
	incident.LifecycleState = lifecycleState

	if err := insertIncidentEventTx(ctx, tx, incident, IncidentEventTypeUpdated, updatedTitle, updatedBody, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing incident transaction: %w", err)
	}
	return nil
}

func (w *ProcessorWorker) resolveProgrammaticIncident(ctx context.Context, incident MonitorIncident) error {
	now := time.Now().UTC()
	resolutionBody := fmt.Sprintf("Monitor recovered from %s %s", incident.Scope, incident.Status)

	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("beginning incident transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE monitor_incidents
		SET
			lifecycle_state = ?,
			status = ?,
			scope = ?,
			resolved_at = ?,
			updated_at = ?
		WHERE id = ?
	`, IncidentLifecycleResolved, MonitorStatusHealthy, MonitorScopeHealthy, now, now, incident.ID)
	if err != nil {
		return fmt.Errorf("resolving programmatic incident: %w", err)
	}

	incident.LifecycleState = IncidentLifecycleResolved
	incident.Status = MonitorStatusHealthy
	incident.Scope = MonitorScopeHealthy
	incident.ResolvedAt = null.TimeFrom(now)
	incident.UpdatedAt = now

	if err := insertIncidentEventTx(ctx, tx, incident, IncidentEventTypeResolved, incident.Title, resolutionBody, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing incident transaction: %w", err)
	}
	return nil
}

func insertIncidentEventTx(ctx context.Context, tx *sql.Tx, incident MonitorIncident, eventType string, title string, body string, createdAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO monitor_incident_events (
			id, incident_id, monitor_id, event_type, source, lifecycle_state, impact, title, body, status, scope, affected_regions, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), incident.ID, incident.MonitorID, eventType, incident.Source, incident.LifecycleState, incident.Impact, title, body, incident.Status, incident.Scope, incident.AffectedRegions, createdAt)
	if err != nil {
		return fmt.Errorf("inserting incident event: %w", err)
	}
	return nil
}

func preserveManualString(current string, previousAuto string, nextAuto string) string {
	if current == "" || current == previousAuto {
		return nextAuto
	}
	return current
}
