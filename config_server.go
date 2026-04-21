package main

import (
	"fmt"
	"log/slog"
)

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

type RegisteredChecker struct {
	Name   string `yaml:"name"`
	Region string `yaml:"region"`
	ApiKey string `yaml:"api_key"`
}

func (c RegisteredChecker) EffectiveName() string {
	if c.Name != "" {
		return c.Name
	}

	return c.Region
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
	RegisteredCheckers []RegisteredChecker `yaml:"registered_checkers"`
	Database           struct {
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

func (c ServerConfig) Validate() error {
	seenCheckerNames := make(map[string]struct{}, len(c.RegisteredCheckers))
	seenAPIKeys := make(map[string]struct{}, len(c.RegisteredCheckers))
	for _, checker := range c.RegisteredCheckers {
		if checker.Region == "" {
			return fmt.Errorf("registered checker %q: region is required", checker.EffectiveName())
		}
		if checker.ApiKey == "" {
			return fmt.Errorf("registered checker %q: api_key is required", checker.EffectiveName())
		}
		if _, exists := seenCheckerNames[checker.EffectiveName()]; exists {
			return fmt.Errorf("registered checker %q: duplicate checker name", checker.EffectiveName())
		}
		seenCheckerNames[checker.EffectiveName()] = struct{}{}
		if _, exists := seenAPIKeys[checker.ApiKey]; exists {
			return fmt.Errorf("registered checker %q: duplicate api_key", checker.EffectiveName())
		}
		seenAPIKeys[checker.ApiKey] = struct{}{}
	}

	return nil
}

func (c ServerConfig) FindRegisteredChecker(name string, region string, apiKey string) (RegisteredChecker, bool) {
	for _, checker := range c.RegisteredCheckers {
		if checker.ApiKey != apiKey || checker.Region != region {
			continue
		}
		if name != "" && name != checker.EffectiveName() {
			continue
		}

		return checker, true
	}

	return RegisteredChecker{}, false
}
