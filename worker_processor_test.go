package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/guregu/null/v5"
)

// defaultTestDatasetConfig returns a DatasetConfig with default threshold values for tests
func defaultTestDatasetConfig() DatasetConfig {
	return DatasetConfig{
		PerRegionFailureThresholdPercent:    40.0,
		FailureThresholdPercent:             50.0,
		DegradedThresholdMinutes:            5,
		DegradedThresholdConsecutiveBuckets: 5,
	}
}

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
		datasetConfig:   defaultTestDatasetConfig(),
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

func TestProcessorWorker_AnalyzeSubmissions(t *testing.T) {
	processorWorker := &ProcessorWorker{
		db:              db,
		subscriber:      nil,
		alerterProducer: nil,
		shutdown:        make(chan struct{}),
		monitorConfig:   MonitorConfig{},
		datasetConfig:   defaultTestDatasetConfig(),
	}

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	minuteInterval := time.Minute

	t.Run("Invalid time range - earliestTime after latestTime", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{}
		latestTime := baseTime
		earliestTime := baseTime.Add(time.Minute * 5) // earliestTime is AFTER latestTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for invalid time range, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Invalid minute interval - zero value", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{}
		latestTime := baseTime.Add(time.Minute * 5)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, 0)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for zero minute interval, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Invalid minute interval - negative value", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{}
		latestTime := baseTime.Add(time.Minute * 5)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, -time.Minute)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for negative minute interval, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Empty buckets - no data analyzed", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{}
		latestTime := baseTime.Add(time.Minute * 5)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for empty buckets, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Bucket with TotalCount = 0", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{},
				FailureCount:    0,
				TotalCount:      0, // Zero total count should be skipped
			},
		}
		latestTime := baseTime
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for bucket with TotalCount=0, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Missing bucket in time range", func(t *testing.T) {
		// Only buckets at baseTime and baseTime+2min exist, baseTime+1min is missing
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
			baseTime.Add(time.Minute * 2): {
				TimestampMinute: baseTime.Add(time.Minute * 2),
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
		}
		latestTime := baseTime.Add(time.Minute * 2)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for healthy state, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("StateHealthy - latest and previous both healthy - no alert", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert when both states are healthy, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("StateHealthy - recovery alert when previous was multi-region unhealthy", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false}, // Multi-region failure
				FailureCount:    2,
				TotalCount:      2,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true}, // Healthy now
				FailureCount:    0,
				TotalCount:      2,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected recovery alert, got shouldAlert=false")
		}
		if alertReason != "Monitor has recovered and is now healthy" {
			t.Errorf("Expected recovery alert reason, got %s", alertReason)
		}
	})

	t.Run("StateHealthy - recovery alert when previous was single-region unhealthy", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false}, // Single region failure with 100% failure rate
				FailureCount:    1,
				TotalCount:      1,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": true}, // Healthy now
				FailureCount:    0,
				TotalCount:      1,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected recovery alert, got shouldAlert=false")
		}
		if alertReason != "Monitor has recovered and is now healthy" {
			t.Errorf("Expected recovery alert reason, got %s", alertReason)
		}
	})

	t.Run("StateUnhealthyMultiRegion - trigger alert when previous was healthy", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true, "eu-west-1": true},
				FailureCount:    0,
				TotalCount:      3,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false, "eu-west-1": true}, // 2/3 failed = 66%
				FailureCount:    2,
				TotalCount:      3,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected alert for multi-region failure after healthy state, got shouldAlert=false")
		}
		if alertReason == "" {
			t.Errorf("Expected non-empty alert reason")
		}
		// Check that the alert reason contains expected content
		if !strings.Contains(alertReason, "High failure rate detected") {
			t.Errorf("Expected alert reason to contain 'High failure rate detected', got %s", alertReason)
		}
	})

	t.Run("StateUnhealthyMultiRegion - no alert when previous was also unhealthy", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false, "eu-west-1": true}, // Already unhealthy
				FailureCount:    2,
				TotalCount:      3,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false, "eu-west-1": true}, // Still unhealthy
				FailureCount:    2,
				TotalCount:      3,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert when previous state was already unhealthy, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("StateUnhealthySingleRegion - no alert on first occurrence", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true}, // Was healthy
				FailureCount:    0,
				TotalCount:      1,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false}, // Now single-region unhealthy
				FailureCount:    1,
				TotalCount:      1,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert on first occurrence of single-region failure, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("StateUnhealthySingleRegion - trigger alert on consecutive failures", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false}, // Single region unhealthy
				FailureCount:    1,
				TotalCount:      1,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false}, // Still single-region unhealthy (consecutive)
				FailureCount:    1,
				TotalCount:      1,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected alert for consecutive single-region failures, got shouldAlert=false")
		}
		if !strings.Contains(alertReason, "High failure rate detected") || !strings.Contains(alertReason, "single region") {
			t.Errorf("Expected alert reason to contain 'High failure rate detected' and 'single region', got %s", alertReason)
		}
	})

	t.Run("StateUnhealthySingleRegion - no alert when previous was multi-region unhealthy", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false}, // Multi-region unhealthy
				FailureCount:    2,
				TotalCount:      2,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false}, // Now single-region unhealthy
				FailureCount:    1,
				TotalCount:      1,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert when transitioning from multi-region to single-region, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Single bucket analysis - healthy state with single data point", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
		}
		latestTime := baseTime
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for single healthy bucket, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Single bucket analysis - multi-region unhealthy with single data point (triggers alert)", func(t *testing.T) {
		// With only one bucket and previous state defaulting to healthy,
		// a multi-region failure should trigger an alert
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false},
				FailureCount:    2,
				TotalCount:      2,
			},
		}
		latestTime := baseTime
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected alert for multi-region failure when previous state defaults to healthy, got shouldAlert=false")
		}
		if !strings.Contains(alertReason, "High failure rate detected") {
			t.Errorf("Expected alert reason to contain 'High failure rate detected', got %s", alertReason)
		}
	})

	t.Run("Single bucket analysis - single-region unhealthy with single data point (no alert)", func(t *testing.T) {
		// With only one bucket and previous state defaulting to healthy,
		// a single-region failure should NOT trigger an alert (first occurrence)
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": false},
				FailureCount:    1,
				TotalCount:      1,
			},
		}
		latestTime := baseTime
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for single-region failure on first occurrence, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Failure rate below 50% should be healthy", func(t *testing.T) {
		// 1 out of 3 regions failed = 33% failure rate, should be StateHealthy
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true, "eu-west-1": true},
				FailureCount:    0,
				TotalCount:      3,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": true, "eu-west-1": true}, // 1/3 = 33%
				FailureCount:    1,
				TotalCount:      3,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert for failure rate below 50%%, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Failure rate exactly 50% should be unhealthy", func(t *testing.T) {
		// 2 out of 4 regions failed = 50% failure rate, should be StateUnhealthyMultiRegion
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true, "eu-west-1": true, "ap-south-1": true},
				FailureCount:    0,
				TotalCount:      4,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false, "eu-west-1": true, "ap-south-1": true}, // 2/4 = 50%
				FailureCount:    2,
				TotalCount:      4,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected alert for failure rate at 50%%, got shouldAlert=false")
		}
		if !strings.Contains(alertReason, "High failure rate detected") {
			t.Errorf("Expected alert reason to contain 'High failure rate detected', got %s", alertReason)
		}
	})

	t.Run("Multiple buckets with gap - only latest and earliest matter for state transition", func(t *testing.T) {
		// Latest bucket (minute 3) is healthy, previous bucket (minute 2) is unhealthy
		// Should trigger recovery alert
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
			// minute 1 is missing
			baseTime.Add(time.Minute * 2): {
				TimestampMinute: baseTime.Add(time.Minute * 2),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false},
				FailureCount:    2,
				TotalCount:      2,
			},
			baseTime.Add(time.Minute * 3): {
				TimestampMinute: baseTime.Add(time.Minute * 3),
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
		}
		latestTime := baseTime.Add(time.Minute * 3)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected recovery alert after state transition from unhealthy to healthy, got shouldAlert=false")
		}
		if alertReason != "Monitor has recovered and is now healthy" {
			t.Errorf("Expected recovery alert reason, got %s", alertReason)
		}
	})

	t.Run("Alert reason contains region names for multi-region failure", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true, "eu-west-1": true},
				FailureCount:    0,
				TotalCount:      3,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false, "eu-west-1": true},
				FailureCount:    2,
				TotalCount:      3,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected alert, got shouldAlert=false")
		}
		// The alert reason should contain the region names that failed
		// Note: order may vary, so check for both regions
		if !strings.Contains(alertReason, "us-east-1") && !strings.Contains(alertReason, "us-west-1") {
			t.Errorf("Expected alert reason to contain failed region names, got %s", alertReason)
		}
	})

	t.Run("Buckets only contain TotalCount 0 entries", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{},
				FailureCount:    0,
				TotalCount:      0,
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{},
				FailureCount:    0,
				TotalCount:      0,
			},
		}
		latestTime := baseTime.Add(time.Minute)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if shouldAlert {
			t.Errorf("Expected no alert when all buckets have TotalCount=0, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Mixed buckets - some with TotalCount 0 should be skipped", func(t *testing.T) {
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2, // Valid entry
			},
			baseTime.Add(time.Minute): {
				TimestampMinute: baseTime.Add(time.Minute),
				Regions:         map[string]bool{},
				FailureCount:    0,
				TotalCount:      0, // Should be skipped
			},
			baseTime.Add(time.Minute * 2): {
				TimestampMinute: baseTime.Add(time.Minute * 2),
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2, // Valid entry
			},
		}
		latestTime := baseTime.Add(time.Minute * 2)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, minuteInterval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		// Both valid entries are healthy, so no alert expected
		if shouldAlert {
			t.Errorf("Expected no alert for healthy states, got shouldAlert=true")
		}
		if alertReason != "" {
			t.Errorf("Expected empty alert reason, got %s", alertReason)
		}
	})

	t.Run("Larger interval - multiple minutes per bucket", func(t *testing.T) {
		interval := time.Minute * 5 // 5 minute intervals
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TimestampMinute: baseTime,
				Regions:         map[string]bool{"us-east-1": true, "us-west-1": true},
				FailureCount:    0,
				TotalCount:      2,
			},
			baseTime.Add(time.Minute * 5): {
				TimestampMinute: baseTime.Add(time.Minute * 5),
				Regions:         map[string]bool{"us-east-1": false, "us-west-1": false},
				FailureCount:    2,
				TotalCount:      2,
			},
		}
		latestTime := baseTime.Add(time.Minute * 5)
		earliestTime := baseTime

		shouldAlert, alertReason, err := processorWorker.analyzeSubmissions(buckets, latestTime, earliestTime, interval)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if !shouldAlert {
			t.Errorf("Expected alert for multi-region failure, got shouldAlert=false")
		}
		if !strings.Contains(alertReason, "High failure rate detected") {
			t.Errorf("Expected alert reason to contain 'High failure rate detected', got %s", alertReason)
		}
	})
}

