package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildAlertPresentation(t *testing.T) {
	presentation := buildAlertPresentation(AlertMessage{
		Name:            "API Gateway",
		Status:          MonitorStatusDown,
		AffectedRegions: []string{"eu-west-1", "us-east-1"},
	})

	if presentation.Title != "[DOWN] API Gateway" {
		t.Fatalf("unexpected title %q", presentation.Title)
	}
	if presentation.Priority != "high" {
		t.Fatalf("unexpected priority %q", presentation.Priority)
	}
	if presentation.StatusIcon != "🟥" {
		t.Fatalf("unexpected status icon %q", presentation.StatusIcon)
	}
	expectedTags := []string{"red_square", "eu-west-1", "us-east-1"}
	if strings.Join(presentation.Tags, ",") != strings.Join(expectedTags, ",") {
		t.Fatalf("unexpected tags %v", presentation.Tags)
	}
}

func TestBuildWebhookPayload(t *testing.T) {
	occurredAt := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)

	t.Run("down global alert", func(t *testing.T) {
		alert := AlertMessage{
			MonitorID:       "monitor-1",
			Name:            "API Gateway",
			Status:          MonitorStatusDown,
			Scope:           MonitorScopeGlobal,
			Reason:          "Multi-region outage detected",
			OccurredAt:      occurredAt,
			AffectedRegions: []string{"eu-west-1", "us-east-1"},
		}

		payload := buildWebhookPayload(alert)

		if payload.Status != "firing" {
			t.Fatalf("expected status firing, got %q", payload.Status)
		}
		if payload.Title != "API Gateway is down" {
			t.Fatalf("unexpected title %q", payload.Title)
		}
		if !strings.Contains(payload.Message, "🟥 API Gateway is down at 2025-01-01 12:30:00") {
			t.Fatalf("unexpected message %q", payload.Message)
		}
		if !strings.Contains(payload.Message, "(affected regions: eu-west-1, us-east-1)") {
			t.Fatalf("expected affected regions in message, got %q", payload.Message)
		}
		if !strings.Contains(payload.Description, "Affected regions: eu-west-1, us-east-1.") {
			t.Fatalf("unexpected description %q", payload.Description)
		}
		if payload.Metadata.MonitorID != "monitor-1" {
			t.Fatalf("unexpected metadata monitor_id %q", payload.Metadata.MonitorID)
		}
		if payload.Metadata.Status != "down" {
			t.Fatalf("unexpected metadata status %q", payload.Metadata.Status)
		}
		if payload.Metadata.Name != "API Gateway" {
			t.Fatalf("unexpected metadata name %q", payload.Metadata.Name)
		}
		if strings.Join(payload.Metadata.AffectedRegions, ",") != "eu-west-1,us-east-1" {
			t.Fatalf("unexpected metadata affected_regions %v", payload.Metadata.AffectedRegions)
		}
		if payload.Metadata.OccurredAt != "2025-01-01T12:30:00Z" {
			t.Fatalf("unexpected metadata occurred_at %q", payload.Metadata.OccurredAt)
		}
	})

	t.Run("healthy resolved alert", func(t *testing.T) {
		alert := AlertMessage{
			MonitorID:       "monitor-2",
			Name:            "Auth Service",
			Status:          MonitorStatusHealthy,
			Scope:           MonitorScopeHealthy,
			OccurredAt:      occurredAt,
			AffectedRegions: []string{},
		}

		payload := buildWebhookPayload(alert)

		if payload.Status != "resolved" {
			t.Fatalf("expected status resolved, got %q", payload.Status)
		}
		if payload.Title != "Auth Service is healthy" {
			t.Fatalf("unexpected title %q", payload.Title)
		}
		if !strings.Contains(payload.Message, "🟩 Auth Service is healthy") {
			t.Fatalf("unexpected message %q", payload.Message)
		}
		if !strings.Contains(payload.Message, "(affected regions: none)") {
			t.Fatalf("expected none regions in message, got %q", payload.Message)
		}
		if payload.Metadata.Status != "healthy" {
			t.Fatalf("unexpected metadata status %q", payload.Metadata.Status)
		}
		if payload.Metadata.OccurredAt != "2025-01-01T12:30:00Z" {
			t.Fatalf("unexpected metadata occurred_at %q", payload.Metadata.OccurredAt)
		}
	})

	t.Run("degraded local alert without reason", func(t *testing.T) {
		alert := AlertMessage{
			MonitorID:       "monitor-3",
			Name:            "Billing Worker",
			Status:          MonitorStatusDegraded,
			Scope:           MonitorScopeLocal,
			OccurredAt:      occurredAt,
			AffectedRegions: []string{"us-east-1"},
		}

		payload := buildWebhookPayload(alert)

		if payload.Status != "firing" {
			t.Fatalf("expected status firing, got %q", payload.Status)
		}
		if payload.Title != "Billing Worker is degraded" {
			t.Fatalf("unexpected title %q", payload.Title)
		}
		if !strings.Contains(payload.Message, "🟨 Billing Worker is degraded") {
			t.Fatalf("unexpected message %q", payload.Message)
		}
		if !strings.Contains(payload.Description, "Affected regions: us-east-1.") {
			t.Fatalf("unexpected description %q", payload.Description)
		}
		if strings.Contains(payload.Description, "Additional context:") {
			t.Fatalf("description should not contain additional context when reason is empty, got %q", payload.Description)
		}
	})

	t.Run("zero occurred at falls back to now", func(t *testing.T) {
		alert := AlertMessage{
			MonitorID:       "monitor-4",
			Name:            "Cache Cluster",
			Status:          MonitorStatusDown,
			AffectedRegions: []string{"ap-southeast-1"},
		}

		payload := buildWebhookPayload(alert)

		if payload.Metadata.OccurredAt == "" {
			t.Fatalf("expected metadata occurred_at to be populated")
		}
		if _, err := time.Parse(time.RFC3339, payload.Metadata.OccurredAt); err != nil {
			t.Fatalf("metadata occurred_at is not RFC3339: %v", err)
		}
	})
}

