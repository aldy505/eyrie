package main

import (
	"context"
	"sync"
	"time"

	"log/slog"
)

type BufferedNtfyAlerter struct {
	wrapped        NtfyAlerter
	suppressWindow time.Duration
	digestInterval time.Duration
	mu             sync.RWMutex
	states         map[string]*monitorAlertState
	digestTicker   *time.Ticker
	digestStop     chan struct{}
	digestWg       sync.WaitGroup
}

type monitorAlertState struct {
	suppressUntil     time.Time
	degradedSince     time.Time
	degradedBuffer    []AlertMessage
	notifiedUnhealthy bool
}

func NewBufferedNtfyAlerter(wrapped NtfyAlerter, suppressWindowMinutes, digestIntervalMinutes int) *BufferedNtfyAlerter {
	a := &BufferedNtfyAlerter{
		wrapped:        wrapped,
		suppressWindow: time.Duration(suppressWindowMinutes) * time.Minute,
		states:         make(map[string]*monitorAlertState),
		digestStop:     make(chan struct{}),
	}

	if digestIntervalMinutes > 0 {
		a.digestInterval = time.Duration(digestIntervalMinutes) * time.Minute
		a.digestTicker = time.NewTicker(a.digestInterval)
		a.digestWg.Add(1)
		go a.runDigestLoop()
	}

	return a
}

func (a *BufferedNtfyAlerter) runDigestLoop() {
	defer a.digestWg.Done()
	for {
		select {
		case <-a.digestTicker.C:
			a.flushDigest(context.Background())
		case <-a.digestStop:
			return
		}
	}
}

func (a *BufferedNtfyAlerter) Send(ctx context.Context, alert AlertMessage) error {
	switch alert.Status {
	case MonitorStatusDown:
		return a.handleDownAlert(ctx, alert)
	case MonitorStatusDegraded:
		return a.handleDegradedAlert(ctx, alert)
	case MonitorStatusHealthy:
		return a.handleHealthyAlert(ctx, alert)
	default:
		return a.wrapped.Send(ctx, alert)
	}
}

func (a *BufferedNtfyAlerter) handleDownAlert(ctx context.Context, alert AlertMessage) error {
	a.mu.Lock()
	state := a.getOrCreateState(alert.MonitorID)
	suppressed := time.Now().Before(state.suppressUntil)
	state.suppressUntil = time.Now().Add(a.suppressWindow)
	state.degradedBuffer = nil
	a.mu.Unlock()

	if suppressed {
		slog.Debug("ntfy: down alert suppressed",
			slog.String("monitor_id", alert.MonitorID),
			slog.String("name", alert.Name))
		return nil
	}

	if err := a.wrapped.Send(ctx, alert); err != nil {
		return err
	}

	a.mu.Lock()
	state.notifiedUnhealthy = true
	a.mu.Unlock()
	return nil
}

func (a *BufferedNtfyAlerter) handleDegradedAlert(ctx context.Context, alert AlertMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	state := a.getOrCreateState(alert.MonitorID)

	if a.digestInterval <= 0 {
		return nil
	}

	if time.Now().Before(state.suppressUntil) {
		return nil
	}

	if len(state.degradedBuffer) == 0 {
		state.degradedSince = alert.OccurredAt
	}
	state.degradedBuffer = append(state.degradedBuffer, alert)
	return nil
}

func (a *BufferedNtfyAlerter) handleHealthyAlert(ctx context.Context, alert AlertMessage) error {
	a.mu.Lock()
	state, exists := a.states[alert.MonitorID]
	if exists && len(state.degradedBuffer) > 0 {
		a.mu.Unlock()
		a.flushDigestForMonitor(ctx, alert.MonitorID)
		return nil
	}

	if !exists {
		a.mu.Unlock()
		return a.wrapped.Send(ctx, alert)
	}

	if !state.notifiedUnhealthy {
		a.mu.Unlock()
		slog.Debug("ntfy: healthy alert suppressed (no delivered unhealthy notification)",
			slog.String("monitor_id", alert.MonitorID),
			slog.String("name", alert.Name))
		return nil
	}

	a.mu.Unlock()
	if err := a.wrapped.Send(ctx, alert); err != nil {
		return err
	}

	a.mu.Lock()
	state.notifiedUnhealthy = false
	a.mu.Unlock()
	return nil
}

func (a *BufferedNtfyAlerter) getOrCreateState(monitorID string) *monitorAlertState {
	if state, ok := a.states[monitorID]; ok {
		return state
	}
	state := &monitorAlertState{}
	a.states[monitorID] = state
	return state
}

func (a *BufferedNtfyAlerter) flushDigest(ctx context.Context) {
	a.mu.Lock()
	monitorIDs := make([]string, 0, len(a.states))
	for monitorID := range a.states {
		monitorIDs = append(monitorIDs, monitorID)
	}
	a.mu.Unlock()

	for _, monitorID := range monitorIDs {
		a.flushDigestForMonitor(ctx, monitorID)
	}
}

func (a *BufferedNtfyAlerter) flushDigestForMonitor(ctx context.Context, monitorID string) {
	a.mu.Lock()
	state, exists := a.states[monitorID]
	if !exists || len(state.degradedBuffer) == 0 {
		a.mu.Unlock()
		return
	}

	digest := a.buildDigestMessage(monitorID, state)
	first := state.degradedBuffer[0]
	first.Reason = digest
	first.Status = MonitorStatusHealthy
	bufferLength := len(state.degradedBuffer)
	state.degradedBuffer = nil
	a.mu.Unlock()

	if err := a.wrapped.Send(ctx, first); err != nil {
		slog.Error("ntfy: failed to send degraded digest",
			slog.String("monitor_id", monitorID),
			slog.String("error", err.Error()))
		return
	}

	slog.Debug("ntfy: sent degraded digest",
		slog.String("monitor_id", monitorID),
		slog.Int("alert_count", bufferLength))

	a.mu.Lock()
	state.notifiedUnhealthy = true
	a.mu.Unlock()
}

func (a *BufferedNtfyAlerter) buildDigestMessage(monitorID string, state *monitorAlertState) string {
	first := state.degradedBuffer[0]
	regions := joinUniqueRegions(state.degradedBuffer)
	uniqueReasons := uniqueReasons(state.degradedBuffer)

	digest := first.Name + " has been degraded since " + state.degradedSince.Format(time.DateTime)
	if regions != "" {
		digest += ". Regions: " + regions
	}
	if uniqueReasons != "" {
		digest += ". Issues: " + uniqueReasons
	}

	return digest
}

func joinUniqueRegions(alerts []AlertMessage) string {
	seen := make(map[string]struct{})
	var regions []string
	for _, alert := range alerts {
		for _, r := range alert.AffectedRegions {
			if _, ok := seen[r]; !ok {
				seen[r] = struct{}{}
				regions = append(regions, r)
			}
		}
	}
	return joinStrings(regions)
}

func uniqueReasons(alerts []AlertMessage) string {
	seen := make(map[string]struct{})
	var reasons []string
	for _, alert := range alerts {
		if alert.Reason != "" {
			if _, ok := seen[alert.Reason]; !ok {
				seen[alert.Reason] = struct{}{}
				reasons = append(reasons, alert.Reason)
			}
		}
	}
	return joinStrings(reasons)
}

func joinStrings(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "; "
		}
		result += p
	}
	return result
}

func (a *BufferedNtfyAlerter) Stop() {
	if a.digestTicker != nil {
		a.digestTicker.Stop()
		close(a.digestStop)
		a.digestWg.Wait()
	}
}
