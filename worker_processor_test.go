package main_test

import (
	"testing"
	"time"
)

func TestEarliestLatestTimeTracking(t *testing.T) {
	type Submission struct {
		CreatedAt time.Time
	}

	submissions := []Submission{
		{CreatedAt: time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2025, 1, 1, 1, 1, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2025, 1, 1, 1, 4, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2025, 1, 1, 1, 2, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2025, 1, 1, 1, 5, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2025, 1, 1, 1, 3, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2025, 1, 1, 1, 6, 0, 0, time.UTC)},
	}
	earliestTime := time.Now().Add(100 * 365 * 24 * time.Hour)
	latestTime := time.Now()
	interval := time.Minute

	for _, submission := range submissions {
		bucketTime := submission.CreatedAt.Truncate(interval)

		// Track earliest and latest times: the first bucket that is earlier than
		// earliestTime initializes earliestTime, and later buckets can update
		// latestTime when they are after the current latestTime.
		if bucketTime.Before(earliestTime) {
			earliestTime = bucketTime
		}
		if bucketTime.After(latestTime) {
			latestTime = bucketTime
		}
	}

	t.Logf("Earliest time: %v", earliestTime)
	t.Logf("Latest time: %v", latestTime)

	if !earliestTime.Equal(time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("Expected earliest time to be 2025-01-01 01:00:00, got %v", earliestTime)
	}
	if !latestTime.Equal(time.Date(2025, 1, 1, 1, 6, 0, 0, time.UTC)) {
		t.Errorf("Expected latest time to be 2025-01-01 01:06:00, got %v", latestTime)
	}
}
