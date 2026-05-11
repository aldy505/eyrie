package main

import "testing"

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

func TestMonitorValidateHTTPClientCertificateRequiresKeyPair(t *testing.T) {
	monitor := Monitor{
		ID:   "http-monitor",
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			URL:            "https://example.com",
			ClientCertPath: "/tmp/client.crt",
		},
	}

	err := monitor.Validate()
	if err == nil || err.Error() != "monitor http-monitor: http.client_cert_path and http.client_key_path must be set together" {
		t.Fatalf("expected client cert/key pair validation error, got %v", err)
	}
}

func TestMonitorValidateHTTPClientKeyPasswordRequiresKey(t *testing.T) {
	monitor := Monitor{
		ID:   "http-monitor",
		Type: MonitorTypeHTTP,
		HTTP: &MonitorHTTPConfig{
			URL:               "https://example.com",
			ClientKeyPassword: "secret",
		},
	}

	err := monitor.Validate()
	if err == nil || err.Error() != "monitor http-monitor: http.client_key_password requires http.client_key_path" {
		t.Fatalf("expected client key password validation error, got %v", err)
	}
}
