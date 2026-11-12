package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrHistoricalDataTooOld is returned when a submission predates the
	// current UTC day. Eyrie only keeps current and future raw partitions, so
	// historical writes are rejected instead of silently creating old data.
	ErrHistoricalDataTooOld = errors.New("historical data is older than the current UTC day")

	// ErrHistoricalDataTooFuture is returned when a submission falls beyond
	// the configured partition lookahead window.
	ErrHistoricalDataTooFuture = errors.New("historical data is beyond the partition lookahead window")
)

// PostgresStore is the concrete PostgreSQL storage implementation. It is used
// directly by the server and workers; there is deliberately no storage
// interface because PostgreSQL is the only supported backend.
type PostgresStore struct {
	pool *pgxpool.Pool

	partitionMu     sync.RWMutex
	knownPartitions map[string]struct{}

	// allowHistoricalData is a test seam used by the existing fixed-date test
	// fixtures. Production never enables it.
	allowHistoricalData bool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{
		pool:            pool,
		knownPartitions: make(map[string]struct{}),
	}
}

func OpenPostgresStore(ctx context.Context, config DatabaseConfig) (*PostgresStore, error) {
	if config.URL == "" {
		return nil, fmt.Errorf("database.url is required")
	}

	poolConfig, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing database.url: %w", err)
	}
	if config.MaxConnections > 0 {
		poolConfig.MaxConns = int32(config.MaxConnections)
	}
	if config.MinConnections > 0 {
		poolConfig.MinConns = int32(config.MinConnections)
	}
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	poolConfig.HealthCheckPeriod = time.Minute
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}

	return NewPostgresStore(pool), nil
}

func (s *PostgresStore) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.pool.Close()
	return nil
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errors.New("postgres store is not initialized")
	}
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) markPartitionKnown(name string) {
	s.partitionMu.Lock()
	defer s.partitionMu.Unlock()
	if s.knownPartitions == nil {
		s.knownPartitions = make(map[string]struct{})
	}
	s.knownPartitions[name] = struct{}{}
}

func (s *PostgresStore) partitionKnown(name string) bool {
	s.partitionMu.RLock()
	defer s.partitionMu.RUnlock()
	_, ok := s.knownPartitions[name]
	return ok
}

func (s *PostgresStore) forgetPartition(name string) {
	s.partitionMu.Lock()
	defer s.partitionMu.Unlock()
	delete(s.knownPartitions, name)
}
