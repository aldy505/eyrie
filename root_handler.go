package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

type rootRequestAudience string

const (
	rootRequestAudienceBrowser rootRequestAudience = "browser"
	rootRequestAudienceCLI     rootRequestAudience = "cli"
)

type rootCLIFormat string

const (
	rootCLIFormatJSON rootCLIFormat = "json"
	rootCLIFormatText rootCLIFormat = "text"

	rootRecentEventLimit = 5
)

var rootBrowserMarkers = []string{
	"mozilla",
	"chrome",
	"safari",
	"edge",
	"firefox",
	"opera",
	"chromium",
	"iphone",
	"ipad",
	"android",
	"mobile",
}

type RootStatusSummary struct {
	Services  int `json:"services"`
	Healthy   int `json:"healthy"`
	Degraded  int `json:"degraded"`
	Down      int `json:"down"`
	Incidents int `json:"incidents"`
}

type RootStatusService struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Group                string   `json:"group,omitempty"`
	GroupType            string   `json:"group_type"`
	ProbeType            string   `json:"probe_type"`
	Status               string   `json:"status"`
	Scope                string   `json:"scope"`
	ResponseTimeMs       int64    `json:"response_time_ms"`
	AgeDays              int      `json:"age_days"`
	DowntimeTodayMinutes int      `json:"downtime_today_minutes"`
	Description          string   `json:"description,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	AffectedRegions      []string `json:"affected_regions,omitempty"`
}

type RootStatusEvent struct {
	MonitorID string    `json:"monitor_id"`
	Name      string    `json:"name"`
	EventType string    `json:"event_type"`
	Status    string    `json:"status"`
	Scope     string    `json:"scope"`
	Impact    string    `json:"impact,omitempty"`
	Title     string    `json:"title,omitempty"`
	Body      string    `json:"body,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type RootStatusResponse struct {
	Format       string              `json:"format"`
	Title        string              `json:"title"`
	LastUpdated  time.Time           `json:"last_updated"`
	Summary      RootStatusSummary   `json:"summary"`
	Services     []RootStatusService `json:"services"`
	RecentEvents []RootStatusEvent   `json:"recent_events"`
}

type RootErrorResponse struct {
	Format string `json:"format"`
	Error  string `json:"error"`
}

func (s *Server) RootHandler(spa *spaHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			spa.ServeHTTP(w, r)
			return
		}

		if classifyRootRequest(r.UserAgent()) == rootRequestAudienceBrowser {
			spa.serveIndex(w, r)
			return
		}

		format := resolveRootCLIFormat(r)
		if err := s.acquireDuckDBReadPermit(r.Context()); err != nil {
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.CaptureException(fmt.Errorf("acquiring duckdb read permit: %w", err))
			}
			writeRootError(w, format, http.StatusServiceUnavailable, "request cancelled while waiting for database capacity")
			return
		}
		if s.duckDBReadLimiter != nil {
			defer s.duckDBReadLimiter.Release(1)
		}

		response, err := s.buildRootStatusResponse(r.Context())
		if err != nil {
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.CaptureException(fmt.Errorf("building root status response: %w", err))
			}
			writeRootError(w, format, http.StatusInternalServerError, "failed to build root status response")
			return
		}

		switch format {
		case rootCLIFormatText:
			w.Header().Set("content-type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(renderRootStatusText(response)))
		default:
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			_ = encoder.Encode(response)
		}
	}
}

func classifyRootRequest(userAgent string) rootRequestAudience {
	if userAgent == "" {
		return rootRequestAudienceCLI
	}

	userAgent = strings.ToLower(userAgent)
	for _, marker := range rootBrowserMarkers {
		if strings.Contains(userAgent, marker) {
			return rootRequestAudienceBrowser
		}
	}

	return rootRequestAudienceCLI
}

func resolveRootCLIFormat(r *http.Request) rootCLIFormat {
	switch strings.ToLower(r.URL.Query().Get("format")) {
	case "text", "plain", "ascii":
		return rootCLIFormatText
	case "json":
		return rootCLIFormatJSON
	}

	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/plain") && !strings.Contains(accept, "text/html") {
		return rootCLIFormatText
	}

	return rootCLIFormatJSON
}

func writeRootError(w http.ResponseWriter, format rootCLIFormat, statusCode int, message string) {
	switch format {
	case rootCLIFormatText:
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		w.WriteHeader(statusCode)
		_, _ = fmt.Fprintf(w, "Error: %s\n", message)
	default:
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(statusCode)
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(RootErrorResponse{
			Format: string(rootCLIFormatJSON),
			Error:  message,
		})
	}
}

