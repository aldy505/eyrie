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
