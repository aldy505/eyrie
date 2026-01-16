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
	"time"

	"github.com/getsentry/sentry-go"
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

type webhookRequestPayload struct {
	Message string `json:"message"`
}

func (w *WebhookAlerter) Send(ctx context.Context, monitor Monitor, reason string, occurredAt time.Time) error {
	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Webhook Alerter Send"))
	ctx = span.Context()
	defer span.Finish()

	requestBody, err := json.Marshal(webhookRequestPayload{
		Message: fmt.Sprintf("Alert for monitor '%s': %s at %s", monitor.Name, reason, occurredAt.Format(time.RFC3339)),
	})
	if err != nil {
		return err
	}

	var signature string
	if w.hmacSecret != "" {
		signer := hmac.New(sha256.New, []byte(w.hmacSecret))
		signer.Write(requestBody)
		signature = fmt.Sprintf("%x", signer.Sum(nil))
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
	if signature != "" {
		request.Header.Set("X-Signature", signature)
	}

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
