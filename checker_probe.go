package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/guregu/null/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func (c *Checker) probeMonitor(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	switch monitor.EffectiveType() {
	case MonitorTypeTCP:
		return c.probeTCP(ctx, monitor)
	case MonitorTypeICMP:
		return c.probeICMP(ctx, monitor)
	case MonitorTypeRedis:
		return c.probeRedis(ctx, monitor)
	case MonitorTypePostgres:
		return c.probePostgres(ctx, monitor)
	default:
		return c.probeHTTP(ctx, monitor)
	}
}

func (c *Checker) probeHTTP(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	httpConfig := monitor.EffectiveHTTP()
	requestStart := time.Now()
	checkerTracer := NewCheckerTracer()
	traceCtx := httptrace.WithClientTrace(ctx, checkerTracer.GetClientTrace())

	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	request, err := http.NewRequestWithContext(traceCtx, httpConfig.Method, httpConfig.URL, nil)
	if err != nil {
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	for key, value := range httpConfig.Headers {
		request.Header.Set(key, value)
	}

	timeout := monitor.EffectiveTimeout(30 * time.Second)
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: httpConfig.SkipTLSVerify},
		},
		Timeout: timeout,
	}

	response, err := httpClient.Do(request)
	submission.LatencyMs = time.Since(requestStart).Milliseconds()
	submission.Timings = checkerTracer.GetTimings()
	if err != nil {
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer func() {
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}()

	submission.StatusCode = response.StatusCode
	submission.Success = monitor.IsSuccessfulStatus(response.StatusCode, false)

	if response.ContentLength > 0 && response.Body != nil {
		bodyBytes, err := io.ReadAll(response.Body)
		if err == nil {
			submission.ResponseBody = null.StringFrom(string(bodyBytes))
		}
	}

	if response.TLS != nil {
		submission.TlsVersion = null.StringFrom(tls.VersionName(response.TLS.Version))
		submission.TlsCipher = null.StringFrom(tls.CipherSuiteName(response.TLS.CipherSuite))
		if len(response.TLS.PeerCertificates) > 0 && response.TLS.PeerCertificates[0] != nil {
			submission.TlsExpiry = null.TimeFrom(response.TLS.PeerCertificates[0].NotAfter)
		}
	}

	if !submission.Success {
		submission.FailureReason = null.StringFrom(fmt.Sprintf("received unexpected status code %d", response.StatusCode))
	}

	return submission
}

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
			ServerName:         strings.Split(monitor.TCP.Address, ":")[0],
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

func (c *Checker) probeICMP(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(5 * time.Second)
	count := max(monitor.ICMP.Count, 1)
	waitSeconds := max(int(timeout/time.Second), 1)
	command := exec.CommandContext(ctx, "ping", "-c", strconv.Itoa(count), "-W", strconv.Itoa(waitSeconds), monitor.ICMP.Host)
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

func (c *Checker) probePostgres(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(10 * time.Second)
	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	db, err := sql.Open("pgx", monitor.Postgres.DSN)
	if err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer db.Close()

	if err := db.PingContext(pingCtx); err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}

	submission.LatencyMs = time.Since(start).Milliseconds()
	submission.Success = true
	return submission
}

func sendRedisCommand(conn net.Conn, command string, arguments ...string) error {
	parts := append([]string{command}, arguments...)
	var payload bytes.Buffer
	payload.WriteString(fmt.Sprintf("*%d\r\n", len(parts)))
	for _, part := range parts {
		payload.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(part), part))
	}
	if _, err := conn.Write(payload.Bytes()); err != nil {
		return err
	}

	reply := make([]byte, 4096)
	n, err := conn.Read(reply)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("empty redis reply")
	}
	if reply[0] == '-' {
		return errors.New(strings.TrimSpace(string(reply[:n])))
	}
	return nil
}
