package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestBufferedNtfyAlerter_SendsDownImmediately(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 60)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDown,
		Reason:    "Connection refused",
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 1 {
		t.Fatalf("expected 1 sent alert, got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_SuppressesRepeatDown(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 0)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDown,
		OccurredAt: time.Now(),
	})

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDown,
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 1 {
		t.Fatalf("expected 1 sent alert (repeat down suppressed), got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_SuppressesDegradedWhenDigestDisabled(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 0)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDegraded,
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 0 {
		t.Fatalf("expected 0 sent alerts (degraded suppressed when digest disabled), got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_FlushesDegradedOnHealthy(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 60)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID:  "monitor-1",
		Name:       "API Gateway",
		Status:     MonitorStatusDegraded,
		Reason:     "Latency spike",
		OccurredAt: time.Now(),
	})

	alerter.Send(context.Background(), AlertMessage{
		MonitorID:  "monitor-1",
		Name:       "API Gateway",
		Status:     MonitorStatusHealthy,
		Reason:     "Recovered",
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 1 {
		t.Fatalf("expected 1 digest flush on healthy, got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_HealthyDoesNotFlushOtherMonitor(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 60)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDegraded,
		OccurredAt: time.Now(),
	})

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-2",
		Name:      "Redis",
		Status:    MonitorStatusHealthy,
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 1 {
		t.Fatalf("expected 1 (healthy for other monitor flushes nothing), got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_DigestIntervalZeroSuppressesDegraded(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 0)
	defer alerter.Stop()

	for i := 0; i < 5; i++ {
		alerter.Send(context.Background(), AlertMessage{
			MonitorID: "monitor-1",
			Name:      "API Gateway",
			Status:    MonitorStatusDegraded,
			OccurredAt: time.Now(),
		})
	}

	mu.Lock()
	if sent != 0 {
		t.Fatalf("expected 0 sent (degraded suppressed with digest=0), got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_DownClearsDegradedBuffer(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 60)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDegraded,
		Reason:    "Latency spike",
		OccurredAt: time.Now(),
	})

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDown,
		Reason:    "Connection refused",
		OccurredAt: time.Now(),
	})

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusHealthy,
		Reason:    "Recovered",
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 2 {
		t.Fatalf("expected 2 (down sent, healthy skipped because buffer was cleared), got %d", sent)
	}
	mu.Unlock()
}

func TestBufferedNtfyAlerter_HealthyAfterHealthyPassesThrough(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 60)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusHealthy,
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 1 {
		t.Fatalf("expected 1 (healthy with no prior state passes through), got %d", sent)
	}
	mu.Unlock()
}
	func TestBufferedNtfyAlerter_DegradedAfterDownNotSent(t *testing.T) {
	var mu sync.Mutex
	var sent int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	wrapped := NtfyAlerter{topicURL: server.URL}
	alerter := NewBufferedNtfyAlerter(wrapped, 15, 60)
	defer alerter.Stop()

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDown,
		OccurredAt: time.Now(),
	})

	alerter.Send(context.Background(), AlertMessage{
		MonitorID: "monitor-1",
		Name:      "API Gateway",
		Status:    MonitorStatusDegraded,
		OccurredAt: time.Now(),
	})

	mu.Lock()
	if sent != 1 {
		t.Fatalf("expected 1 (degraded after down suppressed by suppression window), got %d", sent)
	}
	mu.Unlock()
}

func TestJoinUniqueRegions(t *testing.T) {
	alerts := []AlertMessage{
		{AffectedRegions: []string{"eu-west-1", "us-east-1"}},
		{AffectedRegions: []string{"us-east-1", "ap-southeast-1"}},
		{AffectedRegions: []string{"eu-west-1"}},
	}

	result := joinUniqueRegions(alerts)
	expected := "eu-west-1; us-east-1; ap-southeast-1"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestUniqueReasons(t *testing.T) {
	alerts := []AlertMessage{
		{Reason: "Latency spike"},
		{Reason: "Timeout"},
		{Reason: "Latency spike"},
		{Reason: ""},
	}

	result := uniqueReasons(alerts)
	expected := "Latency spike; Timeout"
	if result != expected {
		t.Fatalf("expected %q, got %q", expected, result)
	}
}

func TestBuildAlerters_NtfyWrappedWithBuffered(t *testing.T) {
	config := ServerConfig{}
	config.Alerting.Ntfy = []NtfyAlertingConfig{
		{Name: "ntfy-prod", Enabled: true, TopicURL: "https://ntfy.sh/eyrie-test", SuppressWindowMinutes: 10, DigestIntervalMinutes: 30},
		{Name: "ntfy-digest-disabled", Enabled: true, TopicURL: "https://ntfy.sh/eyrie-test-2", DigestIntervalMinutes: 0},
		{Name: "ntfy-disabled", Enabled: false, TopicURL: "https://ntfy.sh/eyrie-test-3"},
	}

	alerters := BuildAlerters(config)
	if len(alerters) != 2 {
		t.Fatalf("expected 2 enabled alerters, got %d", len(alerters))
	}

	if alerters[0].Name != "ntfy-prod" {
		t.Fatalf("unexpected first alerter name: %s", alerters[0].Name)
	}
	if alerters[1].Name != "ntfy-digest-disabled" {
		t.Fatalf("unexpected second alerter name: %s", alerters[1].Name)
	}

	_, ok := alerters[0].Alerter.(*BufferedNtfyAlerter)
	if !ok {
		t.Fatal("expected Ntfy alerter to be wrapped in BufferedNtfyAlerter")
	}

	_, ok = alerters[1].Alerter.(*BufferedNtfyAlerter)
	if !ok {
		t.Fatal("expected digest-disabled Ntfy alerter to still be wrapped in BufferedNtfyAlerter")
	}
}