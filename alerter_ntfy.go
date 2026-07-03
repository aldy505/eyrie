package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
)

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
	request.Header.Set("Title", buildNtfyTitle(alert))
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

func buildNtfyTitle(alert AlertMessage) string {
	if alert.Name == "" {
		return strings.TrimSpace(buildAlertPresentation(alert).Title)
	}

	return alert.Name
}
