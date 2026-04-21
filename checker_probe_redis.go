package main

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"time"

	"github.com/guregu/null/v5"
)

func (c *Checker) probeRedis(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(10 * time.Second)
	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	var conn net.Conn
	var err error
	if monitor.Redis.UseTLS {
		dialer := &net.Dialer{Timeout: timeout}
		conn, err = tls.DialWithDialer(dialer, "tcp", monitor.Redis.Address, &tls.Config{InsecureSkipVerify: monitor.Redis.SkipTLSVerify})
	} else {
		conn, err = net.DialTimeout("tcp", monitor.Redis.Address, timeout)
	}
	if err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}

	if monitor.Redis.Password.Valid {
		if err := sendRedisCommand(conn, "AUTH", monitor.Redis.Password.String); err != nil {
			submission.LatencyMs = time.Since(start).Milliseconds()
			submission.FailureReason = null.StringFrom(err.Error())
			return submission
		}
	}
	if monitor.Redis.Database > 0 {
		if err := sendRedisCommand(conn, "SELECT", strconv.Itoa(monitor.Redis.Database)); err != nil {
			submission.LatencyMs = time.Since(start).Milliseconds()
			submission.FailureReason = null.StringFrom(err.Error())
			return submission
		}
	}
	if err := sendRedisCommand(conn, "PING"); err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}

	submission.LatencyMs = time.Since(start).Milliseconds()
	submission.Success = true
	return submission
}
