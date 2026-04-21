package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
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

				checkCycleStart := time.Now()
				span := sentry.StartTransaction(checkerCtx, "checker.perform_checks", sentry.WithOpName("task.checker.cycle"), sentry.WithTransactionSource(sentry.SourceCustom))
				ctx = span.Context()
				span.SetData("eyrie.monitor_count", len(c.monitors.Monitors))
				span.SetData("eyrie.region", c.config.Region)
				sentry.NewMeter(context.Background()).WithCtx(ctx).Gauge("eyrie.checker.monitors.configured", float64(len(c.monitors.Monitors)), sentry.WithAttributes(attribute.String("region", c.config.Region)))
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
				sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.checker.cycle.duration", float64(time.Since(checkCycleStart).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("region", c.config.Region)))
				span.Finish()
				checkerCancel()
			}

			// We fetch new monitor config from upstream in the background
			if c.nextUpstreamFetch != nil && currentTime.After(*c.nextUpstreamFetch) {
				go func() {
					fetchCtx, fetchCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
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
	span := sentry.StartSpan(ctx, "http.client", sentry.WithDescription("Register Checker to upstream"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
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
	checkStartedAt := time.Now()
	span := sentry.StartSpan(ctx, "monitor.check", sentry.WithDescription("Perform Monitor Check"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()
	span.SetData("eyrie.monitor_id", monitor.ID)
	span.SetData("eyrie.probe_type", monitor.EffectiveType())
	span.SetData("eyrie.region", c.config.Region)

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

	submission := c.probeMonitor(ctx, monitor)
	if submission.Timestamp.IsZero() {
		submission.Timestamp = time.Now().UTC()
	}
	if !submission.Success && !submission.FailureReason.Valid {
		submission.FailureReason = null.StringFrom("probe failed")
	}

	result := "success"
	if !submission.Success {
		result = "failure"
	}
	sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.checks", 1, sentry.WithAttributes(attribute.String("probe_type", submission.ProbeType), attribute.String("region", c.config.Region), attribute.String("result", result)))
	sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.monitor.check.duration", float64(time.Since(checkStartedAt).Milliseconds()), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", submission.ProbeType), attribute.String("region", c.config.Region), attribute.String("result", result)))
	if submission.LatencyMs > 0 {
		sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.monitor.latency", float64(submission.LatencyMs), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", submission.ProbeType), attribute.String("region", c.config.Region), attribute.String("result", result)))
	}

	go c.sendMonitorSubmission(ctx, submission)
	return nil
}

func (c *Checker) sendMonitorSubmission(ctx context.Context, submission CheckerSubmissionRequest) {
	span := sentry.StartSpan(ctx, "http.client", sentry.WithDescription("Send Monitor Submission"), sentry.WithSpanOrigin(sentry.SpanOriginManual))
	ctx = span.Context()
	defer span.Finish()
	sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.attempted", 1, sentry.WithAttributes(attribute.String("probe_type", submission.ProbeType), attribute.String("region", c.config.Region)))

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
		sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.sent", 1, sentry.WithAttributes(attribute.String("probe_type", submission.ProbeType), attribute.String("region", c.config.Region)))
		return
	}

	if parentErr != nil {
		sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.failed", 1, sentry.WithAttributes(attribute.String("probe_type", submission.ProbeType), attribute.String("region", c.config.Region)))
		slog.ErrorContext(ctx, "failed to send monitor submission after retries", slog.String("error", parentErr.Error()))
	}
}
