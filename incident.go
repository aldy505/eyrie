package main

import (
	"encoding/json"
	"slices"
	"strings"
)

const (
	MonitorStatusHealthy  = "healthy"
	MonitorStatusDegraded = "degraded"
	MonitorStatusDown     = "down"

	MonitorScopeHealthy = "healthy"
	MonitorScopeLocal   = "local"
	MonitorScopeGlobal  = "global"

	IncidentSourceProgrammatic = "programmatic"
	IncidentSourceManual       = "manual"

	IncidentLifecycleInvestigating = "investigating"
	IncidentLifecycleIdentified    = "identified"
	IncidentLifecycleMonitoring    = "monitoring"
	IncidentLifecycleResolved      = "resolved"

	IncidentImpactUnknown             = "unknown"
	IncidentImpactOperational         = "operational"
	IncidentImpactMaintenance         = "maintenance"
	IncidentImpactDegradedPerformance = "degraded_performance"
	IncidentImpactPartialOutage       = "partial_outage"
	IncidentImpactMajorOutage         = "major_outage"

	IncidentEventTypeCreated  = "created"
	IncidentEventTypeUpdated  = "updated"
	IncidentEventTypeResolved = "resolved"
)

type IncidentEvaluation struct {
	Status                  string
	Scope                   string
	Reason                  string
	AffectedRegions         []string
	FailureReasonsBreakdown map[string][]string // category -> list of regions
}

func HealthyIncidentEvaluation() IncidentEvaluation {
	return IncidentEvaluation{Status: MonitorStatusHealthy, Scope: MonitorScopeHealthy, AffectedRegions: []string{}, FailureReasonsBreakdown: make(map[string][]string)}
}

func (e IncidentEvaluation) Equal(other IncidentEvaluation) bool {
	if e.Status != other.Status || e.Scope != other.Scope || e.Reason != other.Reason {
		return false
	}
	left := append([]string(nil), e.AffectedRegions...)
	right := append([]string(nil), other.AffectedRegions...)
	slices.Sort(left)
	slices.Sort(right)
	if !slices.Equal(left, right) {
		return false
	}

	// Compare FailureReasonsBreakdown with deterministic ordering
	if len(e.FailureReasonsBreakdown) != len(other.FailureReasonsBreakdown) {
		return false
	}
	for category, regions := range e.FailureReasonsBreakdown {
		otherRegions, ok := other.FailureReasonsBreakdown[category]
		if !ok {
			return false
		}
		if len(regions) != len(otherRegions) {
			return false
		}
		// Sort for comparison since map values can be in different order
		sortedLeft := append([]string(nil), regions...)
		sortedRight := append([]string(nil), otherRegions...)
		slices.Sort(sortedLeft)
		slices.Sort(sortedRight)
		if !slices.Equal(sortedLeft, sortedRight) {
			return false
		}
	}
	return true
}

func (e IncidentEvaluation) RegionsJSON() string {
	regions := append([]string(nil), e.AffectedRegions...)
	slices.Sort(regions)
	body, err := json.Marshal(regions)
	if err != nil {
		return "[]"
	}
	return string(body)
}

func ParseRegionsJSON(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return []string{}
	}
	var regions []string
	if err := json.Unmarshal([]byte(trimmed), &regions); err != nil || regions == nil {
		return []string{}
	}
	slices.Sort(regions)
	return regions
}

func (e IncidentEvaluation) HasActiveIncident() bool {
	return e.Status != MonitorStatusHealthy
}

func (e IncidentEvaluation) Impact() string {
	switch {
	case e.Status == MonitorStatusHealthy:
		return IncidentImpactOperational
	case e.Status == MonitorStatusDown && e.Scope == MonitorScopeGlobal:
		return IncidentImpactMajorOutage
	case e.Status == MonitorStatusDown:
		return IncidentImpactPartialOutage
	case e.Status == MonitorStatusDegraded:
		return IncidentImpactDegradedPerformance
	default:
		return IncidentImpactUnknown
	}
}

func (e IncidentEvaluation) ProgrammaticTitle(monitor Monitor) string {
	switch {
	case e.Status == MonitorStatusDown && e.Scope == MonitorScopeGlobal:
		return monitor.Name + " is experiencing a global outage"
	case e.Status == MonitorStatusDown:
		return monitor.Name + " is partially unavailable"
	case e.Status == MonitorStatusDegraded:
		return monitor.Name + " is experiencing regional degradation"
	default:
		return monitor.Name + " is healthy"
	}
}

func (e IncidentEvaluation) FailureReasonsJSON() string {
	if len(e.FailureReasonsBreakdown) == 0 {
		return "{}"
	}
	body, err := json.Marshal(e.FailureReasonsBreakdown)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func ParseFailureReasonsJSON(raw string) map[string][]string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return make(map[string][]string)
	}
	var breakdown map[string][]string
	if err := json.Unmarshal([]byte(trimmed), &breakdown); err != nil || breakdown == nil {
		return make(map[string][]string)
	}
	return breakdown
}
