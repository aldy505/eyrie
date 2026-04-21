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
)

type WebhookAlerter struct {
	webhookURL    string
	hmacSecret    string
	customHeaders map[string]string
}

func NewWebhookAlerter(webhookURL, hmacSecret string, customHeaders map[string]string) *WebhookAlerter {
	return &WebhookAlerter{
		webhookURL:    webhookURL,
		hmacSecret:    hmacSecret,
		customHeaders: customHeaders,
	}
}

func (w *WebhookAlerter) Send(ctx context.Context, alert AlertMessage) error {
	span := startSentrySpan(ctx, "http.client", "Webhook Alerter Send")
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
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "eyrie-webhook/1.0")
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
	priority    int
}

func (a NtfyAlerter) Send(ctx context.Context, alert AlertMessage) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.topicURL, strings.NewReader(formatAlertMessage(alert)))
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "eyrie-ntfy/1.0")
	request.Header.Set("Title", fmt.Sprintf("[%s] %s", strings.ToUpper(alert.Status), alert.Name))
	request.Header.Set("Priority", fmt.Sprintf("%d", a.priority))
	request.Header.Set("Tags", strings.Join(alert.AffectedRegions, ","))
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
			buildBody: func(alert AlertMessage) ([]byte, error) {
				return json.Marshal(map[string]string{"text": formatAlertMessage(alert)})
			},
		})
	}
	if config.Alerting.Discord.Enabled && config.Alerting.Discord.WebhookURL != "" {
		alerters = append(alerters, JSONWebhookAlerter{
			url:         config.Alerting.Discord.WebhookURL,
			userAgent:   "eyrie-discord/1.0",
			contentType: "application/json",
			buildBody: func(alert AlertMessage) ([]byte, error) {
				return json.Marshal(map[string]string{"content": formatAlertMessage(alert)})
			},
		})
	}
	if config.Alerting.Teams.Enabled && config.Alerting.Teams.WebhookURL != "" {
		alerters = append(alerters, JSONWebhookAlerter{
			url:         config.Alerting.Teams.WebhookURL,
			userAgent:   "eyrie-teams/1.0",
			contentType: "application/json",
			buildBody: func(alert AlertMessage) ([]byte, error) {
				return json.Marshal(map[string]string{"text": formatAlertMessage(alert)})
			},
		})
	}
	if config.Alerting.Ntfy.Enabled && config.Alerting.Ntfy.TopicURL != "" {
		alerters = append(alerters, NtfyAlerter{
			topicURL:    config.Alerting.Ntfy.TopicURL,
			accessToken: config.Alerting.Ntfy.AccessToken,
			username:    config.Alerting.Ntfy.Username,
			password:    config.Alerting.Ntfy.Password,
			priority:    max(config.Alerting.Ntfy.Priority, 1),
		})
	}
	return alerters
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
		alert.OccurredAt.Format(time.RFC3339),
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
