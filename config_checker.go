package main

type CheckerConfig struct {
	UpstreamURL   string `yaml:"upstream_url" envconfig:"UPSTREAM_URL"`
	Region        string `yaml:"region" envconfig:"REGION"`
	ApiKey        string `yaml:"api_key" envconfig:"API_KEY"`
	OpenTelemetry struct {
		OtlpHttpExporter struct {
			TracesEndpoint  string            `yaml:"traces_endpoint"`
			TracesHeaders   map[string]string `yaml:"traces_headers"`
			LogsEndpoint    string            `yaml:"logs_endpoint"`
			LogsHeaders     map[string]string `yaml:"logs_headers"`
			MetricsEndpoint string            `yaml:"metrics_endpoint"`
			MetricsHeaders  map[string]string `yaml:"metrics_headers"`
		} `yaml:"otlp_http_exporter"`
	} `yaml:"open_telemetry"`
}
