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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/guregu/null/v5"
	"github.com/rs/cors"
	"gocloud.dev/pubsub"
)

type Server struct {
	*http.Server
	db                *sql.DB
	serverConfig      ServerConfig
	monitorConfig     MonitorConfig
	processorProducer *pubsub.Topic
	ingesterProducer  *pubsub.Topic
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
	s := &Server{
		db:                options.Database,
		serverConfig:      options.ServerConfig,
		monitorConfig:     options.MonitorConfig,
		processorProducer: options.ProcessorProducer,
		ingesterProducer:  options.IngesterProducer,
	}

	// Create a sub-filesystem rooted at frontend/dist so we can reference index.html directly
	distFS, err := fs.Sub(frontendFilesystem, "frontend/dist")
	if err != nil {
		return nil, fmt.Errorf("creating sub-filesystem: %w", err)
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
	mux.Handle("GET /uptime-data", corsMiddleware.Handler(sentryMiddleware.HandleFunc(s.UptimeDataHandler)))
	mux.Handle("GET /uptime-data-by-region", corsMiddleware.Handler(sentryMiddleware.HandleFunc(s.UptimeDataByRegionHandler)))
	mux.Handle("POST /checker/register", sentryMiddleware.HandleFunc(s.CheckerRegistration))
	mux.Handle("POST /checker/submit", sentryMiddleware.HandleFunc(s.CheckerSubmission))
	mux.Handle("/", SpaHandler(distFS, "index.html"))

	srv := &http.Server{
		Addr:    net.JoinHostPort(s.serverConfig.Server.Host, strconv.Itoa(s.serverConfig.Server.Port)),
		Handler: mux,
	}

	s.Server = srv

	return s, nil
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

	var monitors []UptimeDataByRegionMonitor
	for region, historicals := range regionData {
		// Group each monitor historicals on a daily bucket
		dailyBucket := make(map[time.Time][]MonitorHistorical)
		for _, mh := range historicals {
			year, month, day := mh.CreatedAt.Date()
			bucketDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			if _, exists := dailyBucket[bucketDate]; !exists {
				dailyBucket[bucketDate] = []MonitorHistorical{}
			}
			dailyBucket[bucketDate] = append(dailyBucket[bucketDate], mh)
		}

		var regionMonitor = UptimeDataByRegionMonitor{
			Region:         region,
			ResponseTimeMs: 0,
			Age:            len(dailyBucket),
			Downtimes: make(map[int]struct {
				DurationMinutes int `json:"duration_minutes"`
			}),
		}

		// Calculate stats for each day
		for date, dayHistoricals := range dailyBucket {
			var totalLatency int64 = 0
			var totalChecks int64 = 0
			var downtimeMinutes int = 0

			for _, mh := range dayHistoricals {
				totalLatency += int64(mh.LatencyMs)
				totalChecks++

				if !slices.Contains(monitorConfig.ExpectedStatusCodes, mh.StatusCode) {
					downtimeMinutes += 1 // assuming each check is done every minute
				}
			}

			averageLatency := totalLatency / totalChecks
			regionMonitor.ResponseTimeMs += averageLatency

			// Calculate downtime index (days ago from now)
			now := time.Now().UTC()
			dailyDowntimesIndex := int(now.Sub(date).Hours() / 24)
			regionMonitor.Downtimes[dailyDowntimesIndex] = struct {
				DurationMinutes int `json:"duration_minutes"`
			}{
				DurationMinutes: downtimeMinutes,
			}
		}

		// Calculate overall average latency for this region
		if len(dailyBucket) > 0 {
			regionMonitor.ResponseTimeMs = regionMonitor.ResponseTimeMs / int64(len(dailyBucket))
		}

		monitors = append(monitors, regionMonitor)
	}

	// Sort monitors by region name for consistent output
	slices.SortStableFunc(monitors, func(a, b UptimeDataByRegionMonitor) int {
		return strings.Compare(a.Region, b.Region)
	})

	response := UptimeDataByRegionResponse{
		LastUpdated: time.Now(),
		Metadata: UptimeDataByRegionMetadata{
			Name:        monitorConfig.Name,
			Description: monitorConfig.Description,
		},
		Monitors: monitors,
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
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
		if err := rows.Scan(&monitorHistorical.Region, &monitorHistorical.StatusCode, &monitorHistorical.LatencyMs, &monitorHistorical.CreatedAt); err != nil {
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

			if !slices.Contains(monitorConfig.ExpectedStatusCodes, mh.StatusCode) {
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
	const (
		minutesPerDay   = 1440
		checksPerMinute = 1
		checksPerDay    = minutesPerDay * checksPerMinute
	)

	span := sentry.StartSpan(ctx, "function", sentry.WithDescription("Fetch From Aggregate Monitor Historical"))
	ctx = span.Context()
	defer span.Finish()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return UptimeDataHistorical{}, fmt.Errorf("getting db connection: %w", err)
	}
	defer conn.Close()

	// Query aggregated data
	rows, err := conn.QueryContext(ctx, `
		SELECT
			date,
			avg_latency_ms,
			success_rate
		FROM
			monitor_historical_daily_aggregate
		WHERE
			monitor_id = ?
		ORDER BY date DESC`,
		monitorId,
	)
	if err != nil {
		return UptimeDataHistorical{}, fmt.Errorf("querying aggregate monitor historical: %w", err)
	}
	defer rows.Close()

	var aggregates []MonitorHistoricalDailyAggregate
	for rows.Next() {
		var aggregate MonitorHistoricalDailyAggregate
		if err := rows.Scan(&aggregate.Date, &aggregate.AvgLatencyMs, &aggregate.SuccessRate); err != nil {
			return UptimeDataHistorical{}, fmt.Errorf("scanning aggregate monitor historical: %w", err)
		}
		aggregates = append(aggregates, aggregate)
	}

	if len(aggregates) == 0 {
		return UptimeDataHistorical{}, fmt.Errorf("no aggregate data found for monitor %s", monitorId)
	}

	var uptimeHistorical = UptimeDataHistorical{
		LatencyMs:  0,
		MonitorAge: len(aggregates),
		DailyDowntimes: make(map[int]struct {
			DurationMinutes int `json:"duration_minutes"`
		}),
	}

	// Calculate average latency and downtime from aggregates
	var totalLatency int64 = 0
	now := time.Now().UTC()

	for _, aggregate := range aggregates {
		totalLatency += int64(aggregate.AvgLatencyMs)

		// Calculate downtime based on success rate
		// If success rate is 80%, that means 20% downtime
		// Assuming checks are done every minute, and there are 1440 minutes in a day
		// downtime_minutes = (100 - success_rate) * 1440 / 100
		downtimeMinutes := (100 - aggregate.SuccessRate) * minutesPerDay / 100

		// Calculate the index for DailyDowntimes map
		// The index represents days ago from now
		// Use date-based calculation to avoid floating-point precision issues
		year, month, day := aggregate.Date.Date()
		aggregateDate := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		nowYear, nowMonth, nowDay := now.Date()
		nowDate := time.Date(nowYear, nowMonth, nowDay, 0, 0, 0, 0, time.UTC)
		dailyDowntimesIndex := int(nowDate.Sub(aggregateDate) / (24 * time.Hour))

		uptimeHistorical.DailyDowntimes[dailyDowntimesIndex] = struct {
			DurationMinutes int `json:"duration_minutes"`
		}{
			DurationMinutes: downtimeMinutes,
		}
	}

	// Calculate overall average latency
	if len(aggregates) > 0 {
		uptimeHistorical.LatencyMs = totalLatency / int64(len(aggregates))
	}

	return uptimeHistorical, nil
}

func (s *Server) fetchMonitorHistoricalGroupedByRegion(ctx context.Context, monitorId string) (map[string][]MonitorHistorical, error) {
	// Fetch monitor historical data grouped by region
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

	// Query all monitor historical data for this monitor
	rows, err := conn.QueryContext(ctx, `
		SELECT
			region,
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
		if err := rows.Scan(&monitorHistorical.Region, &monitorHistorical.StatusCode, &monitorHistorical.LatencyMs, &monitorHistorical.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning monitor historical: %w", err)
		}
		monitorHistoricals = append(monitorHistoricals, monitorHistorical)
	}

	// Group by region and calculate stats for each region
	regionData := make(map[string][]MonitorHistorical)
	for _, mh := range monitorHistoricals {
		regionData[mh.Region] = append(regionData[mh.Region], mh)
	}

	return regionData, nil
}

type CheckerRegistrationRequest struct {
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
	var validChecker bool
	for _, checker := range s.serverConfig.RegisteredCheckers {
		if checker.Region == request.Region && checker.ApiKey == apiKey {
			validChecker = true
			break
		}
	}

	if !validChecker {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(CommonErrorResponse{
			Error: "invalid region",
		})
		return
	}

	if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
		hub.Scope().SetTag("eyrie.checker_region", request.Region)
	}
	if span := sentry.SpanFromContext(r.Context()); span != nil {
		span.SetData("eyrie.checker_region", request.Region)
	}

	slog.InfoContext(r.Context(), "checker registered", slog.String("region", request.Region))
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(s.monitorConfig)
}

type CheckerSubmissionRequest struct {
	MonitorID       string              `json:"monitor_id"`
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
	Title                    string `json:"title"`
	ShowLastUpdated          bool   `json:"show_last_updated"`
	RetentionDays            int    `json:"retention_days"`
	DegradedThresholdMinutes int    `json:"degraded_threshold_minutes"`
	FailureThresholdMinutes  int    `json:"failure_threshold_minutes"`
}

func (s *Server) ConfigHandler(w http.ResponseWriter, r *http.Request) {
	response := ConfigHandlerResponse{
		Title:                    s.serverConfig.Metadata.Title,
		ShowLastUpdated:          s.serverConfig.Metadata.ShowLastUpdated,
		RetentionDays:            s.serverConfig.Dataset.RetentionDays,
		DegradedThresholdMinutes: s.serverConfig.Dataset.DegradedThresholdMinutes,
		FailureThresholdMinutes:  s.serverConfig.Dataset.FailureThresholdMinutes,
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
