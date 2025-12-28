package main

import "log/slog"

type ServerConfig struct {
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port" default:"8600"`

		LogLevel slog.Level `yaml:"log_level"`
	} `yaml:"server"`
	RegisteredCheckers []struct {
		Region string `yaml:"region"`
		ApiKey string `yaml:"api_key"`
	} `yaml:"registered_checkers"`
	Database struct {
		Path string `yaml:"path" default:"eyrie.db"`
	} `yaml:"database"`
	TaskQueue struct {
		Processor struct {
			ProducerAddress string `yaml:"producer_address" default:"mem://processor_tasks"`
			ConsumerAddress string `yaml:"consumer_address" default:"mem://processor_tasks"`
		} `yaml:"processor"`
		Ingester struct {
			ProducerAddress string `yaml:"producer_address" default:"mem://ingester_tasks"`
			ConsumerAddress string `yaml:"consumer_address" default:"mem://ingester_tasks"`
		} `yaml:"ingester"`
		Alerter struct {
			ProducerAddress string `yaml:"producer_address" default:"mem://alerter_tasks"`
			ConsumerAddress string `yaml:"consumer_address" default:"mem://alerter_tasks"`
		} `yaml:"alerter"`
	} `yaml:"task_queue"`
	Dataset struct {
		RetentionDays int `yaml:"retention_days" default:"90"`
	} `yaml:"dataset"`
	Alerting struct {
		Webhook struct {
			Enabled    bool   `yaml:"enabled"`
			Url        string `yaml:"url"`
			HmacSecret string `yaml:"hmac_secret"`
		} `yaml:"webhook"`
	} `yaml:"alerting"`
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
