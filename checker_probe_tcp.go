package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"time"

	"github.com/guregu/null/v5"
)

func (c *Checker) probeTCP(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(10 * time.Second)
	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", monitor.TCP.Address)
	if err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer conn.Close()

	if monitor.TCP.UseTLS {
		tlsConn := tls.Client(conn, &tls.Config{
			InsecureSkipVerify: monitor.TCP.SkipTLSVerify,
			ServerName:         serverNameForAddress(monitor.TCP.Address),
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			submission.LatencyMs = time.Since(start).Milliseconds()
			submission.FailureReason = null.StringFrom(err.Error())
			return submission
		}
		conn = tlsConn
	}

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	if monitor.TCP.Send.Valid {
		if _, err := io.WriteString(conn, monitor.TCP.Send.String); err != nil {
			submission.LatencyMs = time.Since(start).Milliseconds()
			submission.FailureReason = null.StringFrom(err.Error())
			return submission
		}
	}
	if monitor.TCP.ExpectContains.Valid {
		buffer := make([]byte, 4096)
		n, err := conn.Read(buffer)
		if err != nil {
			submission.LatencyMs = time.Since(start).Milliseconds()
			submission.FailureReason = null.StringFrom(err.Error())
			return submission
		}
		submission.ResponseBody = null.StringFrom(string(buffer[:n]))
		if !strings.Contains(submission.ResponseBody.String, monitor.TCP.ExpectContains.String) {
			submission.LatencyMs = time.Since(start).Milliseconds()
			submission.FailureReason = null.StringFrom("tcp response did not contain expected text")
			return submission
		}
	}

	submission.LatencyMs = time.Since(start).Milliseconds()
	submission.Success = true
	return submission
}
