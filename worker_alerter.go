package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"gocloud.dev/pubsub"
)

type AlertMessage struct {
	MonitorID  string    `json:"monitor_id"`
	Name       string    `json:"name"`
	Reason     string    `json:"reason"`
	OccurredAt time.Time `json:"occurred_at"`
}

type AlerterWorker struct {
	subscriber *pubsub.Subscription
	shutdown   chan struct{}
}

func NewAlerterWorker(subscriber *pubsub.Subscription) *AlerterWorker {
	return &AlerterWorker{
		subscriber: subscriber,
		shutdown:   make(chan struct{}),
	}
}

// Start begins the alerting process, listening for new messages.
// It is a blocking call.
func (w *AlerterWorker) Start() error {
	for {
		select {
		case <-w.shutdown:
			return nil
		default:
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			message, err := w.subscriber.Receive(ctx)
			if err != nil {
				slog.ErrorContext(ctx, "receiving message for alert queue", slog.String("error", err.Error()))
				time.Sleep(time.Millisecond * 10)
				continue
			}

			// Process the message here.
			var alert AlertMessage
			if err := json.Unmarshal(message.Body, &alert); err != nil {
				slog.ErrorContext(ctx, "unmarshaling alert message", slog.String("error", err.Error()))
				if message.Nackable() {
					message.Nack()
				} else {
					message.Ack()
				}
				continue
			}

			slog.InfoContext(ctx, "received alert", slog.String("monitor_id", alert.MonitorID), slog.String("name", alert.Name), slog.String("reason", alert.Reason), slog.Time("occurred_at", alert.OccurredAt))

			// Here you would add the logic to actually send the alert (e.g., email, SMS, etc.)
			// TODO: Implement this

			message.Ack()
		}
	}
}

func (w *AlerterWorker) Stop() error {
	close(w.shutdown)
	return nil
}
