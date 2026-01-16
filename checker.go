package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/guregu/null/v5"
	"golang.org/x/sync/semaphore"
)

type Checker struct {
	config            CheckerConfig
	httpClient        *http.Client
	shutdown          chan struct{}
	monitors          MonitorConfig
	nextUpstreamFetch *time.Time
}

type CheckerOptions struct {
	CheckerConfig CheckerConfig
	HttpClient    *http.Client
}

func NewChecker(options CheckerOptions) (*Checker, error) {
	if options.HttpClient == nil {
		options.HttpClient = http.DefaultClient
	}
	return &Checker{
		config:     options.CheckerConfig,
		httpClient: options.HttpClient,
		shutdown:   make(chan struct{}),
	}, nil
}

func (c *Checker) Start() error {
	// We want to register to upstream first.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

REGISTER_UPSTREAM:
	registerCtx, registerCancel := context.WithTimeout(ctx, time.Minute)
	defer registerCancel()

	err := c.registerToUpstream(registerCtx)
	if err != nil {
		if errors.Is(err, ErrCheckerRegistrationFailed) {
			slog.ErrorContext(ctx, "checker registration failed, will retry in 10 seconds", slog.String("error", err.Error()))
			time.Sleep(10 * time.Second)
			goto REGISTER_UPSTREAM
		}

		return fmt.Errorf("registering to upstream: %w", err)
	}

	nextUpstreamFetch := time.Now().Add(10 * time.Minute)
	c.nextUpstreamFetch = &nextUpstreamFetch

	for {
		ctx := sentry.SetHubOnContext(ctx, sentry.CurrentHub().Clone())

		select {
		case <-c.shutdown:
			return nil
		default:
			currentTime := time.Now()
			if c.nextUpstreamFetch != nil {
				checkerCtx, checkerCancel := context.WithTimeout(ctx, time.Minute*5)

				// Perform checks based on existing monitors
				if len(c.monitors.Monitors) == 0 {
					slog.InfoContext(ctx, "no monitors received from upstream, sleeping for 30 seconds")
					time.Sleep(30 * time.Second)
					checkerCancel()
					continue
				}

				// We only want to perform checks for every minute with 0 seconds.
				if currentTime.Second() != 0 {
					nextCheck := currentTime.Truncate(time.Minute).Add(time.Minute)
					sleepDuration := nextCheck.Sub(currentTime)
					checkerCancel()
					slog.InfoContext(ctx, "sleeping until next full minute for checks", slog.Duration("sleep_duration", sleepDuration))
					time.Sleep(sleepDuration)
					continue
				}

				span := sentry.StartSpan(checkerCtx, "function", sentry.WithDescription("Perform Monitor Checks"))
				ctx = span.Context()
				slog.InfoContext(ctx, "performing checks for monitors", slog.Int("monitor_count", len(c.monitors.Monitors)))
				s := semaphore.NewWeighted(10) // Limit to 10 concurrent checks
				wg := sync.WaitGroup{}
				for _, monitor := range c.monitors.Monitors {
					wg.Go(func() {
						if err := s.Acquire(checkerCtx, 1); err != nil {
							slog.ErrorContext(ctx, "acquiring semaphore for monitor check", slog.String("error", err.Error()))
							return
						}
						defer s.Release(1)

						checkerStart := time.Now()
						err := c.performMonitorCheck(checkerCtx, monitor)
						if err != nil {
							slog.ErrorContext(ctx, "performing monitor check", slog.String("monitor_id", monitor.ID), slog.String("error", err.Error()))
						}

						slog.InfoContext(ctx, "completed monitor check", slog.String("monitor_id", monitor.ID), slog.Duration("duration", time.Since(checkerStart)))
					})
				}

				wg.Wait()
				span.Finish()
				checkerCancel()
			}

			// We fetch new monitor config from upstream in the background
			if c.nextUpstreamFetch != nil && currentTime.After(*c.nextUpstreamFetch) {
				go func() {
					fetchCtx, fetchCancel := context.WithTimeout(ctx, time.Minute)
					defer fetchCancel()

					err := c.registerToUpstream(fetchCtx)
					if err != nil {
						if errors.Is(err, ErrCheckerRegistrationFailed) {
							slog.ErrorContext(ctx, "checker registration failed, not updating the next upstream fetch value", slog.String("error", err.Error()))
							return
						}

						slog.ErrorContext(ctx, "failed to fetch monitor config from upstream", slog.String("error", err.Error()))
						return
					}

					nextUpstreamFetch := currentTime.Add(10 * time.Minute)
					c.nextUpstreamFetch = &nextUpstreamFetch
				}()
			}

			time.Sleep(time.Second)
		}
	}
}

