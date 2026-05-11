package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/guregu/null/v5"
	"github.com/rs/cors"
	"gocloud.dev/pubsub"
	"golang.org/x/sync/semaphore"
)

type Server struct {
	*http.Server
	db                                  *sql.DB
	serverConfig                        ServerConfig
	monitorConfig                       MonitorConfig
	processorProducer                   *pubsub.Topic
	ingesterProducer                    *pubsub.Topic
	duckDBReadLimiter                   *semaphore.Weighted
	historicalDailyAggregateCache       uptimeAggregateCache[[]MonitorHistoricalDailyAggregate]
	historicalRegionDailyAggregateCache uptimeAggregateCache[[]MonitorHistoricalRegionDailyAggregate]
}

type ServerOptions struct {
	Database          *sql.DB
	ServerConfig      ServerConfig
	MonitorConfig     MonitorConfig
	ProcessorProducer *pubsub.Topic
	IngesterProducer  *pubsub.Topic
}

//go:embed frontend/dist
var frontendFilesystem embed.FS

func NewServer(options ServerOptions) (*Server, error) {
	duckDBReadLimit := resolveDuckDBReadConcurrencyLimit(
		options.ServerConfig.Database.ReadConcurrencyLimit,
		runtime.GOMAXPROCS(0),
	)

	s := &Server{
		db:                options.Database,
		serverConfig:      options.ServerConfig,
		monitorConfig:     options.MonitorConfig,
		processorProducer: options.ProcessorProducer,
		ingesterProducer:  options.IngesterProducer,
		duckDBReadLimiter: semaphore.NewWeighted(duckDBReadLimit),
	}

	var err error
	s.historicalDailyAggregateCache, err = newUptimeAggregateCache[[]MonitorHistoricalDailyAggregate](options.ServerConfig, "uptime:daily", uptimeDataCacheTTL)
	if err != nil {
		return nil, fmt.Errorf("creating daily aggregate cache: %w", err)
	}

	s.historicalRegionDailyAggregateCache, err = newUptimeAggregateCache[[]MonitorHistoricalRegionDailyAggregate](options.ServerConfig, "uptime:region-daily", uptimeDataCacheTTL)
	if err != nil {
		return nil, fmt.Errorf("creating region aggregate cache: %w", err)
	}

	// Create a sub-filesystem rooted at frontend/dist so we can reference index.html directly
	distFS, err := fs.Sub(frontendFilesystem, "frontend/dist")
	if err != nil {
		return nil, fmt.Errorf("creating frontend sub-filesystem: %w", err)
	}

	sentryMiddleware := sentryhttp.New(sentryhttp.Options{
		Repanic:         true,
		WaitForDelivery: true,
		Timeout:         2 * time.Second,
	})

	corsMiddleware := cors.New(cors.Options{
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{http.MethodGet},
	})

	mux := http.NewServeMux()
	mux.Handle("GET /config", corsMiddleware.Handler(sentryMiddleware.HandleFunc(s.ConfigHandler)))
	mux.Handle("GET /uptime-data", corsMiddleware.Handler(sentryMiddleware.HandleFunc(s.withDuckDBReadPermit(s.UptimeDataHandler))))
	mux.Handle("GET /uptime-data-by-region", corsMiddleware.Handler(sentryMiddleware.HandleFunc(s.withDuckDBReadPermit(s.UptimeDataByRegionHandler))))
	mux.Handle("GET /monitor-incidents", corsMiddleware.Handler(sentryMiddleware.HandleFunc(s.withDuckDBReadPermit(s.MonitorIncidentsHandler))))
	mux.Handle("POST /checker/register", sentryMiddleware.HandleFunc(s.CheckerRegistration))
	mux.Handle("POST /checker/submit", sentryMiddleware.HandleFunc(s.CheckerSubmission))
	if os.Getenv("ENABLE_PPROF_ENDPOINT") == "1" {
		mux.Handle("/debug/pprof/", http.DefaultServeMux)
	}
	mux.Handle("/", SpaHandler(distFS, "index.html"))

	srv := &http.Server{
		Addr:    net.JoinHostPort(s.serverConfig.Server.Host, strconv.Itoa(s.serverConfig.Server.Port)),
		Handler: mux,
	}

	s.Server = srv

	return s, nil
}

func resolveDuckDBReadConcurrencyLimit(configured int, gomaxprocs int) int64 {
	if configured > 0 {
		return int64(configured)
	}

	derivedLimit := gomaxprocs / 2
	if derivedLimit < 1 {
		derivedLimit = 1
	}

	return int64(derivedLimit)
}

func aggregateCacheKey(monitorID string, day time.Time) string {
	return monitorID + ":" + utcDayStart(day).Format("2006-01-02")
}