func (s *Server) buildRootStatusResponse(ctx context.Context) (RootStatusResponse, error) {
	config := s.buildConfigResponse()
	uptime, err := s.buildUptimeDataResponse(ctx)
	if err != nil {
		return RootStatusResponse{}, err
	}

	incidents, err := s.buildMonitorIncidentsResponse(ctx)
	if err != nil {
		return RootStatusResponse{}, err
	}

	recentEvents, err := s.loadRecentIncidentEvents(ctx, rootRecentEventLimit)
	if err != nil {
		return RootStatusResponse{}, err
	}

	incidentByMonitorID := make(map[string]MonitorIncidentSummary, len(incidents.Incidents))
	for _, incident := range incidents.Incidents {
		incidentByMonitorID[incident.MonitorID] = incident
	}

	services := make([]RootStatusService, 0)
	summary := RootStatusSummary{}
	for _, group := range uptime.Monitors {
		for _, monitor := range group.Monitors {
			incident, found := incidentByMonitorID[monitor.ID]
			if !found {
				incident = MonitorIncidentSummary{
					MonitorID: monitor.ID,
					Name:      monitor.Name,
					Status:    MonitorStatusHealthy,
					Scope:     MonitorScopeHealthy,
				}
			}

			service := RootStatusService{
				ID:                   monitor.ID,
				Name:                 monitor.Name,
				GroupType:            string(group.Type),
				ProbeType:            incident.ProbeType,
				Status:               incident.Status,
				Scope:                incident.Scope,
				ResponseTimeMs:       monitor.ResponseTimeMs,
				AgeDays:              monitor.Age,
				DowntimeTodayMinutes: monitorDowntimeToday(monitor),
				Reason:               incident.Reason,
				AffectedRegions:      incident.AffectedRegions,
			}
			if monitor.Description.Valid {
				service.Description = monitor.Description.String
			}
			if group.Type == UptimeDataHandlerMonitorTypeGroup {
				service.Group = group.Name
			}

			services = append(services, service)
			summary.Services++

			switch incident.Status {
			case MonitorStatusDown:
				summary.Down++
			case MonitorStatusDegraded:
				summary.Degraded++
			default:
				summary.Healthy++
			}
		}
	}
	summary.Incidents = summary.Degraded + summary.Down

	title := config.Title
	if title == "" {
		title = "Status Page"
	}

	return RootStatusResponse{
		Format:       string(rootCLIFormatJSON),
		Title:        title,
		LastUpdated:  latestRootUpdate(uptime.LastUpdated, incidents.LastUpdated),
		Summary:      summary,
		Services:     services,
		RecentEvents: recentEvents,
	}, nil
}

func monitorDowntimeToday(monitor UptimeDataHandlerSingleMonitor) int {
	if today, ok := monitor.Downtimes[0]; ok {
		return today.DurationMinutes
	}

	return 0
}

func latestRootUpdate(times ...time.Time) time.Time {
	var latest time.Time
	for _, candidate := range times {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	if latest.IsZero() {
		return time.Now().UTC()
	}
	return latest.UTC()
}

func (s *Server) loadRecentIncidentEvents(ctx context.Context, limit int) ([]RootStatusEvent, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, event_type, impact, title, body, status, scope, created_at
		FROM monitor_incident_events
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying recent incident events: %w", err)
	}
	defer rows.Close()

	events := make([]RootStatusEvent, 0, limit)
	for rows.Next() {
		var event RootStatusEvent
		if err := rows.Scan(
			&event.MonitorID,
			&event.EventType,
			&event.Impact,
			&event.Title,
			&event.Body,
			&event.Status,
			&event.Scope,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning recent incident event: %w", err)
		}

		event.Name = s.monitorName(event.MonitorID)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recent incident events: %w", err)
	}

	return events, nil
}

func (s *Server) monitorName(monitorID string) string {
	for _, monitor := range s.monitorConfig.Monitors {
		if monitor.ID == monitorID {
			return monitor.Name
		}
	}

	return monitorID
}

func renderRootStatusText(response RootStatusResponse) string {
	var builder strings.Builder

	fmt.Fprintf(&builder, "%s\n", response.Title)
	fmt.Fprintf(&builder, "Updated: %s\n", response.LastUpdated.Format(time.RFC3339))
	builder.WriteString("Format : text\n\n")

	builder.WriteString("Summary\n")
	fmt.Fprintf(&builder, "  Services : %d\n", response.Summary.Services)
	fmt.Fprintf(&builder, "  Healthy  : %d\n", response.Summary.Healthy)
	fmt.Fprintf(&builder, "  Degraded : %d\n", response.Summary.Degraded)
	fmt.Fprintf(&builder, "  Down     : %d\n", response.Summary.Down)
	fmt.Fprintf(&builder, "  Incidents: %d\n", response.Summary.Incidents)

	builder.WriteString("\nServices\n")
	if len(response.Services) == 0 {
		builder.WriteString("  No services available.\n")
	} else {
		builder.WriteString("  STATUS    NAME                         LATENCY   AGE   DOWN TODAY   GROUP\n")
		for _, service := range response.Services {
			group := service.Group
			if group == "" {
				group = "-"
			}
			fmt.Fprintf(
				&builder,
				"  %-9s %-28s %-8s %-4s %-12s %s\n",
				strings.ToUpper(service.Status),
				truncateRootText(service.Name, 28),
				formatRootLatency(service.ResponseTimeMs),
				fmt.Sprintf("%dd", service.AgeDays),
				fmt.Sprintf("%dm", service.DowntimeTodayMinutes),
				truncateRootText(group, 20),
			)

			if service.Reason != "" {
				fmt.Fprintf(&builder, "    reason : %s\n", service.Reason)
			}
			if len(service.AffectedRegions) > 0 {
				fmt.Fprintf(&builder, "    regions: %s\n", strings.Join(service.AffectedRegions, ", "))
			}
		}
	}

	if len(response.RecentEvents) > 0 {
		builder.WriteString("\nRecent events\n")
		for _, event := range response.RecentEvents {
			fmt.Fprintf(
				&builder,
				"  - %s  %-8s  %-28s  %s\n",
				event.CreatedAt.Format(time.RFC3339),
				strings.ToUpper(event.EventType),
				truncateRootText(event.Name, 28),
				firstNonEmpty(event.Title, event.Body, "-"),
			)
		}
	}

	return builder.String()
}

func formatRootLatency(latencyMs int64) string {
	if latencyMs <= 0 {
		return "-"
	}

	return fmt.Sprintf("%dms", latencyMs)
}

func truncateRootText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}

	return value[:max-3] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}
