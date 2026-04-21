package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"time"

	"github.com/guregu/null/v5"
)

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
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: httpConfig.SkipTLSVerifyValue()},
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
