package main

import "log/slog"

type DatasetConfig struct {
	RetentionDays                    int     `yaml:"retention_days" default:"90"`
	ProcessingLookbackMinutes        int     `yaml:"processing_lookback_minutes" default:"10"`
	PerRegionFailureThresholdPercent float64 `yaml:"per_region_failure_threshold_percent" default:"40.0"`
	FailureThresholdPercent          float64 `yaml:"failure_threshold_percent" default:"50.0"`
	DegradedThresholdMinutes         int     `yaml:"degraded_threshold_minutes" default:"10"`
	FailureThresholdMinutes          int     `yaml:"failure_threshold_minutes" default:"15"`
}

type WebhookAlertingConfig struct {
	Enabled       bool              `yaml:"enabled"`
	URL           string            `yaml:"url"`
	HmacSecret    string            `yaml:"hmac_secret"`
	CustomHeaders map[string]string `yaml:"custom_headers"`
}

type SlackAlertingConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

type DiscordAlertingConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

type TeamsAlertingConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

type NtfyAlertingConfig struct {
	Enabled     bool   `yaml:"enabled"`
	TopicURL    string `yaml:"topic_url"`
	AccessToken string `yaml:"access_token"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	Priority    int    `yaml:"priority" default:"3"`
}

type ServerConfig struct {
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port" default:"8600"`

		LogLevel slog.Level `yaml:"log_level"`
	} `yaml:"server"`
	Metadata struct {
		Title           string `yaml:"title" default:"Status Page"`
		ShowLastUpdated bool   `yaml:"show_last_updated" default:"true"`
	} `yaml:"metadata"`
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
	Dataset  DatasetConfig `yaml:"dataset"`
	Alerting struct {
		Webhook WebhookAlertingConfig `yaml:"webhook"`
		Slack   SlackAlertingConfig   `yaml:"slack"`
		Discord DiscordAlertingConfig `yaml:"discord"`
		Teams   TeamsAlertingConfig   `yaml:"teams"`
		Ntfy    NtfyAlertingConfig    `yaml:"ntfy"`
	} `yaml:"alerting"`
	Sentry struct {
		Dsn                   string  `yaml:"dsn"`
		ErrorSampleRate       float64 `yaml:"error_sample_rate" default:"1.0"`
		TracesSampleRate      float64 `yaml:"traces_sample_rate" default:"1.0"`
		ProfilingSampleRate   float64 `yaml:"profiling_sample_rate" default:"0.1"`
		Debug                 bool    `yaml:"debug" default:"false"`
		TraceOutgoingRequests bool    `yaml:"trace_outgoing_requests" default:"false"`
	} `yaml:"sentry"`
}