func TestProcessorWorker_BuildFailureReasonsBreakdown(t *testing.T) {
	t.Run("Empty_buckets_returns_empty_breakdown", func(t *testing.T) {
		worker := &ProcessorWorker{datasetConfig: defaultTestDatasetConfig()}
		buckets := make(map[time.Time]SubmissionBucket)
		submissions := []MonitorHistorical{}

		result := worker.buildFailureReasonsBreakdown(buckets, submissions, time.Now().UTC(), time.Now().UTC().Add(-1*time.Hour), time.Minute)

		if len(result) != 0 {
			t.Errorf("Expected empty breakdown, got %v", result)
		}
	})

	t.Run("Only_includes_regions_marked_as_failed", func(t *testing.T) {
		worker := &ProcessorWorker{datasetConfig: defaultTestDatasetConfig()}
		baseTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

		// Create bucket with us-east-1 failed, eu-west-1 healthy
		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TotalCount:   2,
				FailureCount: 1,
				Regions: map[string]bool{
					"us-east-1": false, // failed
					"eu-west-1": true,  // healthy
				},
			},
		}

		// Create submissions: timeout from us-east-1, timeout from eu-west-1
		submissions := []MonitorHistorical{
			{
				Success:       false,
				CreatedAt:     baseTime.Add(30 * time.Second),
				Region:        "us-east-1",
				FailureReason: null.StringFrom("connection timeout"),
			},
			{
				Success:       false,
				CreatedAt:     baseTime.Add(45 * time.Second),
				Region:        "eu-west-1",
				FailureReason: null.StringFrom("connection timeout"),
			},
		}

		result := worker.buildFailureReasonsBreakdown(buckets, submissions, baseTime, baseTime.Add(-1*time.Hour), time.Minute)

		// Only us-east-1 should be in breakdown (eu-west-1 is marked healthy)
		if len(result) != 1 {
			t.Errorf("Expected 1 category, got %d: %v", len(result), result)
		}
		if regions, ok := result["timeout"]; ok {
			if len(regions) != 1 || regions[0] != "us-east-1" {
				t.Errorf("Expected only us-east-1 in timeout category, got %v", regions)
			}
		} else {
			t.Errorf("Expected timeout category in breakdown, got %v", result)
		}
	})

	t.Run("Boundary_timestamps_inclusive_start_exclusive_end", func(t *testing.T) {
		worker := &ProcessorWorker{datasetConfig: defaultTestDatasetConfig()}
		baseTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TotalCount:   1,
				FailureCount: 1,
				Regions:      map[string]bool{"us-east-1": false},
			},
		}

		// Submission exactly at bucketStart should be included (>= bucketStart)
		// Submission at bucketEnd should be excluded (< bucketEnd)
		submissions := []MonitorHistorical{
			{
				Success:       false,
				CreatedAt:     baseTime, // exactly at start - should be included
				Region:        "us-east-1",
				FailureReason: null.StringFrom("timeout"),
			},
			{
				Success:       false,
				CreatedAt:     baseTime.Add(1 * time.Minute), // exactly at end - should be excluded
				Region:        "us-east-1",
				FailureReason: null.StringFrom("timeout"),
			},
		}

		result := worker.buildFailureReasonsBreakdown(buckets, submissions, baseTime, baseTime.Add(-1*time.Hour), time.Minute)

		// Should only have 1 submission (the one at boundary start)
		if len(result) != 1 {
			t.Errorf("Expected 1 category, got %d: %v", len(result), result)
		}
		if regions, ok := result["timeout"]; ok {
			if len(regions) != 1 {
				t.Errorf("Expected 1 region in timeout category, got %d: %v", len(regions), regions)
			}
		}
	})

	t.Run("Multiple_failure_categories_aggregated", func(t *testing.T) {
		worker := &ProcessorWorker{datasetConfig: defaultTestDatasetConfig()}
		baseTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TotalCount:   2,
				FailureCount: 2,
				Regions: map[string]bool{
					"us-east-1": false,
					"eu-west-1": false,
				},
			},
		}

		submissions := []MonitorHistorical{
			{
				Success:       false,
				CreatedAt:     baseTime.Add(10 * time.Second),
				Region:        "us-east-1",
				FailureReason: null.StringFrom("connection timeout"),
			},
			{
				Success:       false,
				CreatedAt:     baseTime.Add(20 * time.Second),
				Region:        "eu-west-1",
				FailureReason: null.StringFrom("TLS certificate error"),
			},
		}

		result := worker.buildFailureReasonsBreakdown(buckets, submissions, baseTime, baseTime.Add(-1*time.Hour), time.Minute)

		if len(result) != 2 {
			t.Errorf("Expected 2 categories, got %d: %v", len(result), result)
		}

		if timeoutRegions, ok := result["timeout"]; ok && len(timeoutRegions) == 1 && timeoutRegions[0] == "us-east-1" {
			// Good
		} else {
			t.Errorf("Expected timeout in us-east-1, got %v", result["timeout"])
		}

		if tlsRegions, ok := result["tls_error"]; ok && len(tlsRegions) == 1 && tlsRegions[0] == "eu-west-1" {
			// Good
		} else {
			t.Errorf("Expected tls_error in eu-west-1, got %v", result["tls_error"])
		}
	})

	t.Run("Same_failure_category_multiple_regions", func(t *testing.T) {
		worker := &ProcessorWorker{datasetConfig: defaultTestDatasetConfig()}
		baseTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)

		buckets := map[time.Time]SubmissionBucket{
			baseTime: {
				TotalCount:   3,
				FailureCount: 3,
				Regions: map[string]bool{
					"us-east-1":  false,
					"eu-west-1":  false,
					"ap-south-1": false,
				},
			},
		}

		submissions := []MonitorHistorical{
			{
				Success:       false,
				CreatedAt:     baseTime.Add(10 * time.Second),
				Region:        "us-east-1",
				FailureReason: null.StringFrom("read timeout"),
			},
			{
				Success:       false,
				CreatedAt:     baseTime.Add(20 * time.Second),
				Region:        "eu-west-1",
				FailureReason: null.StringFrom("dial timeout"),
			},
			{
				Success:       false,
				CreatedAt:     baseTime.Add(30 * time.Second),
				Region:        "ap-south-1",
				FailureReason: null.StringFrom("i/o timeout"),
			},
		}

		result := worker.buildFailureReasonsBreakdown(buckets, submissions, baseTime, baseTime.Add(-1*time.Hour), time.Minute)

		if len(result) != 1 {
			t.Errorf("Expected 1 category, got %d: %v", len(result), result)
		}

		if regions, ok := result["timeout"]; ok {
			if len(regions) != 3 {
				t.Errorf("Expected 3 regions in timeout, got %d: %v", len(regions), regions)
			}
			// Verify sorted
			if regions[0] != "ap-south-1" || regions[1] != "eu-west-1" || regions[2] != "us-east-1" {
				t.Errorf("Expected sorted regions, got %v", regions)
			}
		} else {
			t.Errorf("Expected timeout category, got %v", result)
		}
	})

	t.Run("Uses_most_recent_bucket_with_failures", func(t *testing.T) {
		worker := &ProcessorWorker{datasetConfig: defaultTestDatasetConfig()}
		baseTime := time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)
		oldTime := baseTime.Add(-5 * time.Minute)

		buckets := map[time.Time]SubmissionBucket{
			oldTime: {
				TotalCount:   1,
				FailureCount: 1,
				Regions:      map[string]bool{"us-east-1": false},
			},
			baseTime: {
				TotalCount:   1,
				FailureCount: 1,
				Regions:      map[string]bool{"eu-west-1": false},
			},
		}

		submissions := []MonitorHistorical{
			{
				Success:       false,
				CreatedAt:     oldTime.Add(10 * time.Second),
				Region:        "us-east-1",
				FailureReason: null.StringFrom("timeout"),
			},
			{
				Success:       false,
				CreatedAt:     baseTime.Add(10 * time.Second),
				Region:        "eu-west-1",
				FailureReason: null.StringFrom("connection refused"),
			},
		}

		result := worker.buildFailureReasonsBreakdown(buckets, submissions, baseTime, oldTime.Add(-1*time.Hour), time.Minute)

		// Should only have data from most recent bucket (baseTime)
		if len(result) != 1 {
			t.Errorf("Expected 1 category, got %d: %v", len(result), result)
		}
		if regions, ok := result["connection_refused"]; ok {
			if len(regions) != 1 || regions[0] != "eu-west-1" {
				t.Errorf("Expected only eu-west-1 in connection_refused, got %v", regions)
			}
		} else {
			t.Errorf("Expected connection_refused category, got %v", result)
		}
	})
}

