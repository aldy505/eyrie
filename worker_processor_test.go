package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/guregu/null/v5"
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
	earliestTime := time.Now().UTC().Add(100 * 365 * 24 * time.Hour)
	latestTime := time.Now().UTC().Add(-100 * 365 * 24 * time.Hour)
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

func TestProcessorWorker_GroupSubmissionByMinute(t *testing.T) {
	processorWorker := &ProcessorWorker{
		db:              db,
		subscriber:      nil,
		alerterProducer: nil,
		shutdown:        make(chan struct{}),
		monitorConfig:   MonitorConfig{},
	}

	const monitorId = "1"

	const regionUSEast1 = "us-east-1"
	const regionUSWest1 = "us-west-1"
	const regionCanada = "ca-central-1"
	const regionEUWest1 = "eu-west-1"
	const regionEUCentral1 = "eu-central-1"

	t.Run("Normal Circumstances", func(t *testing.T) {
		var baseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

		submissions := []MonitorHistorical{
			// First batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  10,
				CreatedAt:  baseTime,
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  30,
				CreatedAt:  baseTime,
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 200,
				LatencyMs:  60,
				CreatedAt:  baseTime,
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 200,
				LatencyMs:  320,
				CreatedAt:  baseTime.Add(time.Millisecond * 500),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 200,
				LatencyMs:  750,
				CreatedAt:  baseTime.Add(time.Millisecond * 800),
			},
			// Second batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  400,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 120),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  50,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 11),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 200,
				LatencyMs:  620,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 600),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Second * 20),
			},
			// Third batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  700,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Millisecond * 680),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  900,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Millisecond * 860),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 200,
				LatencyMs:  10000,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Second * 10),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Second * 20),
			},
			// Fourth batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  700,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Millisecond * 680),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  900,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Millisecond * 860),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Second * 20),
			},
		}
		interval := time.Minute * 1
		monitor := Monitor{
			ID:                  monitorId,
			Name:                "Test monitor",
			Interval:            "1m",
			Method:              "GET",
			SkipTLSVerify:       false,
			Url:                 "http://127.0.0.1/health",
			Headers:             map[string]string{},
			ExpectedStatusCodes: []int{200},
			TimeoutSeconds:      null.NewInt(20, true),
			JqAssertion:         null.String{},
		}

		bucket, earliestTime, latestTime := processorWorker.groupSubmissionByMinute(submissions, interval, monitor)

		if len(bucket) != 4 {
			t.Errorf("Expected 4 time buckets, got %d", len(bucket))
		} else {
			eq := reflect.DeepEqual(bucket, map[time.Time]SubmissionBucket{
				baseTime: {
					TimestampMinute: baseTime,
					Regions: map[string]bool{
						regionCanada:     true,
						regionEUWest1:    true,
						regionEUCentral1: true,
						regionUSEast1:    true,
						regionUSWest1:    true,
					},
					FailureCount: 0,
					TotalCount:   5,
				},
				baseTime.Add(time.Minute): {
					TimestampMinute: baseTime.Add(time.Minute),
					Regions: map[string]bool{
						regionCanada:     true,
						regionEUWest1:    false,
						regionEUCentral1: false,
						regionUSEast1:    true,
						regionUSWest1:    true,
					},
					FailureCount: 2,
					TotalCount:   5,
				},
				baseTime.Add(time.Minute * 2): {
					TimestampMinute: baseTime.Add(time.Minute * 2),
					Regions: map[string]bool{
						regionCanada:     true,
						regionEUWest1:    false,
						regionEUCentral1: false,
						regionUSEast1:    true,
						regionUSWest1:    true,
					},
					FailureCount: 2,
					TotalCount:   5,
				},
				baseTime.Add(time.Minute * 3): {
					TimestampMinute: baseTime.Add(time.Minute * 3),
					Regions: map[string]bool{
						regionCanada:     false,
						regionEUWest1:    false,
						regionEUCentral1: false,
						regionUSEast1:    true,
						regionUSWest1:    true,
					},
					FailureCount: 3,
					TotalCount:   5,
				},
			})
			if !eq {
				t.Errorf("Bucket contents do not match expected values.\nGot: %+v", bucket)
			}
		}

		expectedEarliest := baseTime
		if !earliestTime.Equal(expectedEarliest) {
			t.Errorf("Expected earliest time %v, got %v", expectedEarliest, earliestTime)
		}

		expectedLatest := baseTime.Add(time.Minute * 3)
		if !latestTime.Equal(expectedLatest) {
			t.Errorf("Expected latest time %v, got %v", expectedLatest, latestTime)
		}
	})

	t.Run("Multiple Region per Bucket", func(t *testing.T) {
		var baseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

		submissions := []MonitorHistorical{
			// First batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  10,
				CreatedAt:  baseTime,
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  30,
				CreatedAt:  baseTime,
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 200,
				LatencyMs:  60,
				CreatedAt:  baseTime,
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 200,
				LatencyMs:  320,
				CreatedAt:  baseTime.Add(time.Millisecond * 500),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 200,
				LatencyMs:  750,
				CreatedAt:  baseTime.Add(time.Millisecond * 800),
			},
			// Second batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  400,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 120),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  50,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 11),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 529,
				LatencyMs:  620,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 600),
			},
			// Whoops, double entry for Canada
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 200,
				LatencyMs:  60,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Millisecond * 900),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute).Add(time.Second * 20),
			},
			// Third batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  700,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Millisecond * 680),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  900,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Millisecond * 860),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 200,
				LatencyMs:  10000,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Second * 10),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 2).Add(time.Second * 20),
			},
			// Fourth batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  700,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Millisecond * 680),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  900,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Millisecond * 860),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 3).Add(time.Second * 20),
			},
			// Fifth batch
			{
				MonitorID:  monitorId,
				Region:     regionUSEast1,
				StatusCode: 200,
				LatencyMs:  700,
				CreatedAt:  baseTime.Add(time.Minute * 4).Add(time.Millisecond * 680),
			},
			{
				MonitorID:  monitorId,
				Region:     regionUSWest1,
				StatusCode: 200,
				LatencyMs:  900,
				CreatedAt:  baseTime.Add(time.Minute * 4).Add(time.Millisecond * 860),
			},
			{
				MonitorID:  monitorId,
				Region:     regionCanada,
				StatusCode: 500,
				LatencyMs:  20000,
				CreatedAt:  baseTime.Add(time.Minute * 4).Add(time.Second * 20),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUWest1,
				StatusCode: 200,
				LatencyMs:  14000,
				CreatedAt:  baseTime.Add(time.Minute * 4).Add(time.Second * 14),
			},
			{
				MonitorID:  monitorId,
				Region:     regionEUCentral1,
				StatusCode: 200,
				LatencyMs:  15000,
				CreatedAt:  baseTime.Add(time.Minute * 4).Add(time.Second * 15),
			},
		}
		interval := time.Minute * 3
		monitor := Monitor{
			ID:                  monitorId,
			Name:                "Test monitor",
			Interval:            "1m",
			Method:              "GET",
			SkipTLSVerify:       false,
			Url:                 "http://127.0.0.1/health",
			Headers:             map[string]string{},
			ExpectedStatusCodes: []int{200},
			TimeoutSeconds:      null.NewInt(20, true),
			JqAssertion:         null.String{},
		}

		bucket, earliestTime, latestTime := processorWorker.groupSubmissionByMinute(submissions, interval, monitor)

		if len(bucket) != 2 {
			t.Errorf("Expected 2 time buckets, got %d", len(bucket))
		} else {
			eq := reflect.DeepEqual(bucket, map[time.Time]SubmissionBucket{
				baseTime: {
					TimestampMinute: baseTime,
					Regions: map[string]bool{
						regionCanada:     true,
						regionEUWest1:    false,
						regionEUCentral1: false,
						regionUSEast1:    true,
						regionUSWest1:    true,
					},
					FailureCount: 2,
					TotalCount:   5,
				},
				baseTime.Add(time.Minute * 3): {
					TimestampMinute: baseTime.Add(time.Minute * 3),
					Regions: map[string]bool{
						regionCanada:     false,
						regionEUWest1:    false,
						regionEUCentral1: false,
						regionUSEast1:    true,
						regionUSWest1:    true,
					},
					FailureCount: 3,
					TotalCount:   5,
				},
			})
			if !eq {
				t.Errorf("Bucket contents do not match expected values.\nGot: %+v", bucket)
			}
		}

		expectedEarliest := baseTime
		if !earliestTime.Equal(expectedEarliest) {
			t.Errorf("Expected earliest time %v, got %v", expectedEarliest, earliestTime)
		}

		expectedLatest := baseTime.Add(time.Minute * 3)
		if !latestTime.Equal(expectedLatest) {
			t.Errorf("Expected latest time %v, got %v", expectedLatest, latestTime)
		}
	})
}
