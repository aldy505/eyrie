package main

import (
	"context"
	"errors"
)

var ErrAlerterNotConfigured = errors.New("alerter not configured")
var ErrAlerterRateLimited = errors.New("alerter rate limited")
var ErrAlerterDropped = errors.New("alerter message dropped")

type Alerter interface {
	Send(ctx context.Context, alert AlertMessage) error
}
