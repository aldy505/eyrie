package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const monitorIncidentSelectColumns = `
	id, monitor_id, source, lifecycle_state, auto_impact, impact, auto_title, auto_body,
	title, body, status, scope, affected_regions, started_at, resolved_at, created_at, updated_at
`

type RetentionStats struct {
	PartitionsCreated int
	PartitionsDropped int
	IncidentsDeleted  int64
	EventsDeleted     int64
}

func scanMonitorIncident(row pgx.Row) (MonitorIncident, error) {
	var incident MonitorIncident
	err := row.Scan(
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
	return incident, err
}

func (s *PostgresStore) FetchIncidentState(ctx context.Context, monitorID string) (MonitorIncidentState, bool, error) {
	var state MonitorIncidentState
	err := s.pool.QueryRow(ctx, `
		SELECT monitor_id, status, scope, affected_regions, reason,
			COALESCE(failure_reasons_json, '{}'), last_transition_at, updated_at
		FROM monitor_incident_state
		WHERE monitor_id = $1
	`, monitorID).Scan(
		&state.MonitorID,
		&state.Status,
		&state.Scope,
		&state.AffectedRegions,
		&state.Reason,
		&state.FailureReasonsJson,
		&state.LastTransitionAt,
		&state.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MonitorIncidentState{}, false, nil
		}
		return MonitorIncidentState{}, false, fmt.Errorf("querying incident state: %w", err)
	}
	return state, true, nil
}