func (s *Server) loadHistoricalDailyAggregatesBeforeDate(ctx context.Context, monitorID string, beforeDate time.Time) ([]MonitorHistoricalDailyAggregate, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_daily_aggregate
		WHERE monitor_id = ? AND date < ?
		ORDER BY date ASC
	`, monitorID, utcDayStart(beforeDate).Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("querying historical daily aggregates: %w", err)
	}
	defer rows.Close()

	var aggregates []MonitorHistoricalDailyAggregate
	for rows.Next() {
		var aggregate MonitorHistoricalDailyAggregate
		if err := rows.Scan(
			&aggregate.MonitorID,
			&aggregate.Date,
			&aggregate.AvgLatencyMs,
			&aggregate.MinLatencyMs,
			&aggregate.MaxLatencyMs,
			&aggregate.SuccessRate,
		); err != nil {
			return nil, fmt.Errorf("scanning historical daily aggregate: %w", err)
		}
		aggregates = append(aggregates, aggregate)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating historical daily aggregates: %w", err)
	}

	return aggregates, nil
}

func (s *Server) loadHistoricalDailyAggregatesForDate(ctx context.Context, monitorID string, day time.Time) ([]MonitorHistoricalDailyAggregate, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_daily_aggregate
		WHERE monitor_id = ? AND date = ?
		ORDER BY date ASC
	`, monitorID, utcDayStart(day).Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("querying current day daily aggregates: %w", err)
	}
	defer rows.Close()

	var aggregates []MonitorHistoricalDailyAggregate
	for rows.Next() {
		var aggregate MonitorHistoricalDailyAggregate
		if err := rows.Scan(
			&aggregate.MonitorID,
			&aggregate.Date,
			&aggregate.AvgLatencyMs,
			&aggregate.MinLatencyMs,
			&aggregate.MaxLatencyMs,
			&aggregate.SuccessRate,
		); err != nil {
			return nil, fmt.Errorf("scanning current day daily aggregate: %w", err)
		}
		aggregates = append(aggregates, aggregate)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating current day daily aggregates: %w", err)
	}

	return aggregates, nil
}

func (s *Server) historicalDailyAggregatesBeforeDate(ctx context.Context, monitorID string, beforeDate time.Time) ([]MonitorHistoricalDailyAggregate, error) {
	if s.historicalDailyAggregateCache == nil {
		return s.loadHistoricalDailyAggregatesBeforeDate(ctx, monitorID, beforeDate)
	}

	key := aggregateCacheKey(monitorID, beforeDate)
	return s.historicalDailyAggregateCache.getOrLoadContext(ctx, key, func(ctx context.Context) ([]MonitorHistoricalDailyAggregate, error) {
		return s.loadHistoricalDailyAggregatesBeforeDate(ctx, monitorID, beforeDate)
	})
}

func (s *Server) loadHistoricalRegionDailyAggregatesBeforeDate(ctx context.Context, monitorID string, beforeDate time.Time) ([]MonitorHistoricalRegionDailyAggregate, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_region_daily_aggregate
		WHERE monitor_id = ? AND date < ?
		ORDER BY date ASC
	`, monitorID, utcDayStart(beforeDate).Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("querying historical region daily aggregates: %w", err)
	}
	defer rows.Close()

	var aggregates []MonitorHistoricalRegionDailyAggregate
	for rows.Next() {
		var aggregate MonitorHistoricalRegionDailyAggregate
		if err := rows.Scan(
			&aggregate.MonitorID,
			&aggregate.Region,
			&aggregate.Date,
			&aggregate.AvgLatencyMs,
			&aggregate.MinLatencyMs,
			&aggregate.MaxLatencyMs,
			&aggregate.SuccessRate,
		); err != nil {
			return nil, fmt.Errorf("scanning historical region daily aggregate: %w", err)
		}
		aggregates = append(aggregates, aggregate)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating historical region daily aggregates: %w", err)
	}

	return aggregates, nil
}

func (s *Server) loadHistoricalRegionDailyAggregatesForDate(ctx context.Context, monitorID string, day time.Time) ([]MonitorHistoricalRegionDailyAggregate, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
		SELECT monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_region_daily_aggregate
		WHERE monitor_id = ? AND date = ?
		ORDER BY date ASC
	`, monitorID, utcDayStart(day).Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("querying current day region daily aggregates: %w", err)
	}
	defer rows.Close()

	var aggregates []MonitorHistoricalRegionDailyAggregate
	for rows.Next() {
		var aggregate MonitorHistoricalRegionDailyAggregate
		if err := rows.Scan(
			&aggregate.MonitorID,
			&aggregate.Region,
			&aggregate.Date,
			&aggregate.AvgLatencyMs,
			&aggregate.MinLatencyMs,
			&aggregate.MaxLatencyMs,
			&aggregate.SuccessRate,
		); err != nil {
			return nil, fmt.Errorf("scanning current day region daily aggregate: %w", err)
		}
		aggregates = append(aggregates, aggregate)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating current day region daily aggregates: %w", err)
	}

	return aggregates, nil
}

func (s *Server) historicalRegionDailyAggregatesBeforeDate(ctx context.Context, monitorID string, beforeDate time.Time) ([]MonitorHistoricalRegionDailyAggregate, error) {
	if s.historicalRegionDailyAggregateCache == nil {
		return s.loadHistoricalRegionDailyAggregatesBeforeDate(ctx, monitorID, beforeDate)
	}

	key := aggregateCacheKey(monitorID, beforeDate)
	return s.historicalRegionDailyAggregateCache.getOrLoadContext(ctx, key, func(ctx context.Context) ([]MonitorHistoricalRegionDailyAggregate, error) {
		return s.loadHistoricalRegionDailyAggregatesBeforeDate(ctx, monitorID, beforeDate)
	})
}

