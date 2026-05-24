package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"gocloud.dev/pubsub"
)

type AlertMessage struct {
	MonitorID       string    `json:"monitor_id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	Scope           string    `json:"scope"`
	Reason          string    `json:"reason"`
	OccurredAt      time.Time `json:"occurred_at"`
	AffectedRegions []string  `json:"affected_regions"`
	AlertNames      []string  `json:"alert_names,omitempty"`
}

type AlerterWorker struct {
	subscriber *pubsub.Subscription
	alerters   []NamedAlerter
	shutdown   chan struct{}
}

func NewAlerterWorker(subscriber *pubsub.Subscription, alerters []NamedAlerter) *AlerterWorker {
	return &AlerterWorker{
		subscriber: subscriber,
		alerters:   alerters,
		shutdown:   make(chan struct{}),
	}
}

func (w *AlerterWorker) Start() error {
	for {
		select {
		case <-w.shutdown:
			return nil
		default:
			ctx, cancel := context.WithCancel(sentry.SetHubOnContext(context.Background(), sentry.CurrentHub().Clone()))
			message, err := w.subscriber.Receive(ctx)
			if err != nil {
				slog.Error("receiving message for alert queue", slog.String("error", err.Error()))
				time.Sleep(10 * time.Millisecond)
				cancel()
				continue
			}

			var alert AlertMessage
			if err := json.Unmarshal(message.Body, &alert); err != nil {
				slog.Error("unmarshaling alert message", slog.String("error", err.Error()))
				if message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				continue
			}

			targetAlerters := selectNamedAlerters(w.alerters, alert.AlertNames)
			targetSelection := describeTargetSelection(w.alerters, alert.AlertNames, targetAlerters)
			if len(targetAlerters) == 0 {
				logMessage := "alert received without configured delivery targets"
				if targetSelection == "no_matching_named_targets" {
					logMessage = "alert received without matching named delivery targets"
				}
				slog.Warn(logMessage,
					slog.String("monitor_id", alert.MonitorID),
					slog.String("status", alert.Status),
					slog.String("scope", alert.Scope),
					slog.String("target_selection", targetSelection),
					slog.Any("requested_alert_names", alert.AlertNames),
					slog.Any("configured_alert_names", namedAlerterNames(w.alerters)))
				sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.alert.deliveries.skipped", 1, sentry.WithAttributes(
					attribute.String("status", alert.Status),
					attribute.String("scope", alert.Scope),
					attribute.String("target_selection", targetSelection),
				))
				message.Ack()
				cancel()
				continue
			}

			sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
			span := sentry.StartTransaction(sendCtx, "alerter.send_alert", sentry.WithOpName("task.alerter.process"), sentry.WithTransactionSource(sentry.SourceCustom))
			sendCtx = span.Context()
			deliveredCount, failedAlerters, sendErr := sendAlertToAlerters(sendCtx, targetAlerters, alert)

			if sendErr != nil {
				sentry.NewMeter(context.Background()).WithCtx(sendCtx).Count("eyrie.alert.deliveries.failed", 1, sentry.WithAttributes(
					attribute.String("status", alert.Status),
					attribute.String("scope", alert.Scope),
					attribute.String("target_selection", targetSelection),
				))
				slog.Error("sending alert",
					slog.String("error", sendErr.Error()),
					slog.Int("delivered_count", deliveredCount),
					slog.Int("alerter_count", len(targetAlerters)),
					slog.Any("failed_alerter_names", failedAlerters))
				if deliveredCount == 0 && message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				sendCancel()
				span.Finish()
				cancel()
				continue
			}

			sentry.NewMeter(context.Background()).WithCtx(sendCtx).Count("eyrie.alert.deliveries.sent", 1, sentry.WithAttributes(
				attribute.String("status", alert.Status),
				attribute.String("scope", alert.Scope),
				attribute.String("target_selection", targetSelection),
			))
			message.Ack()
			sendCancel()
			span.Finish()
			cancel()
		}
	}
}

func (w *AlerterWorker) Stop() error {
	close(w.shutdown)
	return nil
}

func sendAlertToAlerters(ctx context.Context, alerters []NamedAlerter, alert AlertMessage) (int, []string, error) {
	deliveredCount := 0
	failedAlerters := make([]string, 0)
	var sendErr error

	for _, alerter := range alerters {
		if err := alerter.Alerter.Send(ctx, alert); err != nil {
			failedAlerters = append(failedAlerters, alerter.Name)
			sendErr = errors.Join(sendErr, fmt.Errorf("alerter %q: %w", alerter.Name, err))
			continue
		}
		deliveredCount++
	}

	return deliveredCount, failedAlerters, sendErr
}

func selectNamedAlerters(alerters []NamedAlerter, alertNames []string) []NamedAlerter {
	if len(alertNames) == 0 {
		return alerters
	}

	requestedAlertNames := make(map[string]struct{}, len(alertNames))
	for _, alertName := range alertNames {
		if alertName == "" {
			continue
		}
		requestedAlertNames[alertName] = struct{}{}
	}
	if len(requestedAlertNames) == 0 {
		return nil
	}

	filteredAlerters := make([]NamedAlerter, 0, len(requestedAlertNames))
	for _, alerter := range alerters {
		if _, exists := requestedAlertNames[alerter.Name]; exists {
			filteredAlerters = append(filteredAlerters, alerter)
		}
	}

	return filteredAlerters
}

func namedAlerterNames(alerters []NamedAlerter) []string {
	names := make([]string, 0, len(alerters))
	for _, alerter := range alerters {
		names = append(names, alerter.Name)
	}

	return names
}

func describeTargetSelection(configuredAlerters []NamedAlerter, requestedAlertNames []string, selectedAlerters []NamedAlerter) string {
	if len(selectedAlerters) > 0 {
		if len(requestedAlertNames) > 0 {
			return "matched_named_targets"
		}
		return "all_enabled_targets"
	}
	if len(requestedAlertNames) > 0 {
		return "no_matching_named_targets"
	}
	if len(configuredAlerters) == 0 {
		return "no_configured_targets"
	}

	return "no_selected_targets"
}
