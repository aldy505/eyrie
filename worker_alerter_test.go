package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSelectNamedAlertersReturnsAllWhenNoNamesProvided(t *testing.T) {
	alerters := []NamedAlerter{
		{Name: "team-auth"},
		{Name: "team-ops"},
	}

	selected := selectNamedAlerters(alerters, nil)
	if len(selected) != 2 {
		t.Fatalf("expected all alerters, got %d", len(selected))
	}
}

func TestSelectNamedAlertersIgnoresUnknownNames(t *testing.T) {
	alerters := []NamedAlerter{
		{Name: "team-auth"},
		{Name: "team-ops"},
	}

	selected := selectNamedAlerters(alerters, []string{"team-auth", "missing"})
	if len(selected) != 1 {
		t.Fatalf("expected 1 alerter, got %d", len(selected))
	}
	if selected[0].Name != "team-auth" {
		t.Fatalf("unexpected selected alerter %q", selected[0].Name)
	}
}

func TestDescribeTargetSelection(t *testing.T) {
	alerters := []NamedAlerter{
		{Name: "team-auth"},
	}

	if got := describeTargetSelection(alerters, nil, alerters); got != "all_enabled_targets" {
		t.Fatalf("unexpected default target selection %q", got)
	}
	if got := describeTargetSelection(alerters, []string{"team-auth"}, alerters); got != "matched_named_targets" {
		t.Fatalf("unexpected matched target selection %q", got)
	}
	if got := describeTargetSelection(alerters, []string{"missing"}, nil); got != "no_matching_named_targets" {
		t.Fatalf("unexpected no-match target selection %q", got)
	}
	if got := describeTargetSelection(nil, nil, nil); got != "no_configured_targets" {
		t.Fatalf("unexpected no-configured target selection %q", got)
	}
}

func TestSendAlertToAlertersWrapsErrorWithName(t *testing.T) {
	alerters := []NamedAlerter{
		{Name: "team-auth", Alerter: stubAlerter{err: errors.New("boom")}},
		{Name: "team-ops", Alerter: stubAlerter{}},
	}

	deliveredCount, failedAlerters, err := sendAlertToAlerters(context.Background(), alerters, AlertMessage{Name: "API"})
	if deliveredCount != 1 {
		t.Fatalf("expected one successful delivery, got %d", deliveredCount)
	}
	if len(failedAlerters) != 1 || failedAlerters[0] != "team-auth" {
		t.Fatalf("unexpected failed alerters %#v", failedAlerters)
	}
	if err == nil {
		t.Fatal("expected wrapped delivery error")
	}
	if !strings.Contains(err.Error(), `alerter "team-auth": boom`) {
		t.Fatalf("expected alerter name in error, got %v", err)
	}
}

type stubAlerter struct {
	err error
}

func (s stubAlerter) Send(context.Context, AlertMessage) error {
	return s.err
}
