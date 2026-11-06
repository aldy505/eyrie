package main

import (
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"
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

func TestServerConfigFindRegisteredCheckerRequiresExactKey(t *testing.T) {
	config := ServerConfig{
		RegisteredCheckers: []RegisteredChecker{
			{Name: "public-east", Region: "us-east-1", ApiKey: "correct-key"},
		},
	}

	checker, ok := config.FindRegisteredChecker("public-east", "us-east-1", "correct-key")
	if !ok {
		t.Fatal("expected correct API key to match")
	}
	if checker.Region != "us-east-1" {
		t.Fatalf("expected us-east-1, got %q", checker.Region)
	}

	for _, apiKey := range []string{"correct-ke", "correct-key ", "wrong-key", ""} {
		if _, ok := config.FindRegisteredChecker("public-east", "us-east-1", apiKey); ok {
			t.Fatalf("expected API key %q to be rejected", apiKey)
		}
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

func TestServerConfigValidateAcceptsPostgresDatabaseSettings(t *testing.T) {
	config := ServerConfig{}
	config.Database.URL = "postgres://eyrie:eyrie@localhost:5432/eyrie?sslmode=disable"
	config.Database.MaxConnections = 20
	config.Database.MinConnections = 2
	config.Dataset.PartitionLookaheadDays = 7
	config.Dataset.CleanupIntervalMinutes = 60
	config.Dataset.CleanupBatchSize = 1

	if err := config.Validate(); err != nil {
		t.Fatalf("expected valid database config, got %v", err)
	}
}

func TestServerConfigValidateRejectsInvalidPoolSettings(t *testing.T) {
	config := ServerConfig{}
	config.Database.URL = "postgres://eyrie:eyrie@localhost:5432/eyrie?sslmode=disable"
	config.Database.MaxConnections = 2
	config.Database.MinConnections = 3

	err := config.Validate()
	if err == nil {
		t.Fatal("expected pool size validation error")
	}
	if !strings.Contains(err.Error(), "database.min_connections") {
		t.Fatalf("expected database.min_connections error, got %v", err)
	}
}

func TestDatabaseURLEnvironmentOverride(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://env-user:env-pass@db.example.com:5432/eyrie?sslmode=require") // trufflehog:ignore

	var config ServerConfig
	if err := envconfig.Process("", &config); err != nil {
		t.Fatalf("processing environment: %v", err)
	}
	if config.Database.URL != "postgres://env-user:env-pass@db.example.com:5432/eyrie?sslmode=require" { // trufflehog:ignore
		t.Fatalf("expected DATABASE_URL to populate database.url, got %q", config.Database.URL)
	}
}