func (c *Checker) Stop() error {
	close(c.shutdown)
	return nil
}

// ErrCheckerRegistrationFailed is returned when the checker fails to register to the upstream server.
var ErrCheckerRegistrationFailed = fmt.Errorf("checker registration failed")

func (c *Checker) registerToUpstream(ctx context.Context) error {
	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Register Checker to upstream"))
	ctx = span.Context()
	defer span.Finish()

	requestUrl, err := url.JoinPath(c.config.UpstreamURL, "/checker/register")
	if err != nil {
		return fmt.Errorf("joining upstream URL: %w", err)
	}

	requestBody, err := json.Marshal(CheckerRegistrationRequest{
		Region: c.config.Region,
	})
	if err != nil {
		return fmt.Errorf("marshaling registration request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestUrl, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("creating registration request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-API-Key", c.config.ApiKey)
	request.Header.Set("User-Agent", "eyrie-checker/1.0")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("sending registration request: %w", err)
	}
	defer func() {
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: received status code %d", ErrCheckerRegistrationFailed, response.StatusCode)
	}

	var monitorConfig MonitorConfig
	if err := json.NewDecoder(response.Body).Decode(&monitorConfig); err != nil {
		return fmt.Errorf("decoding registration response: %w", err)
	}

	c.monitors = monitorConfig
	return nil
}

func (c *Checker) performMonitorCheck(ctx context.Context, monitor Monitor) error {
	// We parse the interval first. If the interval is sufficient, we may perform the check.
	// The interval should be in the format of stringified `time.Duration` (e.g., "1m", "30s").
	// If the interval is below 1 minute, we execute it immediately as the 1 minute guard
	// is handled in the main loop.
	// Otherwise, if the interval is 5 minutes, we may only perform the check at every 5th minute.
	// This is to reduce the load on both the checker and the upstream server.

	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Perform Monitor Check"))
	ctx = span.Context()
	defer span.Finish()

	parsedInterval, err := time.ParseDuration(monitor.Interval)
	if err != nil {
		return fmt.Errorf("parsing monitor interval: %w", err)
	}

	// Check if it's above 1 minute
	if parsedInterval > time.Minute {
		currentTime := time.Now()
		// We only want to perform checks at multiples of the interval.
		// For example, if the interval is 5 minutes, we want to perform checks at 0, 5, 10, 15, ... minutes.
		if currentTime.Unix()%int64(parsedInterval.Seconds()) != 0 {
			slog.InfoContext(ctx, "skipping monitor check due to interval not met", slog.String("monitor_id", monitor.ID), slog.String("interval", monitor.Interval))
			return nil
		}
	}

	requestStart := time.Now()
	checkerTracer := NewCheckerTracer()
	ctx = httptrace.WithClientTrace(ctx, checkerTracer.GetClientTrace())
	request, err := http.NewRequestWithContext(ctx, monitor.Method, monitor.Url, nil)
	if err != nil {
		return fmt.Errorf("creating monitor request: %w", err)
	}
	if monitor.Headers != nil {
		for key, value := range monitor.Headers {
			request.Header.Set(key, value)
		}
	}
	var timeout = time.Second * 30
	if monitor.TimeoutSeconds.Valid {
		timeout = time.Duration(monitor.TimeoutSeconds.Int64) * time.Second
	}

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
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: monitor.SkipTLSVerify},
		},
		Timeout: timeout,
	}

	response, err := httpClient.Do(request)
	latency := time.Since(requestStart).Milliseconds()
	if err != nil {
		// Even if the request fails, we want to record the latency and status code as 0.
		slog.ErrorContext(ctx, "performing monitor request", slog.String("monitor_id", monitor.ID), slog.String("error", err.Error()))
		go c.sendMonitorSubmission(ctx, CheckerSubmissionRequest{
			MonitorID:  monitor.ID,
			LatencyMs:  latency,
			StatusCode: 0,
			Timestamp:  time.Now(),
		})
		return nil
	}
	defer func() {
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}()

	var responseBody null.String
	if response.ContentLength > 0 && response.Body != nil {
		bodyBytes, err := io.ReadAll(response.Body)
		if err != nil {
			slog.ErrorContext(ctx, "reading monitor response body", slog.String("monitor_id", monitor.ID), slog.String("error", err.Error()))
		} else {
			responseBody = null.NewString(string(bodyBytes), true)
		}
	}

	var tlsVersion null.String
	var tlsCipher null.String
	var tlsExpiry null.Time
	if response.TLS != nil {
		tlsVersion = null.NewString(tls.VersionName(response.TLS.Version), true)
		tlsCipher = null.NewString(tls.CipherSuiteName(response.TLS.CipherSuite), true)

		if len(response.TLS.PeerCertificates) > 0 {
			// According to the Go stdlib docs:
			// The first element is the leaf certificate that the connection is verified against.
			firstPeerCertificate := response.TLS.PeerCertificates[0]
			if firstPeerCertificate != nil {
				tlsExpiry = null.NewTime(firstPeerCertificate.NotAfter, true)
			}
		}
	}

	go c.sendMonitorSubmission(ctx, CheckerSubmissionRequest{
		MonitorID:       monitor.ID,
		LatencyMs:       latency,
		StatusCode:      response.StatusCode,
		Timestamp:       time.Now(),
		ResponseHeaders: map[string]string{},
		ResponseBody:    responseBody,
		TlsVersion:      tlsVersion,
		TlsCipher:       tlsCipher,
		TlsExpiry:       tlsExpiry,
		Timings:         checkerTracer.GetTimings(),
	})

	return nil
}