func (s *PostgresStore) StoreIncidentState(ctx context.Context, monitorID string, evaluation IncidentEvaluation) error {
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO monitor_incident_state (
			monitor_id, status, scope, affected_regions, reason, failure_reasons_json,
			last_transition_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (monitor_id) DO UPDATE SET
			status = EXCLUDED.status,
			scope = EXCLUDED.scope,
			affected_regions = EXCLUDED.affected_regions,
			reason = EXCLUDED.reason,
			failure_reasons_json = EXCLUDED.failure_reasons_json,
			last_transition_at = EXCLUDED.last_transition_at,
			updated_at = EXCLUDED.updated_at
	`, monitorID, evaluation.Status, evaluation.Scope, evaluation.RegionsJSON(), evaluation.Reason, evaluation.FailureReasonsJSON(), now, now)
	if err != nil {
		return fmt.Errorf("upserting incident state: %w", err)
	}
	return nil
}

func (s *PostgresStore) FetchActiveIncident(ctx context.Context, monitorID string) (MonitorIncident, bool, error) {
	return s.loadActiveIncident(ctx, monitorID, "")
}

func (s *PostgresStore) LoadActiveProgrammaticIncident(ctx context.Context, monitorID string) (MonitorIncident, bool, error) {
	return s.loadActiveIncident(ctx, monitorID, IncidentSourceProgrammatic)
}

func (s *PostgresStore) loadActiveIncident(ctx context.Context, monitorID string, source string) (MonitorIncident, bool, error) {
	query := `SELECT ` + monitorIncidentSelectColumns + `
		FROM monitor_incidents
		WHERE monitor_id = $1 AND resolved_at IS NULL`
	args := []any{monitorID}
	if source != "" {
		query += ` AND source = $2`
		args = append(args, source)
	}
	query += ` ORDER BY started_at DESC LIMIT 1`

	incident, err := scanMonitorIncident(s.pool.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MonitorIncident{}, false, nil
		}
		return MonitorIncident{}, false, fmt.Errorf("querying active incident: %w", err)
	}
	return incident, true, nil
}

func (s *PostgresStore) RecentIncidentEvents(ctx context.Context, limit int) ([]MonitorIncidentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, incident_id, monitor_id, event_type, source, lifecycle_state, impact,
			title, body, status, scope, affected_regions, created_at
		FROM monitor_incident_events
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent incident events: %w", err)
	}
	defer rows.Close()

	events := make([]MonitorIncidentEvent, 0, limit)
	for rows.Next() {
		var event MonitorIncidentEvent
		if err := rows.Scan(
			&event.ID,
			&event.IncidentID,
			&event.MonitorID,
			&event.EventType,
			&event.Source,
			&event.LifecycleState,
			&event.Impact,
			&event.Title,
			&event.Body,
			&event.Status,
			&event.Scope,
			&event.AffectedRegions,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning recent incident event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recent incident events: %w", err)
	}
	return events, nil
}

func (s *PostgresStore) CreateProgrammaticIncident(ctx context.Context, incident MonitorIncident, failureReasonsJSON string, event MonitorIncidentEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning incident transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO monitor_incidents (
			id, monitor_id, source, lifecycle_state, auto_impact, impact, auto_title, auto_body,
			title, body, status, scope, affected_regions, failure_reasons_json,
			started_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, incident.ID, incident.MonitorID, incident.Source, incident.LifecycleState, incident.AutoImpact, incident.Impact, incident.AutoTitle, incident.AutoBody, incident.Title, incident.Body, incident.Status, incident.Scope, incident.AffectedRegions, failureReasonsJSON, incident.StartedAt, incident.CreatedAt, incident.UpdatedAt)
	if err != nil {
		return fmt.Errorf("inserting programmatic incident: %w", err)
	}

	if err := insertIncidentEvent(ctx, tx, event); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing incident transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) UpdateProgrammaticIncident(ctx context.Context, incident MonitorIncident, failureReasonsJSON string, event MonitorIncidentEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning incident transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE monitor_incidents
		SET
			auto_impact = $1,
			impact = $2,
			auto_title = $3,
			auto_body = $4,
			title = $5,
			body = $6,
			lifecycle_state = $7,
			status = $8,
			scope = $9,
			affected_regions = $10,
			failure_reasons_json = $11,
			updated_at = $12
		WHERE id = $13
	`, incident.AutoImpact, incident.Impact, incident.AutoTitle, incident.AutoBody, incident.Title, incident.Body, incident.LifecycleState, incident.Status, incident.Scope, incident.AffectedRegions, failureReasonsJSON, incident.UpdatedAt, incident.ID)
	if err != nil {
		return fmt.Errorf("updating programmatic incident: %w", err)
	}

	if err := insertIncidentEvent(ctx, tx, event); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing incident transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) ResolveProgrammaticIncident(ctx context.Context, incident MonitorIncident, event MonitorIncidentEvent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning incident transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE monitor_incidents
		SET
			lifecycle_state = $1,
			status = $2,
			scope = $3,
			resolved_at = $4,
			updated_at = $5
		WHERE id = $6
	`, incident.LifecycleState, incident.Status, incident.Scope, incident.ResolvedAt, incident.UpdatedAt, incident.ID)
	if err != nil {
		return fmt.Errorf("resolving programmatic incident: %w", err)
	}

	if err := insertIncidentEvent(ctx, tx, event); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing incident transaction: %w", err)
	}
	return nil
}

func insertIncidentEvent(ctx context.Context, tx pgx.Tx, event MonitorIncidentEvent) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO monitor_incident_events (
			id, incident_id, monitor_id, event_type, source, lifecycle_state, impact,
			title, body, status, scope, affected_regions, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, event.ID, event.IncidentID, event.MonitorID, event.EventType, event.Source, event.LifecycleState, event.Impact, event.Title, event.Body, event.Status, event.Scope, event.AffectedRegions, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting incident event: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteExpiredIncidents(ctx context.Context, cutoff time.Time) (int64, int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("beginning incident retention transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	eventsTag, err := tx.Exec(ctx, `
		DELETE FROM monitor_incident_events
		WHERE created_at < $1
		   OR incident_id IN (SELECT id FROM monitor_incidents WHERE started_at < $1)
	`, cutoff.UTC())
	if err != nil {
		return 0, 0, fmt.Errorf("deleting expired incident events: %w", err)
	}

	incidentsTag, err := tx.Exec(ctx, `
		DELETE FROM monitor_incidents
		WHERE started_at < $1
	`, cutoff.UTC())
	if err != nil {
		return 0, 0, fmt.Errorf("deleting expired incidents: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("committing incident retention transaction: %w", err)
	}
	return incidentsTag.RowsAffected(), eventsTag.RowsAffected(), nil
}

func (s *PostgresStore) RunRetention(ctx context.Context, now time.Time, retentionDays int, lookaheadDays int) (RetentionStats, error) {
	var stats RetentionStats

	created, err := s.EnsurePartitions(ctx, now, lookaheadDays)
	if err != nil {
		return stats, fmt.Errorf("ensuring partitions: %w", err)
	}
	stats.PartitionsCreated = created

	dropped, err := s.DropExpiredPartitions(ctx, now, retentionDays)
	if err != nil {
		return stats, fmt.Errorf("dropping expired partitions: %w", err)
	}
	stats.PartitionsDropped = dropped

	cutoff := now.UTC().AddDate(0, 0, -retentionDays)
	incidents, events, err := s.DeleteExpiredIncidents(ctx, cutoff)
	if err != nil {
		return stats, fmt.Errorf("deleting expired incidents: %w", err)
	}
	stats.IncidentsDeleted = incidents
	stats.EventsDeleted = events

	return stats, nil
}

func (s *PostgresStore) DatabaseSizeBytes(ctx context.Context) (int64, error) {
	var size int64
	if err := s.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&size); err != nil {
		return 0, fmt.Errorf("querying database size: %w", err)
	}
	return size, nil
}
