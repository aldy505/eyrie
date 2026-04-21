package main

import (
	"context"
	"encoding/json"
	"errors"
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
}

type AlerterWorker struct {
	subscriber *pubsub.Subscription
	alerters   []Alerter
	shutdown   chan struct{}
}

func NewAlerterWorker(subscriber *pubsub.Subscription, alerters []Alerter) *AlerterWorker {
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

			if len(w.alerters) == 0 {
				slog.Info("alert received without configured delivery targets",
					slog.String("monitor_id", alert.MonitorID),
					slog.String("status", alert.Status),
					slog.String("scope", alert.Scope))
				sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.alert.deliveries.skipped", 1, sentry.WithAttributes(attribute.String("status", alert.Status), attribute.String("scope", alert.Scope)))
				message.Ack()
				cancel()
				continue
			}

			sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
			span := sentry.StartTransaction(sendCtx, "alerter.send_alert", sentry.WithOpName("task.alerter.process"), sentry.WithTransactionSource(sentry.SourceCustom))
			sendCtx = span.Context()
			var sendErr error
			deliveredCount := 0
			for _, alerter := range w.alerters {
				if err := alerter.Send(sendCtx, alert); err != nil {
					sendErr = errors.Join(sendErr, err)
					continue
				}
				deliveredCount++
			}

			if sendErr != nil {
				sentry.NewMeter(context.Background()).WithCtx(sendCtx).Count("eyrie.alert.deliveries.failed", 1, sentry.WithAttributes(attribute.String("status", alert.Status), attribute.String("scope", alert.Scope)))
				slog.Error("sending alert",
					slog.String("error", sendErr.Error()),
					slog.Int("delivered_count", deliveredCount),
					slog.Int("alerter_count", len(w.alerters)))
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

			sentry.NewMeter(context.Background()).WithCtx(sendCtx).Count("eyrie.alert.deliveries.sent", 1, sentry.WithAttributes(attribute.String("status", alert.Status), attribute.String("scope", alert.Scope)))
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
