package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const partitionMaintenanceLockKey int64 = 0x6579726965

type partitionSpec struct {
	table   string
	dateKey bool
}

var partitionSpecs = []partitionSpec{
	{table: "monitor_historical"},
	{table: "monitor_historical_hourly_aggregate"},
	{table: "monitor_historical_hourly_region_aggregate"},
	{table: "monitor_historical_daily_aggregate", dateKey: true},
	{table: "monitor_historical_region_daily_aggregate", dateKey: true},
}

func normalizeLookaheadDays(days int) int {
	if days <= 0 {
		return 7
	}
	return days
}

func partitionName(table string, day time.Time) string {
	return fmt.Sprintf("%s_p%s", table, utcDayStart(day).Format("20060102"))
}

func partitionDayCacheKey(day time.Time) string {
	return "all|" + utcDayStart(day).Format("20060102")
}

func partitionBounds(spec partitionSpec, day time.Time) (string, string) {
	start := utcDayStart(day)
	end := start.AddDate(0, 0, 1)
	if spec.dateKey {
		return start.Format("2006-01-02"), end.Format("2006-01-02")
	}
	return start.Format("2006-01-02 00:00:00+00"), end.Format("2006-01-02 00:00:00+00")
}

func createPartitionSQL(spec partitionSpec, day time.Time) string {
	from, to := partitionBounds(spec, day)
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
		pgx.Identifier{partitionName(spec.table, day)}.Sanitize(),
		pgx.Identifier{spec.table}.Sanitize(),
		from,
		to,
	)
}