func (s *Server) CloseCaches() error {
	if s == nil {
		return nil
	}

	if s.historicalDailyAggregateCache != nil {
		if err := s.historicalDailyAggregateCache.close(); err != nil {
			return err
		}
	}

	if s.historicalRegionDailyAggregateCache != nil {
		if err := s.historicalRegionDailyAggregateCache.close(); err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) withDuckDBReadPermit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.duckDBReadLimiter == nil {
			next(w, r)
			return
		}

		if err := s.duckDBReadLimiter.Acquire(r.Context(), 1); err != nil {
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.CaptureException(fmt.Errorf("acquiring duckdb read permit: %w", err))
			}
			slog.WarnContext(r.Context(), "request cancelled while waiting for duckdb read capacity", slog.String("error", err.Error()))
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(CommonErrorResponse{
				Error: "request cancelled while waiting for database capacity",
			})
			return
		}
		defer s.duckDBReadLimiter.Release(1)

		next(w, r)
	}
}

type CommonErrorResponse struct {
	Error string `json:"error"`
}

type UptimeDataMonitorMetadata struct {
	ID          string
	Name        string
	Description null.String
}

type UptimeDataHistorical struct {
	LatencyMs      int64
	MonitorAge     int
	DailyDowntimes map[int]struct {
		DurationMinutes int `json:"duration_minutes"`
	}
}

type UptimeDataHandlerSingleMonitor struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Description    null.String `json:"description,omitempty"`
	ResponseTimeMs int64       `json:"response_time_ms"`
	Age            int         `json:"age"`
	Downtimes      map[int]struct {
		DurationMinutes int `json:"duration_minutes"`
	} `json:"downtimes"`
}

type UptimeDataHandlerMonitorType string

const (
	UptimeDataHandlerMonitorTypeSingle UptimeDataHandlerMonitorType = "single"
	UptimeDataHandlerMonitorTypeGroup  UptimeDataHandlerMonitorType = "group"
)

type UptimeDataHandlerMonitorGroup struct {
	Type        UptimeDataHandlerMonitorType     `json:"type"`
	ID          string                           `json:"id"`
	Name        string                           `json:"name"`
	Description null.String                      `json:"description,omitempty"`
	Monitors    []UptimeDataHandlerSingleMonitor `json:"monitors"`
}

type UptimeDataHandlerResponse struct {
	LastUpdated time.Time                       `json:"last_updated"`
	Monitors    []UptimeDataHandlerMonitorGroup `json:"monitors"`
}

type UptimeDataByRegionMetadata struct {
	Name        string      `json:"name"`
	Description null.String `json:"description,omitempty"`
}

type UptimeDataByRegionMonitor struct {
	Region         string `json:"region"`
	ResponseTimeMs int64  `json:"response_time_ms"`
	Age            int    `json:"age"`
	Downtimes      map[int]struct {
		DurationMinutes int `json:"duration_minutes"`
	} `json:"downtimes"`
}

type UptimeDataByRegionResponse struct {
	LastUpdated time.Time                   `json:"last_updated"`
	Metadata    UptimeDataByRegionMetadata  `json:"metadata"`
	Monitors    []UptimeDataByRegionMonitor `json:"monitors"`
}

type MonitorIncidentSummary struct {
	MonitorID               string              `json:"monitor_id"`
	Name                    string              `json:"name"`
	ProbeType               string              `json:"probe_type"`
	Status                  string              `json:"status"`
	Scope                   string              `json:"scope"`
	Reason                  string              `json:"reason"`
	AffectedRegions         []string            `json:"affected_regions"`
	FailureReasonsBreakdown map[string][]string `json:"failure_reasons_breakdown,omitempty"`
	LastTransitionAt        time.Time           `json:"last_transition_at"`
	UpdatedAt               time.Time           `json:"updated_at"`
	IncidentID              string              `json:"incident_id,omitempty"`
	IncidentSource          string              `json:"incident_source,omitempty"`
	IncidentLifecycleState  string              `json:"incident_lifecycle_state,omitempty"`
	IncidentImpact          string              `json:"incident_impact,omitempty"`
	IncidentTitle           string              `json:"incident_title,omitempty"`
}

type MonitorIncidentsResponse struct {
	LastUpdated time.Time                `json:"last_updated"`
	Incidents   []MonitorIncidentSummary `json:"incidents"`
}