func (c *Checker) sendMonitorSubmission(ctx context.Context, submission CheckerSubmissionRequest) {
	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Send Monitor Submission"))
	ctx = span.Context()
	defer span.Finish()

	// We don't want the submission to be cancelled if the parent context is cancelled.
	ctx = context.WithoutCancel(ctx)

	submissionBody, err := json.Marshal(submission)
	if err != nil {
		slog.ErrorContext(ctx, "marshaling monitor submission", slog.String("error", err.Error()))
		return
	}

	requestUrl, err := url.JoinPath(c.config.UpstreamURL, "/checker/submit")
	if err != nil {
		slog.ErrorContext(ctx, "joining submission URL", slog.String("error", err.Error()))
		return
	}

	var retryRemaining = 3
	var parentErr error
	for retryRemaining > 0 {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestUrl, bytes.NewReader(submissionBody))
		if err != nil {
			parentErr = fmt.Errorf("creating submission request: %w", err)
			slog.ErrorContext(ctx, "creating submission request", slog.String("error", err.Error()))
			retryRemaining--
			time.Sleep(time.Millisecond * 10)
			continue
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-API-Key", c.config.ApiKey)
		request.Header.Set("User-Agent", "eyrie-checker/1.0")
		response, err := c.httpClient.Do(request)
		if err != nil {
			parentErr = fmt.Errorf("sending submission request: %w", err)
			slog.ErrorContext(ctx, "sending submission request", slog.String("error", err.Error()))
			retryRemaining--
			time.Sleep(time.Millisecond * 10)
			continue
		}
		defer func() {
			if response.Body != nil {
				_ = response.Body.Close()
			}
		}()

		if response.StatusCode != http.StatusOK {
			parentErr = fmt.Errorf("received non-200 status code %d", response.StatusCode)
			slog.ErrorContext(ctx, "submission request failed", slog.String("error", parentErr.Error()))
			retryRemaining--
			time.Sleep(time.Millisecond * 10)
			continue
		}

		// Success
		return
	}

	if parentErr != nil {
		slog.ErrorContext(ctx, "failed to send monitor submission after retries", slog.String("error", parentErr.Error()))
	}
}