func partitionExists(ctx context.Context, tx pgx.Tx, name string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func listPartitionNames(ctx context.Context, tx pgx.Tx, table string) (map[string]struct{}, error) {
	rows, err := tx.Query(ctx, `
		SELECT child.relname
		FROM pg_inherits
		JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
		JOIN pg_class child ON child.oid = pg_inherits.inhrelid
		JOIN pg_namespace parent_ns ON parent_ns.oid = parent.relnamespace
		JOIN pg_namespace child_ns ON child_ns.oid = child.relnamespace
		WHERE parent.relname = $1
		  AND parent_ns.nspname = current_schema()
		  AND child_ns.nspname = current_schema()
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func parsePartitionDay(table string, name string) (time.Time, bool) {
	prefix := table + "_p"
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation("20060102", strings.TrimPrefix(name, prefix), time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

// EnsurePartitions creates today's partitions and the configured lookahead
// window. It never creates partitions in the past; older partitions that were
// created by previous runs stay in place until retention drops them.
func (s *PostgresStore) EnsurePartitions(ctx context.Context, now time.Time, lookaheadDays int) (int, error) {
	lookaheadDays = normalizeLookaheadDays(lookaheadDays)
	start := utcDayStart(now)
	end := start.AddDate(0, 0, lookaheadDays+1)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning partition maintenance transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, partitionMaintenanceLockKey); err != nil {
		return 0, fmt.Errorf("acquiring partition maintenance lock: %w", err)
	}

	created := 0
	createdNames := make([]string, 0)
	ensuredDays := make([]time.Time, 0)
	for _, spec := range partitionSpecs {
		existing, err := listPartitionNames(ctx, tx, spec.table)
		if err != nil {
			return 0, fmt.Errorf("listing partitions for %s: %w", spec.table, err)
		}
		for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
			if spec.table == partitionSpecs[0].table {
				ensuredDays = append(ensuredDays, day)
			}
			name := partitionName(spec.table, day)
			if _, ok := existing[name]; ok {
				continue
			}
			if _, err := tx.Exec(ctx, createPartitionSQL(spec, day)); err != nil {
				return 0, fmt.Errorf("creating partition %s: %w", name, err)
			}
			existing[name] = struct{}{}
			createdNames = append(createdNames, name)
			created++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing partition maintenance transaction: %w", err)
	}
	for _, name := range createdNames {
		s.markPartitionKnown(name)
	}
	for _, day := range ensuredDays {
		s.markPartitionKnown(partitionDayCacheKey(day))
	}

	return created, nil
}

// EnsureRawPartition verifies that a raw row for at can be inserted. Only the
// current UTC day and the lookahead window are eligible for new partitions.
func (s *PostgresStore) EnsureRawPartition(ctx context.Context, at time.Time, lookaheadDays int) error {
	lookaheadDays = normalizeLookaheadDays(lookaheadDays)
	day := utcDayStart(at)
	today := utcDayStart(time.Now().UTC())

	if day.Before(today) {
		if s.allowHistoricalData {
			return nil
		}
		return ErrHistoricalDataTooOld
	}
	if day.After(today.AddDate(0, 0, lookaheadDays)) {
		return ErrHistoricalDataTooFuture
	}

	if s.partitionKnown(partitionDayCacheKey(day)) {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning raw partition transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, partitionMaintenanceLockKey); err != nil {
		return fmt.Errorf("acquiring raw partition lock: %w", err)
	}

	ensuredNames := make([]string, 0, len(partitionSpecs))
	for _, spec := range partitionSpecs {
		name := partitionName(spec.table, day)
		exists, err := partitionExists(ctx, tx, name)
		if err != nil {
			return fmt.Errorf("checking partition %s: %w", name, err)
		}
		if !exists {
			if _, err := tx.Exec(ctx, createPartitionSQL(spec, day)); err != nil {
				return fmt.Errorf("creating partition %s: %w", name, err)
			}
		}
		ensuredNames = append(ensuredNames, name)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing raw partition transaction: %w", err)
	}

	for _, name := range ensuredNames {
		s.markPartitionKnown(name)
	}
	s.markPartitionKnown(partitionDayCacheKey(day))
	return nil
}

// DropExpiredPartitions drops whole partitions older than the retention
// cutoff. Dropping a partition is O(1) from the application's perspective and
// reclaims its disk space without row-by-row deletes or vacuum.
func (s *PostgresStore) DropExpiredPartitions(ctx context.Context, now time.Time, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, fmt.Errorf("retention days must be greater than 0")
	}
	cutoff := utcDayStart(now.AddDate(0, 0, -retentionDays))

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("beginning partition retention transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, partitionMaintenanceLockKey); err != nil {
		return 0, fmt.Errorf("acquiring partition retention lock: %w", err)
	}

	dropped := 0
	droppedNames := make([]string, 0)
	droppedDays := make(map[time.Time]struct{})
	for _, spec := range partitionSpecs {
		existing, err := listPartitionNames(ctx, tx, spec.table)
		if err != nil {
			return 0, fmt.Errorf("listing partitions for %s: %w", spec.table, err)
		}
		for name := range existing {
			day, ok := parsePartitionDay(spec.table, name)
			if !ok || !day.Before(cutoff) {
				continue
			}
			if _, err := tx.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", pgx.Identifier{name}.Sanitize())); err != nil {
				return 0, fmt.Errorf("dropping expired partition %s: %w", name, err)
			}
			droppedNames = append(droppedNames, name)
			droppedDays[day] = struct{}{}
			dropped++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing partition retention transaction: %w", err)
	}
	for _, name := range droppedNames {
		s.forgetPartition(name)
	}
	for day := range droppedDays {
		s.forgetPartition(partitionDayCacheKey(day))
	}

	return dropped, nil
}

func (s *PostgresStore) ensurePartitionForDay(ctx context.Context, spec partitionSpec, day time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning partition transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, partitionMaintenanceLockKey); err != nil {
		return fmt.Errorf("acquiring partition lock: %w", err)
	}

	name := partitionName(spec.table, day)
	exists, err := partitionExists(ctx, tx, name)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, createPartitionSQL(spec, day)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.markPartitionKnown(name)
	return nil
}
