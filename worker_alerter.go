package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

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
			ctx, cancel := context.WithCancel(context.Background())
			message, err := w.subscriber.Receive(ctx)
			cancel()
			if err != nil {
				slog.Error("receiving message for alert queue", slog.String("error", err.Error()))
				time.Sleep(10 * time.Millisecond)
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
				message.Ack()
				continue
			}

			sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
			var sendErr error
			for _, alerter := range w.alerters {
				if err := alerter.Send(sendCtx, alert); err != nil {
					sendErr = errors.Join(sendErr, err)
				}
			}
			sendCancel()

			if sendErr != nil {
				slog.Error("sending alert", slog.String("error", sendErr.Error()))
				if message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				continue
			}

			message.Ack()
		}
	}
}

func (w *AlerterWorker) Stop() error {
	close(w.shutdown)
	return nil
}