func TestProviderPayloadBuilders(t *testing.T) {
	alert := AlertMessage{
		Name:            "API Gateway",
		Status:          MonitorStatusDegraded,
		Scope:           MonitorScopeLocal,
		Reason:          "Regional degradation detected in us-east-1",
		OccurredAt:      time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		AffectedRegions: []string{"us-east-1"},
	}

	t.Run("slack includes enriched text", func(t *testing.T) {
		body, err := buildSlackBody(alert)
		if err != nil {
			t.Fatalf("buildSlackBody returned error: %v", err)
		}

		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal slack payload: %v", err)
		}

		if !strings.Contains(payload["text"], "🟨 [DEGRADED] API Gateway") {
			t.Fatalf("expected enriched slack title, got %q", payload["text"])
		}
		if !strings.Contains(payload["text"], "2025-01-01 12:30:00") {
			t.Fatalf("expected human-readable timestamp, got %q", payload["text"])
		}
	})

	t.Run("discord includes enriched content", func(t *testing.T) {
		body, err := buildDiscordBody(alert)
		if err != nil {
			t.Fatalf("buildDiscordBody returned error: %v", err)
		}

		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal discord payload: %v", err)
		}

		if !strings.Contains(payload["content"], "🟨 [DEGRADED] API Gateway") {
			t.Fatalf("expected enriched discord title, got %q", payload["content"])
		}
	})

	t.Run("teams includes summary and enriched text", func(t *testing.T) {
		body, err := buildTeamsBody(alert)
		if err != nil {
			t.Fatalf("buildTeamsBody returned error: %v", err)
		}

		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal teams payload: %v", err)
		}

		if payload["summary"] != "[DEGRADED] API Gateway" {
			t.Fatalf("unexpected teams summary %q", payload["summary"])
		}
		if !strings.Contains(payload["text"], "🟨 [DEGRADED] API Gateway") {
			t.Fatalf("expected enriched teams text, got %q", payload["text"])
		}
	})
}

