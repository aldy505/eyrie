package main

type CheckerConfig struct {
	UpstreamURL string `yaml:"upstream_url" envconfig:"UPSTREAM_URL"`
	Region      string `yaml:"region" envconfig:"REGION"`
	ApiKey      string `yaml:"api_key" envconfig:"API_KEY"`
	Sentry      struct {
		Dsn                   string  `yaml:"dsn"`
		ErrorSampleRate       float64 `yaml:"error_sample_rate" default:"1.0"`
		TracesSampleRate      float64 `yaml:"traces_sample_rate" default:"1.0"`
		ProfilingSampleRate   float64 `yaml:"profiling_sample_rate" default:"0.1"`
		Debug                 bool    `yaml:"debug" default:"false"`
		TraceOutgoingRequests bool    `yaml:"trace_outgoing_requests" default:"false"`
	} `yaml:"sentry"`
}
