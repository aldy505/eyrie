package main

type CheckerConfig struct {
	UpstreamURL string `yaml:"upstream_url" envconfig:"UPSTREAM_URL"`
	Region      string `yaml:"region" envconfig:"REGION"`
	ApiKey      string `yaml:"api_key" envconfig:"API_KEY"`
	Sentry      struct {
		Dsn                   string  `yaml:"dsn" envconfig:"SENTRY_DSN"`
		ErrorSampleRate       float64 `yaml:"error_sample_rate" default:"1.0" envconfig:"SENTRY_ERROR_SAMPLE_RATE"`
		TracesSampleRate      float64 `yaml:"traces_sample_rate" default:"1.0" envconfig:"SENTRY_TRACES_SAMPLE_RATE"`
		ProfilingSampleRate   float64 `yaml:"profiling_sample_rate" default:"0.1" envconfig:"SENTRY_PROFILING_SAMPLE_RATE"`
		Debug                 bool    `yaml:"debug" default:"false" envconfig:"SENTRY_DEBUG"`
		TraceOutgoingRequests bool    `yaml:"trace_outgoing_requests" default:"false" envconfig:"SENTRY_TRACE_OUTGOING_REQUESTS"`
	} `yaml:"sentry"`
}
