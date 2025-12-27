package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/guregu/null/v5"
	"github.com/marcboeker/go-duckdb/v2"
	"gocloud.dev/pubsub"
)

type Server struct {
	connector         *duckdb.Connector
	db                *sql.DB
	serverConfig      ServerConfig
	monitorConfig     MonitorConfig
	processorProducer *pubsub.Topic
	ingesterProducer  *pubsub.Topic
	httpServer        *http.Server
}

func NewServer(config ServerConfig, monitorConfig MonitorConfig) (*Server, error) {
	connector, err := duckdb.NewConnector(config.Database.Path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create duckdb connector: %w", err)
	}

	db := sql.OpenDB(connector)

	if err := Migrate(db, context.Background(), true); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	processorProducer, err := pubsub.OpenTopic(context.Background(), config.TaskQueue.Processor.ProducerAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to open task queue producer: %w", err)
	}

	ingesterProducer, err := pubsub.OpenTopic(context.Background(), config.TaskQueue.Ingester.ProducerAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to open ingester queue producer: %w", err)
	}

	server := &Server{
		connector:         connector,
		db:                db,
		serverConfig:      config,
		monitorConfig:     monitorConfig,
		processorProducer: processorProducer,
		ingesterProducer:  ingesterProducer,
	}

	return server, nil
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /uptime-data", s.UptimeDataHandler)
	mux.HandleFunc("POST /checker/register", s.CheckerRegistration)
	mux.HandleFunc("POST /checker/submit", s.CheckerSubmission)

	srv := &http.Server{
		Addr:    net.JoinHostPort(s.serverConfig.Server.Host, strconv.Itoa(s.serverConfig.Server.Port)),
		Handler: mux,
	}
	s.httpServer = srv
	return srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown http server: %w", err)
		}
	}
	if err := s.processorProducer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to close task queue producer: %w", err)
	}
	if err := s.ingesterProducer.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to close ingester queue producer: %w", err)
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	if err := s.connector.Close(); err != nil {
		return fmt.Errorf("failed to close connector: %w", err)
	}
	return nil
}

type CommonErrorResponse struct {
	Error string `json:"error"`
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
	conn, err := s.db.Conn(ctx)
	if err != nil {
		http.Error(w, "failed to get database connection", http.StatusInternalServerError)
		return
	}
	defer conn.Close()
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
	MonitorID       string            `json:"monitor_id"`
	LatencyMs       int64             `json:"latency_ms"`
	StatusCode      int               `json:"status_code"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    null.String       `json:"response_body,omitempty"`
	TlsVersion      null.String       `json:"tls_version,omitempty"`
	TlsCipher       null.String       `json:"tls_cipher,omitempty"`
	TlsExpiry       null.Time         `json:"tls_expiry,omitempty"`
	Timestamp       time.Time         `json:"timestamp"`
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
			LoggableID: requestId,
			Body:       body,
			Metadata: map[string]string{
				"region":      region,
				"received_at": time.Now().UTC().Format(time.RFC3339),
			},
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to enqueue check submission", slog.String("error", err.Error()))
		}
	})
	wg.Go(func() {
		err := s.ingesterProducer.Send(ctx, &pubsub.Message{
			LoggableID: requestId,
			Body:       body,
			Metadata: map[string]string{
				"region":      region,
				"received_at": time.Now().UTC().Format(time.RFC3339),
			},
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to enqueue ingester submission", slog.String("error", err.Error()))
		}
	})

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
}