func (s *Server) UptimeDataHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	monitorMetadata, err := s.fetchValidMonitorIds(ctx)
	if err != nil {
		if hub := sentry.GetHubFromContext(ctx); hub != nil {
			hub.CaptureException(fmt.Errorf("fetching valid monitor ids: %w", err))
		}
		slog.ErrorContext(ctx, "fetching valid monitor ids", slog.String("error", err.Error()))
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "failed to fetch monitor ids",
		})
		return
	}

	wg := sync.WaitGroup{}
	mutex := sync.Mutex{}
	m := make(map[string]UptimeDataHistorical)

	for _, monitor := range monitorMetadata {
		wg.Go(func() {
			historical, err := s.fetchFromRawMonitorHistorical(ctx, monitor.ID)
			if err != nil {
				if hub := sentry.GetHubFromContext(ctx); hub != nil {
					hub.CaptureException(fmt.Errorf("fetching monitor historical for monitor %s: %w", monitor.ID, err))
				}
				slog.ErrorContext(ctx, "fetching monitor historical", slog.String("monitor_id", monitor.ID), slog.String("error", err.Error()))
				return
			}

			mutex.Lock()
			m[monitor.ID] = historical
			mutex.Unlock()
		})
	}

	wg.Wait()

	response := UptimeDataHandlerResponse{
		LastUpdated: time.Now(),
		Monitors:    []UptimeDataHandlerMonitorGroup{},
	}
	var uptimeDataHandlerMonitorGroup []UptimeDataHandlerMonitorGroup

	// Build an index for monitor metadata to avoid O(n*m) lookups
	monitorIndexByID := make(map[string]int, len(monitorMetadata))
	for i, monitor := range monitorMetadata {
		monitorIndexByID[monitor.ID] = i
	}

	// Prioritize group first
	for _, group := range s.monitorConfig.Groups {
		var groupMonitors []UptimeDataHandlerSingleMonitor
		for _, monitorId := range group.MonitorIDs {
			slog.InfoContext(ctx, "Trying to find monitor id: "+monitorId)
			if idx, ok := monitorIndexByID[monitorId]; ok {
				monitor := monitorMetadata[idx]
				slog.InfoContext(ctx, "Found monitor ID "+monitor.ID+" for group ID "+group.ID)
				if historical, exists := m[monitor.ID]; exists {
					groupMonitors = append(groupMonitors, UptimeDataHandlerSingleMonitor{
						ID:             monitor.ID,
						Name:           monitor.Name,
						Description:    monitor.Description,
						ResponseTimeMs: historical.LatencyMs,
						Age:            historical.MonitorAge,
						Downtimes:      historical.DailyDowntimes,
					})
				}
			}
		}

		uptimeDataHandlerMonitorGroup = append(uptimeDataHandlerMonitorGroup, UptimeDataHandlerMonitorGroup{
			Type:        UptimeDataHandlerMonitorTypeGroup,
			ID:          group.ID,
			Name:        group.Name,
			Description: group.Description,
			Monitors:    groupMonitors,
		})
		slog.InfoContext(ctx, "Added group ID into the response.Monitors")
	}

	// Then add single monitors that are not part of any group
	monitorsInGroups := make(map[string]bool)
	for _, group := range s.monitorConfig.Groups {
		for _, monitorId := range group.MonitorIDs {
			monitorsInGroups[monitorId] = true
		}
	}

	var singleMonitors []UptimeDataHandlerSingleMonitor
	for _, monitor := range monitorMetadata {
		if _, inGroup := monitorsInGroups[monitor.ID]; !inGroup {
			if historical, exists := m[monitor.ID]; exists {
				singleMonitors = append(singleMonitors, UptimeDataHandlerSingleMonitor{
					ID:             monitor.ID,
					Name:           monitor.Name,
					Description:    monitor.Description,
					ResponseTimeMs: historical.LatencyMs,
					Age:            historical.MonitorAge,
					Downtimes:      historical.DailyDowntimes,
				})
			}
		}
	}

	for _, monitor := range singleMonitors {
		uptimeDataHandlerMonitorGroup = append(uptimeDataHandlerMonitorGroup, UptimeDataHandlerMonitorGroup{
			Type:        UptimeDataHandlerMonitorTypeSingle,
			ID:          monitor.ID,
			Name:        monitor.Name,
			Description: monitor.Description,
			Monitors:    []UptimeDataHandlerSingleMonitor{monitor},
		})
	}

	slices.SortStableFunc(uptimeDataHandlerMonitorGroup, func(a UptimeDataHandlerMonitorGroup, b UptimeDataHandlerMonitorGroup) int {
		return strings.Compare(a.ID, b.ID)
	})
	response.Monitors = uptimeDataHandlerMonitorGroup

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) UptimeDataByRegionHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get monitorId from query parameter
	monitorId := r.URL.Query().Get("monitorId")
	if monitorId == "" {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "monitorId query parameter is required",
		})
		return
	}

	// Find the monitor configuration
	var monitorConfig Monitor
	var foundMonitorConfig bool
	for _, config := range s.monitorConfig.Monitors {
		if config.ID == monitorId {
			monitorConfig = config
			foundMonitorConfig = true
			break
		}
	}

	if !foundMonitorConfig {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "monitor not found",
		})
		return
	}

	regionData, err := s.fetchMonitorHistoricalGroupedByRegion(ctx, monitorId)
	if err != nil {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "failed to fetch monitor historical data",
		})
		return
	}

	response := UptimeDataByRegionResponse{
		LastUpdated: time.Now(),
		Metadata: UptimeDataByRegionMetadata{
			Name:        monitorConfig.Name,
			Description: monitorConfig.Description,
		},
		Monitors: regionData,
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) MonitorIncidentsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	monitorMetadata, err := s.fetchValidMonitorIds(ctx)
	if err != nil {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(CommonErrorResponse{Error: "failed to fetch monitor ids"})
		return
	}

	incidents := make([]MonitorIncidentSummary, 0, len(monitorMetadata))
	for _, metadata := range monitorMetadata {
		monitorConfig, err := s.findMonitorConfig(metadata.ID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to load monitor config for incidents response", "monitor_id", metadata.ID, "error", err)
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(CommonErrorResponse{Error: "failed to load incident state"})
			return
		}
		state, err := s.fetchIncidentState(ctx, metadata.ID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to load incident state for monitor", "monitor_id", metadata.ID, "error", err)
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(CommonErrorResponse{Error: "failed to load incident state"})
			return
		}
		activeIncident, activeFound, err := s.fetchActiveIncident(ctx, metadata.ID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to load active incident for monitor", "monitor_id", metadata.ID, "error", err)
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(CommonErrorResponse{Error: "failed to load incident state"})
			return
		}

		summary := MonitorIncidentSummary{
			MonitorID:               metadata.ID,
			Name:                    metadata.Name,
			ProbeType:               string(monitorConfig.EffectiveType()),
			Status:                  state.Status,
			Scope:                   state.Scope,
			Reason:                  state.Reason,
			AffectedRegions:         ParseRegionsJSON(state.AffectedRegions),
			FailureReasonsBreakdown: ParseFailureReasonsJSON(state.FailureReasonsJson),
			LastTransitionAt:        state.LastTransitionAt,
			UpdatedAt:               state.UpdatedAt,
		}
		if activeFound {
			summary.IncidentID = activeIncident.ID
			summary.IncidentSource = activeIncident.Source
			summary.IncidentLifecycleState = activeIncident.LifecycleState
			summary.IncidentImpact = activeIncident.Impact
			summary.IncidentTitle = activeIncident.Title
			if activeIncident.Body != "" {
				summary.Reason = activeIncident.Body
			}
		}
		incidents = append(incidents, summary)
	}

	slices.SortStableFunc(incidents, func(a, b MonitorIncidentSummary) int {
		return strings.Compare(a.MonitorID, b.MonitorID)
	})

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(MonitorIncidentsResponse{
		LastUpdated: time.Now().UTC(),
		Incidents:   incidents,
	})
}

