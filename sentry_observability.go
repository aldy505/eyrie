package main

import (
	"context"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
)

func initSentry(dsn string, errorSampleRate float64, tracesSampleRate float64, debug bool, release string) error {
	return sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		SampleRate:       errorSampleRate,
		EnableTracing:    true,
		TracesSampleRate: tracesSampleRate,
		EnableLogs:       true,
		AttachStacktrace: true,
		Debug:            debug,
		Release:          release,
	})
}

func startSentryTransaction(ctx context.Context, name string, op string) *sentry.Span {
	return sentry.StartTransaction(
		ctx,
		name,
		sentry.WithOpName(op),
		sentry.WithTransactionSource(sentry.SourceCustom),
	)
}

func startSentrySpan(ctx context.Context, op string, description string) *sentry.Span {
	return sentry.StartSpan(
		ctx,
		op,
		sentry.WithDescription(description),
		sentry.WithSpanOrigin(sentry.SpanOriginManual),
	)
}

func sentryCountMetric(ctx context.Context, name string, count int64, attrs ...attribute.Builder) {
	sentry.NewMeter(context.Background()).WithCtx(ctx).Count(name, count, sentry.WithAttributes(attrs...))
}

func sentryGaugeMetric(ctx context.Context, name string, value float64, attrs ...attribute.Builder) {
	sentry.NewMeter(context.Background()).WithCtx(ctx).Gauge(name, value, sentry.WithAttributes(attrs...))
}

func sentryDistributionMetric(ctx context.Context, name string, value float64, unit string, attrs ...attribute.Builder) {
	options := []sentry.MeterOption{sentry.WithAttributes(attrs...)}
	if unit != "" {
		options = append(options, sentry.WithUnit(unit))
	}
	sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution(name, value, options...)
}
