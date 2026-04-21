package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/guregu/null/v5"
)

func (c *Checker) probeICMP(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(5 * time.Second)
	count := max(monitor.ICMP.Count, 1)
	waitSeconds := max(int(timeout/time.Second), 1)
	command := pingCommandContext(ctx, "ping", "-c", strconv.Itoa(count), "-W", strconv.Itoa(waitSeconds), monitor.ICMP.Host)
	output, err := command.CombinedOutput()

	submission := CheckerSubmissionRequest{
		MonitorID:    monitor.ID,
		ProbeType:    string(monitor.EffectiveType()),
		Timestamp:    time.Now().UTC(),
		LatencyMs:    time.Since(start).Milliseconds(),
		ResponseBody: null.StringFrom(string(output)),
	}
	if err != nil {
		submission.FailureReason = null.StringFrom(strings.TrimSpace(string(output)))
		if !submission.FailureReason.Valid || submission.FailureReason.String == "" {
			submission.FailureReason = null.StringFrom(err.Error())
		}
		return submission
	}

	submission.Success = true
	return submission
}