func TestProcessorWorker_EvaluateLatestIncident_LocalDegradedPromotion(t *testing.T) {
	monitor := Monitor{
		ID:                  "test-monitor",
		Name:                "Test monitor",
		ExpectedStatusCodes: []int{200},
	}

	newWorker := func(cfg DatasetConfig) *ProcessorWorker {
		return &ProcessorWorker{
			datasetConfig: cfg,
		}
	}

	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	minuteInterval := time.Minute

	t.Run("single local degraded bucket stays healthy until promoted", func(t *testing.T) {
		worker := newWorker(defaultTestDatasetConfig())
		submissions := []MonitorHistorical{
			{Region: "us-east-1", StatusCode: 500, CreatedAt: baseTime},
			{Region: "us-west-1", StatusCode: 200, CreatedAt: baseTime},
			{Region: "eu-west-1", StatusCode: 200, CreatedAt: baseTime},
		}

		buckets, earliestTime, latestTime := worker.groupSubmissionByMinute(submissions, minuteInterval, monitor)
		evaluation := worker.evaluateLatestIncident(buckets, submissions, latestTime, earliestTime, minuteInterval)

		if evaluation.Status != MonitorStatusHealthy {
			t.Fatalf("expected healthy evaluation before local degradation threshold, got %+v", evaluation)
		}
	})

	t.Run("local degraded promotes after configured consecutive buckets", func(t *testing.T) {
		worker := newWorker(defaultTestDatasetConfig())
		var submissions []MonitorHistorical
		for minute := 0; minute < 5; minute++ {
			bucketTime := baseTime.Add(time.Duration(minute) * minuteInterval)
			submissions = append(submissions,
				MonitorHistorical{Region: "us-east-1", StatusCode: 500, CreatedAt: bucketTime},
				MonitorHistorical{Region: "us-west-1", StatusCode: 200, CreatedAt: bucketTime},
				MonitorHistorical{Region: "eu-west-1", StatusCode: 200, CreatedAt: bucketTime},
			)
		}

		buckets, earliestTime, latestTime := worker.groupSubmissionByMinute(submissions, minuteInterval, monitor)
		evaluation := worker.evaluateLatestIncident(buckets, submissions, latestTime, earliestTime, minuteInterval)

		if evaluation.Status != MonitorStatusDegraded || evaluation.Scope != MonitorScopeLocal {
			t.Fatalf("expected promoted local degraded evaluation, got %+v", evaluation)
		}
	})

	t.Run("elapsed minutes can promote before consecutive bucket threshold", func(t *testing.T) {
		cfg := defaultTestDatasetConfig()
		cfg.DegradedThresholdMinutes = 3
		cfg.DegradedThresholdConsecutiveBuckets = 10
		worker := newWorker(cfg)

		var submissions []MonitorHistorical
		for minute := 0; minute < 3; minute++ {
			bucketTime := baseTime.Add(time.Duration(minute) * minuteInterval)
			submissions = append(submissions,
				MonitorHistorical{Region: "us-east-1", StatusCode: 500, CreatedAt: bucketTime},
				MonitorHistorical{Region: "us-west-1", StatusCode: 200, CreatedAt: bucketTime},
				MonitorHistorical{Region: "eu-west-1", StatusCode: 200, CreatedAt: bucketTime},
			)
		}

		buckets, earliestTime, latestTime := worker.groupSubmissionByMinute(submissions, minuteInterval, monitor)
		evaluation := worker.evaluateLatestIncident(buckets, submissions, latestTime, earliestTime, minuteInterval)

		if evaluation.Status != MonitorStatusDegraded || evaluation.Scope != MonitorScopeLocal {
			t.Fatalf("expected local degraded promotion from elapsed minutes threshold, got %+v", evaluation)
		}
	})

	t.Run("global down still alerts immediately", func(t *testing.T) {
		worker := newWorker(defaultTestDatasetConfig())
		submissions := []MonitorHistorical{
			{Region: "us-east-1", StatusCode: 500, CreatedAt: baseTime},
			{Region: "us-west-1", StatusCode: 500, CreatedAt: baseTime},
			{Region: "eu-west-1", StatusCode: 500, CreatedAt: baseTime},
		}

		buckets, earliestTime, latestTime := worker.groupSubmissionByMinute(submissions, minuteInterval, monitor)
		evaluation := worker.evaluateLatestIncident(buckets, submissions, latestTime, earliestTime, minuteInterval)

		if evaluation.Status != MonitorStatusDown || evaluation.Scope != MonitorScopeGlobal {
			t.Fatalf("expected immediate global down evaluation, got %+v", evaluation)
		}
	})
}
