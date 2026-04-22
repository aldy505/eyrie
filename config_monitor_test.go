package main

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestMonitorConfigValidateCheckerNames(t *testing.T) {
	validCheckers := []RegisteredChecker{
		{Name: "public-east", Region: "us-east-1", ApiKey: "east-key"},
		{Region: "us-west-1", ApiKey: "west-key"},
	}

	validConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:           "postgres",
				Type:         MonitorTypePostgres,
				CheckerNames: []string{"public-east", "us-west-1"},
				Postgres: MonitorPostgresConfig{
					DSN: "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				},
			},
		},
	}

	if err := validConfig.Validate(validCheckers); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}

	invalidConfig := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:           "private-db",
				Type:         MonitorTypePostgres,
				CheckerNames: []string{"missing-checker"},
				Postgres: MonitorPostgresConfig{
					DSN: "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable",
				},
			},
		},
	}

	if err := invalidConfig.Validate(validCheckers); err == nil {
		t.Fatal("expected config validation to fail for unknown checker name")
	}
}

func TestMonitorConfigForChecker(t *testing.T) {
	config := MonitorConfig{
		Monitors: []Monitor{
			{ID: "global-http", Type: MonitorTypeHTTP, HTTP: &MonitorHTTPConfig{URL: "https://example.com"}},
			{ID: "east-only", Type: MonitorTypeTCP, CheckerNames: []string{"public-east"}, TCP: MonitorTCPConfig{Address: "example.com:443"}},
			{ID: "west-only", Type: MonitorTypeTCP, CheckerNames: []string{"public-west"}, TCP: MonitorTCPConfig{Address: "example.net:443"}},
		},
		Groups: []Group{
			{ID: "all", Name: "All", MonitorIDs: []string{"global-http", "east-only", "west-only"}},
		},
	}

	filtered := config.ForChecker("public-east")
	if len(filtered.Monitors) != 2 {
		t.Fatalf("expected 2 monitors for public-east, got %d", len(filtered.Monitors))
	}
	if len(filtered.Groups) != 1 {
		t.Fatalf("expected 1 group after filtering, got %d", len(filtered.Groups))
	}
	if got := filtered.Groups[0].MonitorIDs; len(got) != 2 || got[0] != "global-http" || got[1] != "east-only" {
		t.Fatalf("unexpected filtered group monitor ids: %#v", got)
	}
}

func TestGroupUnmarshalSupportsMonitorsAlias(t *testing.T) {
	var monitorConfig MonitorConfig
	err := yaml.Unmarshal([]byte(`
groups:
  - id: app
    name: App
    monitors:
      - api
      - web
monitors:
  - id: api
    name: API
    type: http
    http:
      url: https://api.example.com/health
  - id: web
    name: Web
    type: http
    http:
      url: https://example.com/health
`), &monitorConfig)
	if err != nil {
		t.Fatalf("failed to unmarshal monitor config: %v", err)
	}

	if len(monitorConfig.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(monitorConfig.Groups))
	}
	if got := monitorConfig.Groups[0].MonitorIDs; len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Fatalf("unexpected group monitor ids: %#v", got)
	}
}

func TestGroupUnmarshalRejectsConflictingAliases(t *testing.T) {
	var monitorConfig MonitorConfig
	err := yaml.Unmarshal([]byte(`
groups:
  - id: app
    name: App
    monitor_ids:
      - api
    monitors:
      - web
monitors:
  - id: api
    name: API
    type: http
    http:
      url: https://api.example.com/health
  - id: web
    name: Web
    type: http
    http:
      url: https://example.com/health
`), &monitorConfig)
	if err == nil {
		t.Fatal("expected unmarshal to fail for conflicting group aliases")
	}
	if !strings.Contains(err.Error(), "monitor_ids and monitors must match") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMonitorConfigValidateGroupReferences(t *testing.T) {
	config := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:   "api",
				Type: MonitorTypeHTTP,
				HTTP: &MonitorHTTPConfig{URL: "https://api.example.com/health"},
			},
		},
		Groups: []Group{
			{ID: "app", Name: "App", MonitorIDs: []string{"api", "missing"}},
		},
	}

	err := config.Validate(nil)
	if err == nil {
		t.Fatal("expected validation to fail for unknown monitor reference")
	}
	if !strings.Contains(err.Error(), `unknown monitor "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMonitorConfigValidateDuplicateGroupReferences(t *testing.T) {
	config := MonitorConfig{
		Monitors: []Monitor{
			{
				ID:   "api",
				Type: MonitorTypeHTTP,
				HTTP: &MonitorHTTPConfig{URL: "https://api.example.com/health"},
			},
		},
		Groups: []Group{
			{ID: "app", Name: "App", MonitorIDs: []string{"api", "api"}},
		},
	}

	err := config.Validate(nil)
	if err == nil {
		t.Fatal("expected validation to fail for duplicate monitor reference")
	}
	if !strings.Contains(err.Error(), `duplicate monitor reference "api"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
