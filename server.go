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
	"sync"
	"time"

	"github.com/guregu/null/v5"
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /uptime-data", s.UptimeDataHandler)
	mux.HandleFunc("POST /checker/register", s.CheckerRegistration)
	mux.HandleFunc("POST /checker/submit", s.CheckerSubmission)
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
	ID   string
	Name string
}

type UptimeDataHistorical struct {
	LatencyMs      int64
	MonitorAge     int
	DailyDowntimes map[int]struct {
		DurationMinutes int `json:"duration_minutes"`
	}
}

type UptimeDataHandlerResponse struct {
	LastUpdated time.Time `json:"last_updated"`
	Monitors    []struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		ResponseTimeMs int64  `json:"response_time_ms"`
		Age            int    `json:"age"`
		Downtimes      map[int]struct {
			DurationMinutes int `json:"duration_minutes"`
		} `json:"downtimes"`
	} `json:"monitors"`
	RetentionDays int `json:"retention_days"`
}

func (s *Server) UptimeDataHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	monitorMetadata, err := s.fetchValidMonitorIds(ctx)
	if err != nil {
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
		Monitors: []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			ResponseTimeMs int64  `json:"response_time_ms"`
			Age            int    `json:"age"`
			Downtimes      map[int]struct {
				DurationMinutes int `json:"duration_minutes"`
			} `json:"downtimes"`
		}{},
		RetentionDays: s.serverConfig.Dataset.RetentionDays,
	}

	for _, monitor := range monitorMetadata {
		if historical, exists := m[monitor.ID]; exists {
			response.Monitors = append(response.Monitors, struct {
				ID             string `json:"id"`
				Name           string `json:"name"`
				ResponseTimeMs int64  `json:"response_time_ms"`
				Age            int    `json:"age"`
				Downtimes      map[int]struct {
					DurationMinutes int `json:"duration_minutes"`
				} `json:"downtimes"`
			}{
				ID:             monitor.ID,
				Name:           monitor.Name,
				ResponseTimeMs: historical.LatencyMs,
				Age:            historical.MonitorAge,
				Downtimes:      historical.DailyDowntimes,
			})
		}
	}

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) fetchValidMonitorIds(ctx context.Context) ([]UptimeDataMonitorMetadata, error) {
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
