package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var db *PostgresStore

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		slog.Error("failed to connect to test postgres", slog.String("error", err.Error()))
		os.Exit(1)
	}

	schema := fmt.Sprintf("eyrie_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		slog.Error("failed to create test schema", slog.String("error", err.Error()))
		admin.Close()
		os.Exit(1)
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		slog.Error("failed to parse test postgres dsn", slog.String("error", err.Error()))
		admin.Close()
		os.Exit(1)
	}
	poolConfig.MaxConns = 10
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		slog.Error("failed to create test postgres pool", slog.String("error", err.Error()))
		admin.Close()
		os.Exit(1)
	}

	db = NewPostgresStore(pool)
	db.allowHistoricalData = true

	if err := MigratePostgres(ctx, pool, true); err != nil {
		slog.Error("failed to migrate test postgres schema", slog.String("error", err.Error()))
		pool.Close()
		admin.Close()
		os.Exit(1)
	}

	if _, err := db.EnsurePartitions(ctx, time.Now().UTC(), 14); err != nil {
		slog.Error("failed to create current test partitions", slog.String("error", err.Error()))
		pool.Close()
		admin.Close()
		os.Exit(1)
	}

	if err := createDefaultTestPartitions(ctx, pool); err != nil {
		slog.Error("failed to create default test partitions", slog.String("error", err.Error()))
		pool.Close()
		admin.Close()
		os.Exit(1)
	}

	exitCode := m.Run()

	pool.Close()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	cancel()
	admin.Close()

	os.Exit(exitCode)
}

func requireTestDB(t *testing.T) *PostgresStore {
	t.Helper()
	if db == nil {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return db
}

func createDefaultTestPartitions(ctx context.Context, pool *pgxpool.Pool) error {
	for _, spec := range partitionSpecs {
		name := spec.table + "_default"
		if _, err := pool.Exec(ctx, fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s DEFAULT",
			pgx.Identifier{name}.Sanitize(),
			pgx.Identifier{spec.table}.Sanitize(),
		)); err != nil {
			return err
		}
	}
	return nil
}
