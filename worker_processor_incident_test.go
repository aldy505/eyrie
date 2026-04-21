package main

import (
	"context"
	"testing"
	"time"
)

func TestProcessorWorker_SyncProgrammaticIncidentLifecycle(t *testing.T) {
	const monitorID = "incident-sync-monitor"

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("failed to get db connection: %v", err)
		}
		defer conn.Close()

		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_incident_events WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_incident_events: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_incidents WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_incidents: %v", err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM monitor_incident_state WHERE monitor_id = ?`, monitorID); err != nil {
			t.Fatalf("failed to clean up monitor_incident_state: %v", err)
		}
	})

	worker := &ProcessorWorker{db: db}
	monitor := Monitor{ID: monitorID, Name: "API Gateway"}

	initial := IncidentEvaluation{
		Status:          MonitorStatusDown,
		Scope:           MonitorScopeGlobal,
		Reason:          "Probe failures detected in all reporting regions (us-east-1, eu-west-1)",
		AffectedRegions: []string{"eu-west-1", "us-east-1"},
	}
	if err := worker.syncProgrammaticIncident(t.Context(), monitor, initial); err != nil {
		t.Fatalf("failed to create programmatic incident: %v", err)
	}

	incident, found, err := worker.loadActiveProgrammaticIncident(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to load active incident: %v", err)
	}
	if !found {
		t.Fatal("expected active programmatic incident to exist")
	}
	if incident.LifecycleState != IncidentLifecycleInvestigating {
		t.Fatalf("expected lifecycle_state=%q, got %q", IncidentLifecycleInvestigating, incident.LifecycleState)
	}
	if incident.Impact != IncidentImpactMajorOutage {
		t.Fatalf("expected impact=%q, got %q", IncidentImpactMajorOutage, incident.Impact)
	}
	if incident.Title != "API Gateway is experiencing a global outage" {
		t.Fatalf("unexpected incident title %q", incident.Title)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("failed to open verification connection: %v", err)
	}
	defer conn.Close()

	var createdEvents int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incident_events WHERE monitor_id = ? AND event_type = ?`, monitorID, IncidentEventTypeCreated).Scan(&createdEvents); err != nil {
		t.Fatalf("failed to count created events: %v", err)
	}
	if createdEvents != 1 {
		t.Fatalf("expected 1 created event, got %d", createdEvents)
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE monitor_incidents
		SET title = 'Investigating API Gateway outage',
			body = 'Support reported elevated errors while mitigation is in progress.',
			impact = ?
		WHERE id = ?
	`, IncidentImpactUnknown, incident.ID); err != nil {
		t.Fatalf("failed to simulate manual override: %v", err)
	}

	updated := IncidentEvaluation{
		Status:          MonitorStatusDegraded,
		Scope:           MonitorScopeLocal,
		Reason:          "Regional degradation detected in us-east-1",
		AffectedRegions: []string{"us-east-1"},
	}
	if err := worker.syncProgrammaticIncident(t.Context(), monitor, updated); err != nil {
		t.Fatalf("failed to update programmatic incident: %v", err)
	}

	incident, found, err = worker.loadActiveProgrammaticIncident(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to reload active incident: %v", err)
	}
	if !found {
		t.Fatal("expected incident to remain active after update")
	}
	if incident.AutoImpact != IncidentImpactDegradedPerformance {
		t.Fatalf("expected auto impact to update, got %q", incident.AutoImpact)
	}
	if incident.Impact != IncidentImpactUnknown {
		t.Fatalf("expected manual impact override to persist, got %q", incident.Impact)
	}
	if incident.Title != "Investigating API Gateway outage" {
		t.Fatalf("expected manual title override to persist, got %q", incident.Title)
	}
	if incident.Body != "Support reported elevated errors while mitigation is in progress." {
		t.Fatalf("expected manual body override to persist, got %q", incident.Body)
	}
	if incident.Status != MonitorStatusDegraded {
		t.Fatalf("expected status=%q, got %q", MonitorStatusDegraded, incident.Status)
	}
	if incident.Scope != MonitorScopeLocal {
		t.Fatalf("expected scope=%q, got %q", MonitorScopeLocal, incident.Scope)
	}

	var updatedEvents int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incident_events WHERE monitor_id = ? AND event_type = ?`, monitorID, IncidentEventTypeUpdated).Scan(&updatedEvents); err != nil {
		t.Fatalf("failed to count updated events: %v", err)
	}
	if updatedEvents != 1 {
		t.Fatalf("expected 1 updated event, got %d", updatedEvents)
	}

	if err := worker.syncProgrammaticIncident(t.Context(), monitor, HealthyIncidentEvaluation()); err != nil {
		t.Fatalf("failed to resolve programmatic incident: %v", err)
	}

	_, found, err = worker.loadActiveProgrammaticIncident(t.Context(), monitorID)
	if err != nil {
		t.Fatalf("failed to check active incident after resolution: %v", err)
	}
	if found {
		t.Fatal("expected no active programmatic incident after resolution")
	}

	var lifecycleState, status string
	if err := conn.QueryRowContext(ctx, `SELECT lifecycle_state, status FROM monitor_incidents WHERE id = ?`, incident.ID).Scan(&lifecycleState, &status); err != nil {
		t.Fatalf("failed to read resolved incident: %v", err)
	}
	if lifecycleState != IncidentLifecycleResolved {
		t.Fatalf("expected lifecycle_state=%q after resolution, got %q", IncidentLifecycleResolved, lifecycleState)
	}
	if status != MonitorStatusHealthy {
		t.Fatalf("expected status=%q after resolution, got %q", MonitorStatusHealthy, status)
	}

	var resolvedEvents int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_incident_events WHERE monitor_id = ? AND event_type = ?`, monitorID, IncidentEventTypeResolved).Scan(&resolvedEvents); err != nil {
		t.Fatalf("failed to count resolved events: %v", err)
	}
	if resolvedEvents != 1 {
		t.Fatalf("expected 1 resolved event, got %d", resolvedEvents)
	}
}