func (s *Server) fetchValidMonitorIds(ctx context.Context) ([]UptimeDataMonitorMetadata, error) {
	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Fetch Valid Monitor IDs"))
	ctx = span.Context()
	defer span.Finish()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring database connection: %w", err)
	}
	defer conn.Close()

	yesterday := time.Now().AddDate(0, 0, -1)
	rows, err := conn.QueryContext(ctx, `
		SELECT DISTINCT monitor_id
		FROM monitor_historical
		WHERE created_at >= ?`, yesterday)
	if err != nil {
		return nil, fmt.Errorf("querying valid monitor ids: %w", err)
	}
	defer rows.Close()

	var monitors []UptimeDataMonitorMetadata
	for rows.Next() {
		var monitor UptimeDataMonitorMetadata
		if err := rows.Scan(&monitor.ID); err != nil {
			return nil, fmt.Errorf("scanning monitor row: %w", err)
		}

		// Find the name from s.monitorConfig
		for _, m := range s.monitorConfig.Monitors {
			if m.ID == monitor.ID {
				monitor.Name = m.Name
				monitor.Description = m.Description
				break
			}
		}
		monitors = append(monitors, monitor)
	}

	return monitors, nil
}

func (s *Server) findMonitorConfig(monitorID string) (Monitor, error) {
	for _, monitor := range s.monitorConfig.Monitors {
		if monitor.ID == monitorID {
			return monitor, nil
		}
	}
	return Monitor{}, ErrMonitorConfigNotFound
}

func (s *Server) fetchIncidentState(ctx context.Context, monitorID string) (MonitorIncidentState, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return MonitorIncidentState{}, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	var state MonitorIncidentState
	err = conn.QueryRowContext(ctx, `
		SELECT monitor_id, status, scope, affected_regions, reason, COALESCE(failure_reasons_json, '{}'), last_transition_at, updated_at
		FROM monitor_incident_state
		WHERE monitor_id = ?
	`, monitorID).Scan(
		&state.MonitorID,
		&state.Status,
		&state.Scope,
		&state.AffectedRegions,
		&state.Reason,
		&state.FailureReasonsJson,
		&state.LastTransitionAt,
		&state.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorIncidentState{
				MonitorID:          monitorID,
				Status:             MonitorStatusHealthy,
				Scope:              MonitorScopeHealthy,
				AffectedRegions:    "[]",
				Reason:             "",
				FailureReasonsJson: "{}",
			}, nil
		}
		return MonitorIncidentState{}, fmt.Errorf("querying incident state: %w", err)
	}

	return state, nil
}

