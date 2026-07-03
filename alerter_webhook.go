package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

type WebhookAlerter struct {
	webhookURL    string
	hmacSecret    string
	customHeaders map[string]string
}

type AlertPresentation struct {
	Title      string
	Priority   string
	StatusIcon string
	Tags       []string
}

type WebhookAlertPayload struct {
	Message     string                `json:"message"`
	Text        string                `json:"text"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Status      string                `json:"status"`
	Metadata    WebhookAlertMetadata  `json:"metadata"`
}

type WebhookAlertMetadata struct {
	MonitorID       string   `json:"monitor_id"`
	Status          string   `json:"status"`
	Name            string   `json:"name"`
	AffectedRegions []string `json:"affected_regions"`
	OccurredAt      string   `json:"occurred_at"`
}

func NewWebhookAlerter(webhookURL, hmacSecret string, customHeaders map[string]string) *WebhookAlerter {
	return &WebhookAlerter{
		webhookURL:    webhookURL,
		hmacSecret:    hmacSecret,
		customHeaders: customHeaders,
	}
}

func (w *WebhookAlerter) Send(ctx context.Context, alert AlertMessage) error {
	span := sentry.StartSpan(ctx, "http.client", sentry.WithDescription("Webhook Alerter Send"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()

	payload := buildWebhookPayload(alert)
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.webhookURL, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	presentation := buildAlertPresentation(alert)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "eyrie-webhook/1.0")
	request.Header.Set("X-Eyrie-Alert-Title", presentation.Title)
	request.Header.Set("X-Eyrie-Alert-Priority", presentation.Priority)
	request.Header.Set("X-Eyrie-Alert-Tags", strings.Join(presentation.Tags, ","))
	for key, value := range w.customHeaders {
		request.Header.Set(key, value)
	}
	if w.hmacSecret != "" {
		signer := hmac.New(sha256.New, []byte(w.hmacSecret))
		signer.Write(requestBody)
		request.Header.Set("X-Signature", fmt.Sprintf("%x", signer.Sum(nil)))
	}

	return doAlertRequest(request)
}

type JSONWebhookAlerter struct {
	url         string
	userAgent   string
	contentType string
	buildBody   func(AlertMessage) ([]byte, error)
}

func (a JSONWebhookAlerter) Send(ctx context.Context, alert AlertMessage) error {
	body, err := a.buildBody(alert)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", a.contentType)
	request.Header.Set("User-Agent", a.userAgent)

	return doAlertRequest(request)
}

func BuildAlerters(config ServerConfig) []NamedAlerter {
	alerters := []NamedAlerter{}
	for _, alert := range config.Alerting.Webhook {
		if !alert.Enabled || alert.URL == "" {
			continue
		}
		alerters = append(alerters, NamedAlerter{
			Name: strings.TrimSpace(alert.Name),
			Alerter: NewWebhookAlerter(
				alert.URL,
				alert.HmacSecret,
				alert.CustomHeaders,
			),
		})
	}
	for _, alert := range config.Alerting.Slack {
		if !alert.Enabled || alert.WebhookURL == "" {
			continue
		}
		alerters = append(alerters, NamedAlerter{
			Name: strings.TrimSpace(alert.Name),
			Alerter: JSONWebhookAlerter{
				url:         alert.WebhookURL,
				userAgent:   "eyrie-slack/1.0",
				contentType: "application/json",
				buildBody:   buildSlackBody,
			},
		})
	}
	for _, alert := range config.Alerting.Discord {
		if !alert.Enabled || alert.WebhookURL == "" {
			continue
		}
		alerters = append(alerters, NamedAlerter{
			Name: strings.TrimSpace(alert.Name),
			Alerter: JSONWebhookAlerter{
				url:         alert.WebhookURL,
				userAgent:   "eyrie-discord/1.0",
				contentType: "application/json",
				buildBody:   buildDiscordBody,
			},
		})
	}
	for _, alert := range config.Alerting.Teams {
		if !alert.Enabled || alert.WebhookURL == "" {
			continue
		}
		alerters = append(alerters, NamedAlerter{
			Name: strings.TrimSpace(alert.Name),
			Alerter: JSONWebhookAlerter{
				url:         alert.WebhookURL,
				userAgent:   "eyrie-teams/1.0",
				contentType: "application/json",
				buildBody:   buildTeamsBody,
			},
		})
	}
	for _, alert := range config.Alerting.Ntfy {
		if !alert.Enabled || alert.TopicURL == "" {
			continue
		}
		wrapped := NtfyAlerter{
			topicURL:    alert.TopicURL,
			accessToken: alert.AccessToken,
			username:    alert.Username,
			password:    alert.Password,
		}
		suppressWindow := alert.SuppressWindowMinutes
		if suppressWindow <= 0 {
			suppressWindow = 15
		}
		digestInterval := alert.DigestIntervalMinutes
		alerters = append(alerters, NamedAlerter{
			Name:    strings.TrimSpace(alert.Name),
			Alerter: NewBufferedNtfyAlerter(wrapped, suppressWindow, digestInterval),
		})
	}
	return alerters
}

func buildAlertPresentation(alert AlertMessage) AlertPresentation {
	status := "healthy"
	if alert.Status != "" {
		status = strings.ToLower(alert.Status)
	}

	presentation := AlertPresentation{
		Title:    fmt.Sprintf("[%s] %s", strings.ToUpper(status), alert.Name),
		Priority: "default",
	}

	switch status {
	case MonitorStatusHealthy:
		presentation.Priority = "low"
		presentation.StatusIcon = "🟩"
		presentation.Tags = append(presentation.Tags, "green_square")
	case MonitorStatusDegraded:
		presentation.Priority = "default"
		presentation.StatusIcon = "🟨"
		presentation.Tags = append(presentation.Tags, "yellow_square")
	case MonitorStatusDown:
		presentation.Priority = "high"
		presentation.StatusIcon = "🟥"
		presentation.Tags = append(presentation.Tags, "red_square")
	}

	if len(alert.AffectedRegions) > 0 {
		presentation.Tags = append(presentation.Tags, alert.AffectedRegions...)
	}

	return presentation
}

func buildWebhookPayload(alert AlertMessage) WebhookAlertPayload {
	presentation := buildAlertPresentation(alert)

	status := strings.ToLower(alert.Status)
	if status == "" {
		status = MonitorStatusHealthy
	}

	alertState := "firing"
	if status == MonitorStatusHealthy {
		alertState = "resolved"
	}

	occurredAt := alert.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	regionsLabel := "none"
	if len(alert.AffectedRegions) > 0 {
		regionsLabel = strings.Join(alert.AffectedRegions, ", ")
	}

	message := fmt.Sprintf("%s %s is %s at %s (affected regions: %s)",
		presentation.StatusIcon,
		alert.Name,
		status,
		occurredAt.Format(time.DateTime),
		regionsLabel,
	)
	if alert.Reason != "" {
		message = fmt.Sprintf("%s %s", message, alert.Reason)
	}

	description := fmt.Sprintf("Monitor %s is currently %s. Affected regions: %s.",
		alert.Name,
		status,
		regionsLabel,
	)
	if alert.Reason != "" {
		description = fmt.Sprintf("%s Additional context: %s", description, alert.Reason)
	}

	return WebhookAlertPayload{
		Message:     message,
		Text:        message,
		Title:       fmt.Sprintf("%s is %s", alert.Name, status),
		Description: description,
		Status:      alertState,
		Metadata: WebhookAlertMetadata{
			MonitorID:       alert.MonitorID,
			Status:          status,
			Name:            alert.Name,
			AffectedRegions: alert.AffectedRegions,
			OccurredAt:      occurredAt.Format(time.RFC3339),
		},
	}
}

func formatProviderAlertMessage(alert AlertMessage) string {
	presentation := buildAlertPresentation(alert)
	if presentation.StatusIcon == "" {
		return fmt.Sprintf("%s\n%s", presentation.Title, formatAlertMessage(alert))
	}

	return fmt.Sprintf("%s %s\n%s", presentation.StatusIcon, presentation.Title, formatAlertMessage(alert))
}

func buildSlackBody(alert AlertMessage) ([]byte, error) {
	return json.Marshal(map[string]string{"text": formatProviderAlertMessage(alert)})
}

func buildDiscordBody(alert AlertMessage) ([]byte, error) {
	return json.Marshal(map[string]string{"content": formatProviderAlertMessage(alert)})
}

func buildTeamsBody(alert AlertMessage) ([]byte, error) {
	presentation := buildAlertPresentation(alert)
	return json.Marshal(map[string]string{
		"summary": presentation.Title,
		"text":    formatProviderAlertMessage(alert),
	})
}

func formatAlertMessage(alert AlertMessage) string {
	scope := "healthy"
	if alert.Scope != "" {
		scope = alert.Scope
	}
	status := "healthy"
	if alert.Status != "" {
		status = alert.Status
	}
	regions := "none"
	if len(alert.AffectedRegions) > 0 {
		regions = strings.Join(alert.AffectedRegions, ", ")
	}

	return fmt.Sprintf("%s is %s (%s) at %s. Regions: %s. %s",
		alert.Name,
		status,
		scope,
		alert.OccurredAt.Format(time.DateTime),
		regions,
		alert.Reason,
	)
}

func doAlertRequest(request *http.Request) error {
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		if response.Body != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	if response.StatusCode == http.StatusTooManyRequests {
		return ErrAlerterRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%w: received non-2xx response code %d", ErrAlerterDropped, response.StatusCode)
	}
	return nil
}
