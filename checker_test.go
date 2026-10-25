package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckerRegisterToUpstreamIncludesCheckerName(t *testing.T) {
	var receivedRequest CheckerRegistrationRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedRequest); err != nil {
			t.Fatalf("failed to decode registration request: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(MonitorConfig{}); err != nil {
			t.Fatalf("failed to encode registration response: %v", err)
		}
	}))
	defer server.Close()

	checker, err := NewChecker(CheckerOptions{
		CheckerConfig: CheckerConfig{
			UpstreamURL: server.URL,
			Name:        "public-east",
			Region:      "us-east-1",
			ApiKey:      "test-key",
		},
		HttpClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	if err := checker.registerToUpstream(t.Context()); err != nil {
		t.Fatalf("expected checker registration to succeed, got %v", err)
	}

	if receivedRequest.Name != "public-east" {
		t.Fatalf("expected checker name public-east, got %q", receivedRequest.Name)
	}
	if receivedRequest.Region != "us-east-1" {
		t.Fatalf("expected checker region us-east-1, got %q", receivedRequest.Region)
	}
}

func TestCheckerStartStopsDuringRegistrationRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	checker, err := NewChecker(CheckerOptions{
		CheckerConfig: CheckerConfig{
			UpstreamURL: server.URL,
			Region:      "us-east-1",
			ApiKey:      "test-key",
		},
		HttpClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("failed to create checker: %v", err)
	}

	startReturned := make(chan error, 1)
	go func() {
		startReturned <- checker.Start()
	}()

	// Give Start a moment to enter the retry wait.
	time.Sleep(50 * time.Millisecond)
	if err := checker.Stop(); err != nil {
		t.Fatalf("failed to stop checker: %v", err)
	}

	select {
	case err := <-startReturned:
		if err != nil {
			t.Fatalf("expected checker to stop cleanly, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("checker did not stop while waiting to retry registration")
	}
}