func (s *Server) fetchActiveIncident(ctx context.Context, monitorID string) (MonitorIncident, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return MonitorIncident{}, false, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	var incident MonitorIncident
	err = conn.QueryRowContext(ctx, `
		SELECT id, monitor_id, source, lifecycle_state, auto_impact, impact, auto_title, auto_body, title, body, status, scope, affected_regions, started_at, resolved_at, created_at, updated_at
		FROM monitor_incidents
		WHERE monitor_id = ? AND resolved_at IS NULL
		ORDER BY started_at DESC
		LIMIT 1
	`, monitorID).Scan(
		&incident.ID,
		&incident.MonitorID,
		&incident.Source,
		&incident.LifecycleState,
		&incident.AutoImpact,
		&incident.Impact,
		&incident.AutoTitle,
		&incident.AutoBody,
		&incident.Title,
		&incident.Body,
		&incident.Status,
		&incident.Scope,
		&incident.AffectedRegions,
		&incident.StartedAt,
		&incident.ResolvedAt,
		&incident.CreatedAt,
		&incident.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorIncident{}, false, nil
		}
		return MonitorIncident{}, false, fmt.Errorf("querying active incident: %w", err)
	}

	return incident, true, nil
}

// ErrMonitorConfigNotFound is returned when a monitor configuration cannot be found for a given monitor ID.
var ErrMonitorConfigNotFound = errors.New("monitor config not found")

func (s *Server) fetchFromRawMonitorHistorical(ctx context.Context, monitorId string) (UptimeDataHistorical, error) {
	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Fetch From Raw Monitor Historical"))
	ctx = span.Context()
	defer span.Finish()

	// Try to fetch from aggregate table first (more efficient)
	result, err := s.fetchFromAggregateMonitorHistorical(ctx, monitorId)
	if err == nil {
		return result, nil
	}

	// Fall back to raw data if aggregate is not available
	slog.InfoContext(ctx, "falling back to raw monitor historical", slog.String("monitor_id", monitorId), slog.String("error", err.Error()))

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return UptimeDataHistorical{}, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, `
	SELECT
		region,
		success,
		status_code,
		latency_ms,
		created_at
	FROM
		monitor_historical
	WHERE
		monitor_id = ?`,
		monitorId,
	)
	if err != nil {
		return UptimeDataHistorical{}, fmt.Errorf("querying monitor historical: %w", err)
	}
	defer rows.Close()

	var monitorHistoricals []MonitorHistorical
	for rows.Next() {
		var monitorHistorical MonitorHistorical
		if err := rows.Scan(&monitorHistorical.Region, &monitorHistorical.Success, &monitorHistorical.StatusCode, &monitorHistorical.LatencyMs, &monitorHistorical.CreatedAt); err != nil {
			return UptimeDataHistorical{}, fmt.Errorf("scanning monitor historical: %w", err)
		}
		monitorHistoricals = append(monitorHistoricals, monitorHistorical)
	}

	// Find monitor config for this monitor
	var monitorConfig Monitor
	var foundMonitorConfig bool
	for _, config := range s.monitorConfig.Monitors {
		if config.ID == monitorId {
			monitorConfig = config
			foundMonitorConfig = true
			break
		}
	}

	if !foundMonitorConfig {
		return UptimeDataHistorical{}, ErrMonitorConfigNotFound
	}

	// Group each monitor historicals on a daily bucket
	dailyBucket := make(map[time.Time][]MonitorHistorical)
	for _, mh := range monitorHistoricals {
		year, month, day := mh.CreatedAt.Date()
		bucketDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		if _, exists := dailyBucket[bucketDate]; !exists {
			dailyBucket[bucketDate] = []MonitorHistorical{}
		}
		dailyBucket[bucketDate] = append(dailyBucket[bucketDate], mh)
	}

	var uptimeHistorical = UptimeDataHistorical{
		LatencyMs:  0,
		MonitorAge: len(dailyBucket),
		DailyDowntimes: make(map[int]struct {
			DurationMinutes int `json:"duration_minutes"`
		}),
	}
	// From each bucket, calculate the average latency and downtime
	for date, historicals := range dailyBucket {
		var totalLatency int64 = 0
		var totalChecks int64 = 0
		var downtimeMinutes int = 0

		for _, mh := range historicals {
			totalLatency += int64(mh.LatencyMs)
			totalChecks++

			if !monitorConfig.IsSuccessfulStatus(mh.StatusCode, mh.Success) {
				downtimeMinutes += 1 // assuming each check is done every minute
			}
		}

		averageLatency := totalLatency / totalChecks
		uptimeHistorical.LatencyMs += averageLatency
		// The index for `DailyDowntimes` map is calculated as the number of days since the latest monitor.
		// Today means 0, yesterday means 1, and so on forth.
		// The calculation is done by finding the difference in hours between now and the date, then dividing by 24 to get days.
		now := time.Now().UTC()
		dailyDowntimesIndex := int(now.Sub(date).Hours() / 24)
		uptimeHistorical.DailyDowntimes[int(dailyDowntimesIndex)] = struct {
			DurationMinutes int `json:"duration_minutes"`
		}{
			DurationMinutes: downtimeMinutes,
		}
	}

	// Calculate overall average latency
	uptimeHistorical.LatencyMs = uptimeHistorical.LatencyMs / int64(len(dailyBucket))

	return uptimeHistorical, nil
}