func TestBuildNtfyTitle(t *testing.T) {
	alert := AlertMessage{
		Name:   "API Gateway",
		Status: MonitorStatusDown,
	}

	if got := buildNtfyTitle(alert); got != "API Gateway" {
		t.Fatalf("unexpected ntfy title %q", got)
	}

	if got := buildNtfyTitle(AlertMessage{Status: MonitorStatusDown}); got != "[DOWN]" {
		t.Fatalf("expected fallback title when name is missing, got %q", got)
	}
}

func TestWebhookAlerterSendAddsParityHeaders(t *testing.T) {
	type capturedRequest struct {
		headers http.Header
		body    []byte
	}

	requests := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- capturedRequest{
			headers: r.Header.Clone(),
			body:    body,
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	alerter := NewWebhookAlerter(server.URL, "secret", map[string]string{"X-Custom": "value"})
	alert := AlertMessage{
		MonitorID:       "monitor-1",
		Name:            "API Gateway",
		Status:          MonitorStatusDown,
		Scope:           MonitorScopeGlobal,
		Reason:          "Multi-region outage detected across 2/2 regions (eu-west-1, us-east-1)",
		OccurredAt:      time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC),
		AffectedRegions: []string{"eu-west-1", "us-east-1"},
	}

	if err := alerter.Send(context.Background(), alert); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	request := <-requests
	if request.headers.Get("X-Eyrie-Alert-Title") != "[DOWN] API Gateway" {
		t.Fatalf("unexpected alert title header %q", request.headers.Get("X-Eyrie-Alert-Title"))
	}
	if request.headers.Get("X-Eyrie-Alert-Priority") != "high" {
		t.Fatalf("unexpected alert priority header %q", request.headers.Get("X-Eyrie-Alert-Priority"))
	}
	if request.headers.Get("X-Eyrie-Alert-Tags") != "red_square,eu-west-1,us-east-1" {
		t.Fatalf("unexpected alert tags header %q", request.headers.Get("X-Eyrie-Alert-Tags"))
	}
	if request.headers.Get("X-Custom") != "value" {
		t.Fatalf("expected custom header to be forwarded, got %q", request.headers.Get("X-Custom"))
	}

	expectedSignature := hmac.New(sha256.New, []byte("secret"))
	expectedSignature.Write(request.body)
	if request.headers.Get("X-Signature") != fmt.Sprintf("%x", expectedSignature.Sum(nil)) {
		t.Fatalf("unexpected signature header %q", request.headers.Get("X-Signature"))
	}

	var decoded WebhookAlertPayload
	if err := json.Unmarshal(request.body, &decoded); err != nil {
		t.Fatalf("failed to decode webhook body: %v", err)
	}
	if decoded.Metadata.MonitorID != alert.MonitorID || decoded.Metadata.Status != alert.Status {
		t.Fatalf("unexpected webhook body %+v", decoded)
	}
	if decoded.Status != "firing" {
		t.Fatalf("expected top-level status firing, got %q", decoded.Status)
	}
	if decoded.Title != "API Gateway is down" {
		t.Fatalf("unexpected title %q", decoded.Title)
	}
	if !strings.Contains(decoded.Message, "🟥 API Gateway is down at 2025-01-01 12:30:00") {
		t.Fatalf("unexpected message %q", decoded.Message)
	}
}

func TestBuildAlertersBuildsNamedEnabledDestinations(t *testing.T) {
	config := ServerConfig{}
	config.Alerting.Teams = []TeamsAlertingConfig{
		{Name: "team-ops", Enabled: true, WebhookURL: "https://example.com/teams"},
	}
	config.Alerting.Ntfy = []NtfyAlertingConfig{
		{Name: "team-auth", Enabled: true, TopicURL: "https://ntfy.sh/auth"},
		{Name: "team-billing", Enabled: false, TopicURL: "https://ntfy.sh/billing"},
	}

	alerters := BuildAlerters(config)
	if len(alerters) != 2 {
		t.Fatalf("expected 2 alerters, got %d", len(alerters))
	}

	gotNames := []string{alerters[0].Name, alerters[1].Name}
	expectedNames := []string{"team-ops", "team-auth"}
	if strings.Join(gotNames, ",") != strings.Join(expectedNames, ",") {
		t.Fatalf("unexpected alerter names %v", gotNames)
	}
}
