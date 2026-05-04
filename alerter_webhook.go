package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

	requestBody, err := json.Marshal(alert)
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

type NtfyAlerter struct {
	topicURL    string
	accessToken string
	username    string
	password    string
}

func (a NtfyAlerter) Send(ctx context.Context, alert AlertMessage) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.topicURL, strings.NewReader(formatAlertMessage(alert)))
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "eyrie-ntfy/1.0")
	presentation := buildAlertPresentation(alert)
	request.Header.Set("Title", presentation.Title)
	request.Header.Set("Priority", presentation.Priority)
	request.Header.Set("Tags", strings.Join(presentation.Tags, ","))
	if a.accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+a.accessToken)
	}
	if a.username != "" || a.password != "" {
		request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(a.username+":"+a.password)))
	}
	return doAlertRequest(request)
}

func BuildAlerters(config ServerConfig) []Alerter {
	alerters := []Alerter{}
	if config.Alerting.Webhook.Enabled && config.Alerting.Webhook.URL != "" {
		alerters = append(alerters, NewWebhookAlerter(config.Alerting.Webhook.URL, config.Alerting.Webhook.HmacSecret, config.Alerting.Webhook.CustomHeaders))
	}
	if config.Alerting.Slack.Enabled && config.Alerting.Slack.WebhookURL != "" {
		alerters = append(alerters, JSONWebhookAlerter{
			url:         config.Alerting.Slack.WebhookURL,
			userAgent:   "eyrie-slack/1.0",
			contentType: "application/json",
			buildBody:   buildSlackBody,
		})
	}
	if config.Alerting.Discord.Enabled && config.Alerting.Discord.WebhookURL != "" {
		alerters = append(alerters, JSONWebhookAlerter{
			url:         config.Alerting.Discord.WebhookURL,
			userAgent:   "eyrie-discord/1.0",
			contentType: "application/json",
			buildBody:   buildDiscordBody,
		})
	}
	if config.Alerting.Teams.Enabled && config.Alerting.Teams.WebhookURL != "" {
		alerters = append(alerters, JSONWebhookAlerter{
			url:         config.Alerting.Teams.WebhookURL,
			userAgent:   "eyrie-teams/1.0",
			contentType: "application/json",
			buildBody:   buildTeamsBody,
		})
	}
	if config.Alerting.Ntfy.Enabled && config.Alerting.Ntfy.TopicURL != "" {
		alerters = append(alerters, NtfyAlerter{
			topicURL:    config.Alerting.Ntfy.TopicURL,
			accessToken: config.Alerting.Ntfy.AccessToken,
			username:    config.Alerting.Ntfy.Username,
			password:    config.Alerting.Ntfy.Password,
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