// fetchFromAggregateMonitorHistorical fetches monitor historical data from the aggregate table
func (s *Server) fetchFromAggregateMonitorHistorical(ctx context.Context, monitorId string) (UptimeDataHistorical, error) {
	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Fetch From Aggregate Monitor Historical"))
	ctx = span.Context()
	defer span.Finish()

	today := utcDayStart(time.Now().UTC())
	historicalAggregates, err := s.historicalDailyAggregatesBeforeDate(ctx, monitorId, today)
	if err != nil {
		return UptimeDataHistorical{}, fmt.Errorf("loading historical aggregate monitor data: %w", err)
	}

	todayAggregates, err := s.loadHistoricalDailyAggregatesForDate(ctx, monitorId, today)
	if err != nil {
		return UptimeDataHistorical{}, fmt.Errorf("loading current day aggregate monitor data: %w", err)
	}

	aggregates := append(historicalAggregates, todayAggregates...)
	if len(aggregates) == 0 {
		return UptimeDataHistorical{}, fmt.Errorf("no aggregate data found for monitor %s", monitorId)
	}

	return summarizeUptimeHistoricalAggregates(aggregates), nil
}

func (s *Server) fetchMonitorHistoricalGroupedByRegion(ctx context.Context, monitorId string) ([]UptimeDataByRegionMonitor, error) {
	regionData, err := s.fetchMonitorHistoricalGroupedByRegionFromAggregates(ctx, monitorId)
	if err == nil {
		return regionData, nil
	}

	slog.InfoContext(ctx, "falling back to raw monitor historical by region", slog.String("monitor_id", monitorId), slog.String("error", err.Error()))

	rawRegionData, rawErr := s.fetchMonitorHistoricalGroupedByRegionRaw(ctx, monitorId)
	if rawErr != nil {
		return nil, rawErr
	}

	monitorConfig, err := s.findMonitorConfig(monitorId)
	if err != nil {
		return nil, err
	}

	return summarizeUptimeRegionHistoricalRows(rawRegionData, monitorConfig), nil
}

func (s *Server) fetchMonitorHistoricalGroupedByRegionFromAggregates(ctx context.Context, monitorId string) ([]UptimeDataByRegionMonitor, error) {
	today := utcDayStart(time.Now().UTC())
	historicalAggregates, err := s.historicalRegionDailyAggregatesBeforeDate(ctx, monitorId, today)
	if err != nil {
		return nil, fmt.Errorf("loading historical region aggregate monitor data: %w", err)
	}

	todayAggregates, err := s.loadHistoricalRegionDailyAggregatesForDate(ctx, monitorId, today)
	if err != nil {
		return nil, fmt.Errorf("loading current day region aggregate monitor data: %w", err)
	}

	aggregates := append(historicalAggregates, todayAggregates...)
	if len(aggregates) == 0 {
		return nil, fmt.Errorf("no aggregate data found for monitor %s", monitorId)
	}

	return summarizeRegionHistoricalAggregates(aggregates), nil
}

func (s *Server) fetchMonitorHistoricalGroupedByRegionRaw(ctx context.Context, monitorId string) (map[string][]MonitorHistorical, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		if hub := sentry.GetHubFromContext(ctx); hub != nil {
			hub.CaptureException(fmt.Errorf("acquiring database connection: %w", err))
		}
		return nil, fmt.Errorf("acquiring database connection: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			if hub := sentry.GetHubFromContext(ctx); hub != nil {
				hub.CaptureException(fmt.Errorf("closing database connection: %w", err))
			}
		}
	}()

	rows, err := conn.QueryContext(ctx, `
		SELECT
			region,
			success,
			status_code,
			latency_ms,
			created_at
		FROM
			monitor_historical
		WHERE
			monitor_id = ?`,
		monitorId,
	)
	if err != nil {
		return nil, fmt.Errorf("querying monitor historical: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			if hub := sentry.GetHubFromContext(ctx); hub != nil {
				hub.CaptureException(fmt.Errorf("closing rows: %w", err))
			}
		}
	}()

	var monitorHistoricals []MonitorHistorical
	for rows.Next() {
		var monitorHistorical MonitorHistorical
		if err := rows.Scan(&monitorHistorical.Region, &monitorHistorical.Success, &monitorHistorical.StatusCode, &monitorHistorical.LatencyMs, &monitorHistorical.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning monitor historical: %w", err)
		}
		monitorHistoricals = append(monitorHistoricals, monitorHistorical)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating monitor historical: %w", err)
	}

	regionData := make(map[string][]MonitorHistorical)
	for _, mh := range monitorHistoricals {
		regionData[mh.Region] = append(regionData[mh.Region], mh)
	}

	return regionData, nil
}

type CheckerRegistrationRequest struct {
	Name   string `json:"name,omitempty"`
	Region string `json:"region"`
}

func (s *Server) CheckerRegistration(w http.ResponseWriter, r *http.Request) {
	// Validate API key
	var apiKey = r.Header.Get("X-API-Key")
	var request CheckerRegistrationRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "invalid request body",
		})
		return
	}

	// Validate API key
	checker, validChecker := s.serverConfig.FindRegisteredChecker(request.Name, request.Region, apiKey)
	if !validChecker {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "invalid checker",
		})
		return
	}

	checkerName := checker.EffectiveName()

	if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
		hub.Scope().SetTag("eyrie.checker_region", request.Region)
		hub.Scope().SetTag("eyrie.checker_name", checkerName)
	}
	if span := sentry.SpanFromContext(r.Context()); span != nil {
		span.SetData("eyrie.checker_region", request.Region)
		span.SetData("eyrie.checker_name", checkerName)
	}

	slog.InfoContext(r.Context(), "checker registered", slog.String("name", checkerName), slog.String("region", request.Region))
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(s.monitorConfig.ForChecker(checkerName))
}

