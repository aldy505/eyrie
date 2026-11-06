package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newPostgresTestStore(t testing.TB) *PostgresStore {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to test postgres: %v", err)
	}

	schema := fmt.Sprintf("eyrie_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("creating test schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parsing test postgres dsn: %v", err)
	}
	poolConfig.MaxConns = 10
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		admin.Close()
		t.Fatalf("creating test postgres pool: %v", err)
	}

	store := NewPostgresStore(pool)
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})

	if err := MigratePostgres(ctx, pool, true); err != nil {
		t.Fatalf("migrating test postgres schema: %v", err)
	}

	return store
}

func TestMigratePostgresCreatesSchemaAndIsIdempotent(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()

	var migrationCount int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("querying schema_migrations: %v", err)
	}
	if migrationCount == 0 {
		t.Fatal("expected at least one applied migration")
	}

	for _, table := range []string{
		"monitor_historical",
		"monitor_historical_hourly_aggregate",
		"monitor_historical_hourly_region_aggregate",
		"monitor_historical_daily_aggregate",
		"monitor_historical_region_daily_aggregate",
		"monitor_incident_state",
		"monitor_incidents",
		"monitor_incident_events",
	} {
		var exists bool
		if err := store.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected table %s to exist", table)
		}
	}

	var partitionedCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_partitioned_table
		JOIN pg_class ON pg_class.oid = pg_partitioned_table.partrelid
		JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
		WHERE pg_namespace.nspname = current_schema()
	`).Scan(&partitionedCount); err != nil {
		t.Fatalf("counting partitioned tables: %v", err)
	}
	if partitionedCount != len(partitionSpecs) {
		t.Fatalf("expected %d partitioned tables, got %d", len(partitionSpecs), partitionedCount)
	}

	if err := MigratePostgres(ctx, store.pool, true); err != nil {
		t.Fatalf("running migrations a second time: %v", err)
	}

	var secondCount int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&secondCount); err != nil {
		t.Fatalf("querying schema_migrations after second run: %v", err)
	}
	if secondCount != migrationCount {
		t.Fatalf("expected migration count to stay %d, got %d", migrationCount, secondCount)
	}
}

func TestEnsurePartitionsCreatesCurrentAndLookahead(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()

	created, err := store.EnsurePartitions(ctx, now, 2)
	if err != nil {
		t.Fatalf("ensuring partitions: %v", err)
	}
	if created != len(partitionSpecs)*3 {
		t.Fatalf("expected %d created partitions, got %d", len(partitionSpecs)*3, created)
	}

	for _, spec := range partitionSpecs {
		for offset := 0; offset <= 2; offset++ {
			day := utcDayStart(now).AddDate(0, 0, offset)
			var exists bool
			if err := store.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, partitionName(spec.table, day)).Scan(&exists); err != nil {
				t.Fatalf("checking partition %s: %v", partitionName(spec.table, day), err)
			}
			if !exists {
				t.Fatalf("expected partition %s to exist", partitionName(spec.table, day))
			}
		}
	}

	createdAgain, err := store.EnsurePartitions(ctx, now, 2)
	if err != nil {
		t.Fatalf("ensuring partitions a second time: %v", err)
	}
	if createdAgain != 0 {
		t.Fatalf("expected no new partitions on second run, got %d", createdAgain)
	}
}

func TestEnsureRawPartitionRejectsPastAndDistantFuture(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()

	if err := store.EnsureRawPartition(ctx, time.Now().UTC().AddDate(0, 0, -1), 7); !errors.Is(err, ErrHistoricalDataTooOld) {
		t.Fatalf("expected ErrHistoricalDataTooOld, got %v", err)
	}
	if err := store.EnsureRawPartition(ctx, time.Now().UTC().AddDate(0, 0, 9), 7); !errors.Is(err, ErrHistoricalDataTooFuture) {
		t.Fatalf("expected ErrHistoricalDataTooFuture, got %v", err)
	}
}

func TestEnsureRawPartitionCreatesToday(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()

	if err := store.EnsureRawPartition(ctx, now, 7); err != nil {
		t.Fatalf("ensuring raw partition: %v", err)
	}

	for _, spec := range partitionSpecs {
		var exists bool
		if err := store.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, partitionName(spec.table, now)).Scan(&exists); err != nil {
			t.Fatalf("checking partition %s: %v", partitionName(spec.table, now), err)
		}
		if !exists {
			t.Fatalf("expected today's %s partition to exist", spec.table)
		}
	}
}

func TestDropExpiredPartitions(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	oldDay := utcDayStart(now).AddDate(0, 0, -100)

	if err := store.ensurePartitionForDay(ctx, partitionSpecs[0], oldDay); err != nil {
		t.Fatalf("creating old partition: %v", err)
	}

	dropped, err := store.DropExpiredPartitions(ctx, now, 90)
	if err != nil {
		t.Fatalf("dropping expired partitions: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("expected 1 dropped partition, got %d", dropped)
	}

	var exists bool
	if err := store.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, partitionName("monitor_historical", oldDay)).Scan(&exists); err != nil {
		t.Fatalf("checking dropped partition: %v", err)
	}
	if exists {
		t.Fatal("expected old partition to be dropped")
	}
}

func TestDropExpiredPartitionsKeepsCutoffDay(t *testing.T) {
	store := newPostgresTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	cutoffDay := utcDayStart(now).AddDate(0, 0, -90)

	if err := store.ensurePartitionForDay(ctx, partitionSpecs[0], cutoffDay); err != nil {
		t.Fatalf("creating cutoff partition: %v", err)
	}

	if _, err := store.DropExpiredPartitions(ctx, now, 90); err != nil {
		t.Fatalf("dropping expired partitions: %v", err)
	}

	var exists bool
	if err := store.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, partitionName("monitor_historical", cutoffDay)).Scan(&exists); err != nil {
		t.Fatalf("checking cutoff partition: %v", err)
	}
	if !exists {
		t.Fatal("expected cutoff day partition to be kept")
	}
}

func TestOpenPostgresStoreRequiresURL(t *testing.T) {
	if _, err := OpenPostgresStore(t.Context(), DatabaseConfig{}); err == nil {
		t.Fatal("expected an error when database.url is empty")
	}
}
