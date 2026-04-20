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
)

type IncidentEvaluation struct {
	Status          string
	Scope           string
	Reason          string
	AffectedRegions []string
}

func HealthyIncidentEvaluation() IncidentEvaluation {
	return IncidentEvaluation{Status: MonitorStatusHealthy, Scope: MonitorScopeHealthy, AffectedRegions: []string{}}
}

func (e IncidentEvaluation) Equal(other IncidentEvaluation) bool {
	if e.Status != other.Status || e.Scope != other.Scope || e.Reason != other.Reason {
		return false
	}
	left := append([]string(nil), e.AffectedRegions...)
	right := append([]string(nil), other.AffectedRegions...)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
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
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var regions []string
	if err := json.Unmarshal([]byte(raw), &regions); err != nil {
		return []string{}
	}
	slices.Sort(regions)
	return regions
}
