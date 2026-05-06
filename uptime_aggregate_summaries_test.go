package main

import (
	"testing"
	"time"
)

func TestAggregateCacheKeyIsDayAware(t *testing.T) {
	day1 := time.Date(2025, 1, 15, 8, 30, 0, 0, time.UTC)
	day1Later := day1.Add(10 * time.Hour)
	day2 := day1.Add(24 * time.Hour)

	key1 := aggregateCacheKey("monitor-1", day1)
	key2 := aggregateCacheKey("monitor-1", day1Later)
	key3 := aggregateCacheKey("monitor-1", day2)

	if key1 != key2 {
		t.Fatalf("expected same-day keys to match, got %q and %q", key1, key2)
	}
	if key1 == key3 {
		t.Fatalf("expected different-day keys to differ, got %q", key1)
	}
}

func TestSummarizeUptimeRegionHistoricalRowsUsesMonitorSuccessSemantics(t *testing.T) {
	monitor := Monitor{
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			URL:                 "https://example.com",
			ExpectedStatusCodes: []int{200},
		},
	}

	createdAt := utcDayStart(time.Now().UTC()).Add(12 * time.Hour)
	regionData := map[string][]MonitorHistorical{
		"us-east-1": {
			{
				StatusCode: 200,
				Success:    false,
				LatencyMs:  100,
				CreatedAt:  createdAt,
			},
			{
				StatusCode: 500,
				Success:    false,
				LatencyMs:  300,
				CreatedAt:  createdAt.Add(10 * time.Minute),
			},
		},
	}

	monitors := summarizeUptimeRegionHistoricalRows(regionData, monitor)
	if len(monitors) != 1 {
		t.Fatalf("expected 1 region summary, got %d", len(monitors))
	}

	summary := monitors[0]
	if summary.Age != 1 {
		t.Fatalf("expected age 1, got %d", summary.Age)
	}
	if got := summary.Downtimes[0].DurationMinutes; got != 1 {
		t.Fatalf("expected downtime minutes to reflect HTTP success semantics, got %d", got)
	}
}

func TestSummarizeUptimeRegionHistoricalRowsUsesPredicate(t *testing.T) {
	createdAt := utcDayStart(time.Now().UTC()).Add(12 * time.Hour)
	regionData := map[string][]MonitorHistorical{
		"us-west-2": {
			{
				StatusCode: 418,
				Success:    false,
				LatencyMs:  100,
				CreatedAt:  createdAt,
			},
			{
				StatusCode: 503,
				Success:    false,
				LatencyMs:  300,
				CreatedAt:  createdAt.Add(10 * time.Minute),
			},
		},
	}

	monitors := summarizeUptimeRegionHistoricalRows(regionData, successPredicate(func(statusCode int, explicitSuccess bool) bool {
		return statusCode == 418 || explicitSuccess
	}))
	if len(monitors) != 1 {
		t.Fatalf("expected 1 region summary, got %d", len(monitors))
	}

	summary := monitors[0]
	if got := summary.Downtimes[0].DurationMinutes; got != 1 {
		t.Fatalf("expected downtime minutes to respect the predicate, got %d", got)
	}
}
