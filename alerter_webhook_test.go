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

	var decoded AlertMessage
	if err := json.Unmarshal(request.body, &decoded); err != nil {
		t.Fatalf("failed to decode webhook body: %v", err)
	}
	if decoded.MonitorID != alert.MonitorID || decoded.Status != alert.Status {
		t.Fatalf("unexpected webhook body %+v", decoded)
	}
}
