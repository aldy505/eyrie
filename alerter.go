package main

import (
	"context"
	"errors"
	"time"
)

// ErrAlerterNotConfigured is returned when an alerter operation is attempted
// but the alerter has not been properly configured or initialized.
var ErrAlerterNotConfigured = errors.New("alerter not configured")

// ErrAlerterRateLimited is returned when an alerter has been rate limited
// and cannot send additional alerts until the rate limit period has passed.
var ErrAlerterRateLimited = errors.New("alerter rate limited")

// ErrAlerterDropped is returned when an alert message cannot be successfully
// delivered by the alerter. This may occur, for example, if the alerter's
// internal message queue is full and the message is dropped to prevent
// blocking, or if a downstream delivery mechanism (such as a webhook) returns
// a non-2xx HTTP response.
var ErrAlerterDropped = errors.New("alerter message dropped")

// Alerter defines an interface for sending alerts when a monitor detects an issue.
// Implementations of this interface handle the delivery of alert notifications
// through various channels (e.g., email, SMS, webhooks, etc.).
type Alerter interface {
	// Send sends an alert notification for the given monitor with the specified reason and occurrence time.
	// The context ctx can be used to control the request lifetime and cancellation.
	// Returns an error if the alert notification fails to send.
	Send(ctx context.Context, monitor Monitor, reason string, occurredAt time.Time) error
}