type CheckerSubmissionRequest struct {
	MonitorID       string              `json:"monitor_id"`
	ProbeType       string              `json:"probe_type,omitempty"`
	Success         bool                `json:"success"`
	FailureReason   null.String         `json:"failure_reason,omitempty"`
	LatencyMs       int64               `json:"latency_ms"`
	StatusCode      int                 `json:"status_code"`
	ResponseHeaders map[string]string   `json:"response_headers,omitempty"`
	ResponseBody    null.String         `json:"response_body,omitempty"`
	TlsVersion      null.String         `json:"tls_version,omitempty"`
	TlsCipher       null.String         `json:"tls_cipher,omitempty"`
	TlsExpiry       null.Time           `json:"tls_expiry,omitempty"`
	Timestamp       time.Time           `json:"timestamp"`
	Timings         CheckerTraceTimings `json:"timings,omitempty"`
}

func (s *Server) CheckerSubmission(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Validate API Key, figure out which region this checker belongs to
	apiKey := r.Header.Get("X-API-Key")
	var region string
	var validChecker bool
	for _, checker := range s.serverConfig.RegisteredCheckers {
		if checker.ApiKey == apiKey {
			validChecker = true
			region = checker.Region
			break
		}
	}

	if !validChecker {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "invalid API key",
		})
		return
	}

	// Decode request body
	var request CheckerSubmissionRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "invalid request body",
		})
		return
	}

	slog.InfoContext(ctx, "received check results", slog.String("monitor_id", request.MonitorID), slog.String("region", region))
	probeType := request.ProbeType
	if probeType == "" {
		probeType = string(MonitorTypeHTTP)
	}
	sentry.NewMeter(context.Background()).WithCtx(ctx).Count("eyrie.monitor.submissions.received", 1, sentry.WithAttributes(attribute.String("probe_type", probeType), attribute.String("region", region)))
	if request.LatencyMs > 0 {
		sentry.NewMeter(context.Background()).WithCtx(ctx).Distribution("eyrie.monitor.submissions.received_latency", float64(request.LatencyMs), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attribute.String("probe_type", probeType), attribute.String("region", region)))
	}

	// Enqueue the submission to the task queue
	body, err := json.Marshal(request)
	if err != nil {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "failed to marshal request body",
		})
		return
	}

	requestId := fmt.Sprintf("%s-%d", request.MonitorID, time.Now().UnixNano())

	if hub := sentry.GetHubFromContext(ctx); hub != nil {
		hub.Scope().SetTag("eyrie.request_id", requestId)
		hub.Scope().SetTag("eyrie.monitor_id", request.MonitorID)
		hub.Scope().SetTag("eyrie.checker_region", region)

	}
	if span := sentry.SpanFromContext(ctx); span != nil {
		span.SetData("eyrie.request_id", requestId)
		span.SetData("eyrie.monitor_id", request.MonitorID)
		span.SetData("eyrie.checker_region", region)
	}

	wg := sync.WaitGroup{}
	wg.Go(func() {
		err := s.processorProducer.Send(ctx, &pubsub.Message{
			Body: body,
			Metadata: map[string]string{
				"region":      region,
				"received_at": time.Now().UTC().Format(time.RFC3339),
				"request_id":  requestId,
			},
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to enqueue check submission", slog.String("error", err.Error()))
		}
	})
	wg.Go(func() {
		err := s.ingesterProducer.Send(ctx, &pubsub.Message{
			Body: body,
			Metadata: map[string]string{
				"region":      region,
				"received_at": time.Now().UTC().Format(time.RFC3339),
				"request_id":  requestId,
			},
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to enqueue ingester submission", slog.String("error", err.Error()))
		}
	})

	wg.Wait()

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
}

type ConfigHandlerResponse struct {
	Title                               string `json:"title"`
	ShowLastUpdated                     bool   `json:"show_last_updated"`
	RetentionDays                       int    `json:"retention_days"`
	DegradedThresholdMinutes            int    `json:"degraded_threshold_minutes"`
	DegradedThresholdConsecutiveBuckets int    `json:"degraded_threshold_consecutive_buckets"`
	FailureThresholdMinutes             int    `json:"failure_threshold_minutes"`
}

func (s *Server) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	response := ConfigHandlerResponse{
		Title:                               s.serverConfig.Metadata.Title,
		ShowLastUpdated:                     s.serverConfig.Metadata.ShowLastUpdated,
		RetentionDays:                       s.serverConfig.Dataset.RetentionDays,
		DegradedThresholdMinutes:            s.serverConfig.Dataset.DegradedThresholdMinutes,
		DegradedThresholdConsecutiveBuckets: s.serverConfig.Dataset.DegradedThresholdConsecutiveBuckets,
		FailureThresholdMinutes:             s.serverConfig.Dataset.FailureThresholdMinutes,
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
