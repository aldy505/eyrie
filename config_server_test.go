package main

import (
	"strings"
	"testing"
)

func TestServerConfigValidateRejectsDuplicateAPIKeys(t *testing.T) {
	config := ServerConfig{
		RegisteredCheckers: []RegisteredChecker{
			{Name: "public-east", Region: "us-east-1", ApiKey: "shared-key"},
			{Name: "public-west", Region: "us-west-1", ApiKey: "shared-key"},
		},
	}

	err := config.Validate()
	if err == nil {
		t.Fatal("expected duplicate api_key validation error")
	}
	if !strings.Contains(err.Error(), "duplicate api_key") {
		t.Fatalf("expected duplicate api_key error, got %v", err)
	}
}

func TestServerConfigValidateRejectsNegativeReadConcurrencyLimit(t *testing.T) {
	config := ServerConfig{}
	config.Database.ReadConcurrencyLimit = -1

	err := config.Validate()
	if err == nil {
		t.Fatal("expected read_concurrency_limit validation error")
	}
	if !strings.Contains(err.Error(), "database.read_concurrency_limit") {
		t.Fatalf("expected database.read_concurrency_limit error, got %v", err)
	}
}

func TestServerConfigValidateRejectsRedisCacheWithoutAddress(t *testing.T) {
	config := ServerConfig{}
	config.Cache.Backend = "redis"

	err := config.Validate()
	if err == nil {
		t.Fatal("expected redis cache validation error")
	}
	if !strings.Contains(err.Error(), "cache.redis.address") {
		t.Fatalf("expected cache.redis.address error, got %v", err)
	}
}

func TestServerConfigValidateRejectsMissingAlertName(t *testing.T) {
	config := ServerConfig{}
	config.Alerting.Ntfy = []NtfyAlertingConfig{
		{Enabled: true, TopicURL: "https://ntfy.sh/eyrie-alerts"},
	}

	err := config.Validate()
	if err == nil {
		t.Fatal("expected missing alert name validation error")
	}
	if !strings.Contains(err.Error(), "alerting.ntfy[0].name") {
		t.Fatalf("expected alerting.ntfy[0].name error, got %v", err)
	}
}

func TestServerConfigValidateRejectsDuplicateAlertNames(t *testing.T) {
	config := ServerConfig{}
	config.Alerting.Teams = []TeamsAlertingConfig{
		{Name: "team-auth", Enabled: true, WebhookURL: "https://example.com/teams"},
	}
	config.Alerting.Ntfy = []NtfyAlertingConfig{
		{Name: "team-auth", Enabled: true, TopicURL: "https://ntfy.sh/auth"},
	}

	err := config.Validate()
	if err == nil {
		t.Fatal("expected duplicate alert name validation error")
	}
	if !strings.Contains(err.Error(), "duplicate alert name") {
		t.Fatalf("expected duplicate alert name error, got %v", err)
	}
}
