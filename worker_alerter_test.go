package main

import "testing"

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
