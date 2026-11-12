package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type RawInsertOutcome int

const (
	RawInserted RawInsertOutcome = iota
	RawDuplicate
	RawRejectedOld
	RawRejectedFuture
)

type SuccessCriteria struct {
	ExpectedStatusCodes []int
}

func successCriteriaForMonitor(monitor Monitor) SuccessCriteria {
	if monitor.EffectiveType() == MonitorTypeHTTP {
		return SuccessCriteria{ExpectedStatusCodes: monitor.EffectiveHTTP().ExpectedStatusCodes}
	}
	return SuccessCriteria{}
}

func (c SuccessCriteria) SQL(startArg int) (string, []any) {
	if len(c.ExpectedStatusCodes) == 0 {
		return "CASE WHEN success THEN 1 ELSE 0 END", nil
	}

	placeholders := make([]string, len(c.ExpectedStatusCodes))
	args := make([]any, len(c.ExpectedStatusCodes))
	for i, code := range c.ExpectedStatusCodes {
		placeholders[i] = fmt.Sprintf("$%d", startArg+i)
		args[i] = code
	}
	return fmt.Sprintf("CASE WHEN status_code IN (%s) THEN 1 ELSE 0 END", strings.Join(placeholders, ", ")), args
}

func normalizeRawTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func rawRowKey(monitorID string, region string, createdAt time.Time) string {
	return monitorID + "\x00" + region + "\x00" + normalizeRawTimestamp(createdAt).Format(time.RFC3339Nano)
}

func (s *PostgresStore) InsertMonitorHistorical(ctx context.Context, row MonitorHistorical, lookaheadDays int) (RawInsertOutcome, error) {
	outcomes, err := s.InsertMonitorHistoricalBatch(ctx, []MonitorHistorical{row}, lookaheadDays)
	if err != nil {
		return RawDuplicate, err
	}
	if len(outcomes) == 0 {
		return RawDuplicate, nil
	}
	return outcomes[0], nil
}

// InsertMonitorHistoricalBatch inserts raw rows with one multi-row INSERT ...
// ON CONFLICT DO NOTHING statement. The returned outcomes are aligned with the
// input slice.
func (s *PostgresStore) InsertMonitorHistoricalBatch(ctx context.Context, rows []MonitorHistorical, lookaheadDays int) ([]RawInsertOutcome, error) {
	outcomes := make([]RawInsertOutcome, len(rows))
	if len(rows) == 0 {
		return outcomes, nil
	}

	lookaheadDays = normalizeLookaheadDays(lookaheadDays)
	today := utcDayStart(time.Now().UTC())
	maxDay := today.AddDate(0, 0, lookaheadDays)

	uniqueRows := make([]MonitorHistorical, 0, len(rows))
	uniqueToInput := make([]int, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))

	for i := range rows {
		rows[i].CreatedAt = normalizeRawTimestamp(rows[i].CreatedAt)
		row := rows[i]
		day := utcDayStart(row.CreatedAt)

		switch {
		case day.Before(today) && !s.allowHistoricalData:
			outcomes[i] = RawRejectedOld
			continue
		case day.After(maxDay):
			outcomes[i] = RawRejectedFuture
			continue
		}

		if err := s.EnsureRawPartition(ctx, row.CreatedAt, lookaheadDays); err != nil {
			switch {
			case errors.Is(err, ErrHistoricalDataTooOld):
				outcomes[i] = RawRejectedOld
				continue
			case errors.Is(err, ErrHistoricalDataTooFuture):
				outcomes[i] = RawRejectedFuture
				continue
			default:
				return nil, fmt.Errorf("ensuring raw partition: %w", err)
			}
		}

		key := rawRowKey(row.MonitorID, row.Region, row.CreatedAt)
		if _, ok := seen[key]; ok {
			outcomes[i] = RawDuplicate
			continue
		}
		seen[key] = struct{}{}
		uniqueRows = append(uniqueRows, row)
		uniqueToInput = append(uniqueToInput, i)
	}

	if len(uniqueRows) == 0 {
		return outcomes, nil
	}

	var builder strings.Builder
	builder.WriteString(`
		INSERT INTO monitor_historical (
			monitor_id, region, probe_type, success, failure_reason, status_code, latency_ms,
			response_body, tls_version, tls_cipher, tls_expiry, tls_cert_not_before,
			tls_cert_issuer, tls_cert_subject, tls_cert_dn, tls_cert_fingerprint,
			tls_cert_is_expired, timing_conn_acquired_ms, timing_first_response_byte_ms,
			timing_dns_lookup_start_ms, timing_dns_lookup_done_ms,
			timing_tls_handshake_start_ms, timing_tls_handshake_done_ms, created_at
		) VALUES `)

	args := make([]any, 0, len(uniqueRows)*24)
	for i, row := range uniqueRows {
		if i > 0 {
			builder.WriteString(",")
		}
		base := i * 24
		builder.WriteString("(")
		for column := 0; column < 24; column++ {
			if column > 0 {
				builder.WriteString(",")
			}
			fmt.Fprintf(&builder, "$%d", base+column+1)
		}
		builder.WriteString(")")

		args = append(args,
			row.MonitorID,
			row.Region,
			row.ProbeType,
			row.Success,
			row.FailureReason,
			row.StatusCode,
			row.LatencyMs,
			row.ResponseBody,
			row.TlsVersion,
			row.TlsCipher,
			row.TlsExpiry,
			row.TlsCertNotBefore,
			row.TlsCertIssuer,
			row.TlsCertSubject,
			row.TlsCertDN,
			row.TlsCertFingerprint,
			row.TlsCertIsExpired,
			row.TimingConnAcquiredMs,
			row.TimingFirstResponseByteMs,
			row.TimingDNSLookupStartMs,
			row.TimingDNSLookupDoneMs,
			row.TimingTLSHandshakeStartMs,
			row.TimingTLSHandshakeDoneMs,
			row.CreatedAt,
		)
	}
	builder.WriteString(` ON CONFLICT DO NOTHING RETURNING monitor_id, region, created_at`)

	resultRows, err := s.pool.Query(ctx, builder.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("inserting monitor historical batch: %w", err)
	}
	defer resultRows.Close()

	inserted := make(map[string]struct{}, len(uniqueRows))
	for resultRows.Next() {
		var monitorID, region string
		var createdAt time.Time
		if err := resultRows.Scan(&monitorID, &region, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning inserted monitor historical: %w", err)
		}
		inserted[rawRowKey(monitorID, region, createdAt)] = struct{}{}
	}
	if err := resultRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating inserted monitor historical: %w", err)
	}

	for i, row := range uniqueRows {
		if _, ok := inserted[rawRowKey(row.MonitorID, row.Region, row.CreatedAt)]; ok {
			outcomes[uniqueToInput[i]] = RawInserted
		} else {
			outcomes[uniqueToInput[i]] = RawDuplicate
		}
	}

	return outcomes, nil
}

func (s *PostgresStore) AggregateHourly(ctx context.Context, monitorID string, start time.Time, end time.Time, criteria SuccessCriteria) error {
	successExpression, successArgs := criteria.SQL(4)
	args := []any{monitorID, start.UTC(), end.UTC()}
	args = append(args, successArgs...)

	monitorQuery := fmt.Sprintf(`
		INSERT INTO monitor_historical_hourly_aggregate (
			monitor_id, bucket_start, avg_latency_ms, min_latency_ms, max_latency_ms,
			success_rate, check_count, success_count
		)
		SELECT
			monitor_id,
			date_trunc('hour', created_at) AS bucket_start,
			ROUND(AVG(latency_ms))::integer AS avg_latency_ms,
			MIN(latency_ms) AS min_latency_ms,
			MAX(latency_ms) AS max_latency_ms,
			ROUND(SUM(%s)::numeric * 100 / COUNT(*))::smallint AS success_rate,
			COUNT(*) AS check_count,
			SUM(%s) AS success_count
		FROM monitor_historical
		WHERE monitor_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY monitor_id, date_trunc('hour', created_at)
		ON CONFLICT (monitor_id, bucket_start) DO UPDATE SET
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			min_latency_ms = EXCLUDED.min_latency_ms,
			max_latency_ms = EXCLUDED.max_latency_ms,
			success_rate = EXCLUDED.success_rate,
			check_count = EXCLUDED.check_count,
			success_count = EXCLUDED.success_count
	`, successExpression, successExpression)
	if _, err := s.pool.Exec(ctx, monitorQuery, args...); err != nil {
		return fmt.Errorf("aggregating hourly monitor historical: %w", err)
	}

	regionQuery := fmt.Sprintf(`
		INSERT INTO monitor_historical_hourly_region_aggregate (
			monitor_id, region, bucket_start, avg_latency_ms, min_latency_ms, max_latency_ms,
			success_rate, check_count, success_count
		)
		SELECT
			monitor_id,
			region,
			date_trunc('hour', created_at) AS bucket_start,
			ROUND(AVG(latency_ms))::integer AS avg_latency_ms,
			MIN(latency_ms) AS min_latency_ms,
			MAX(latency_ms) AS max_latency_ms,
			ROUND(SUM(%s)::numeric * 100 / COUNT(*))::smallint AS success_rate,
			COUNT(*) AS check_count,
			SUM(%s) AS success_count
		FROM monitor_historical
		WHERE monitor_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY monitor_id, region, date_trunc('hour', created_at)
		ON CONFLICT (monitor_id, region, bucket_start) DO UPDATE SET
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			min_latency_ms = EXCLUDED.min_latency_ms,
			max_latency_ms = EXCLUDED.max_latency_ms,
			success_rate = EXCLUDED.success_rate,
			check_count = EXCLUDED.check_count,
			success_count = EXCLUDED.success_count
	`, successExpression, successExpression)
	if _, err := s.pool.Exec(ctx, regionQuery, args...); err != nil {
		return fmt.Errorf("aggregating hourly region monitor historical: %w", err)
	}

	return nil
}

func (s *PostgresStore) AggregateDaily(ctx context.Context, monitorID string, day time.Time) error {
	start := utcDayStart(day)
	end := start.AddDate(0, 0, 1)

	monitorQuery := `
		INSERT INTO monitor_historical_daily_aggregate (
			monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms,
			success_rate, check_count, success_count
		)
		SELECT
			monitor_id,
			(bucket_start AT TIME ZONE 'UTC')::date AS date,
			ROUND(SUM(avg_latency_ms::numeric * check_count) / NULLIF(SUM(check_count), 0))::integer AS avg_latency_ms,
			MIN(min_latency_ms) AS min_latency_ms,
			MAX(max_latency_ms) AS max_latency_ms,
			ROUND(SUM(success_count)::numeric * 100 / NULLIF(SUM(check_count), 0))::smallint AS success_rate,
			SUM(check_count) AS check_count,
			SUM(success_count) AS success_count
		FROM monitor_historical_hourly_aggregate
		WHERE monitor_id = $1 AND bucket_start >= $2 AND bucket_start < $3
		GROUP BY monitor_id, (bucket_start AT TIME ZONE 'UTC')::date
		ON CONFLICT (monitor_id, date) DO UPDATE SET
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			min_latency_ms = EXCLUDED.min_latency_ms,
			max_latency_ms = EXCLUDED.max_latency_ms,
			success_rate = EXCLUDED.success_rate,
			check_count = EXCLUDED.check_count,
			success_count = EXCLUDED.success_count
	`
	if _, err := s.pool.Exec(ctx, monitorQuery, monitorID, start, end); err != nil {
		return fmt.Errorf("aggregating daily monitor historical: %w", err)
	}

	regionQuery := `
		INSERT INTO monitor_historical_region_daily_aggregate (
			monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms,
			success_rate, check_count, success_count
		)
		SELECT
			monitor_id,
			region,
			(bucket_start AT TIME ZONE 'UTC')::date AS date,
			ROUND(SUM(avg_latency_ms::numeric * check_count) / NULLIF(SUM(check_count), 0))::integer AS avg_latency_ms,
			MIN(min_latency_ms) AS min_latency_ms,
			MAX(max_latency_ms) AS max_latency_ms,
			ROUND(SUM(success_count)::numeric * 100 / NULLIF(SUM(check_count), 0))::smallint AS success_rate,
			SUM(check_count) AS check_count,
			SUM(success_count) AS success_count
		FROM monitor_historical_hourly_region_aggregate
		WHERE monitor_id = $1 AND bucket_start >= $2 AND bucket_start < $3
		GROUP BY monitor_id, region, (bucket_start AT TIME ZONE 'UTC')::date
		ON CONFLICT (monitor_id, region, date) DO UPDATE SET
			avg_latency_ms = EXCLUDED.avg_latency_ms,
			min_latency_ms = EXCLUDED.min_latency_ms,
			max_latency_ms = EXCLUDED.max_latency_ms,
			success_rate = EXCLUDED.success_rate,
			check_count = EXCLUDED.check_count,
			success_count = EXCLUDED.success_count
	`
	if _, err := s.pool.Exec(ctx, regionQuery, monitorID, start, end); err != nil {
		return fmt.Errorf("aggregating daily region monitor historical: %w", err)
	}

	return nil
}

func (s *PostgresStore) MonitorIDsSince(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT monitor_id
		FROM monitor_historical
		WHERE created_at >= $1
	`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("querying valid monitor ids: %w", err)
	}
	defer rows.Close()

	var monitors []string
	for rows.Next() {
		var monitorID string
		if err := rows.Scan(&monitorID); err != nil {
			return nil, fmt.Errorf("scanning monitor id: %w", err)
		}
		monitors = append(monitors, monitorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating monitor ids: %w", err)
	}
	return monitors, nil
}

func (s *PostgresStore) RawMonitorHistorical(ctx context.Context, monitorID string) ([]MonitorHistorical, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT region, success, status_code, latency_ms, created_at
		FROM monitor_historical
		WHERE monitor_id = $1
	`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("querying monitor historical: %w", err)
	}
	defer rows.Close()

	var results []MonitorHistorical
	for rows.Next() {
		var row MonitorHistorical
		if err := rows.Scan(&row.Region, &row.Success, &row.StatusCode, &row.LatencyMs, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning monitor historical: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating monitor historical: %w", err)
	}
	return results, nil
}

func (s *PostgresStore) LookbackMonitorHistorical(ctx context.Context, monitorID string, since time.Time, until time.Time) ([]MonitorHistorical, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT monitor_id, region, probe_type, success, failure_reason, status_code,
			latency_ms, response_body, tls_version, tls_cipher, tls_expiry, created_at
		FROM monitor_historical
		WHERE monitor_id = $1 AND created_at >= $2 AND created_at <= $3
	`, monitorID, since.UTC(), until.UTC())
	if err != nil {
		return nil, fmt.Errorf("querying monitor historical lookback: %w", err)
	}
	defer rows.Close()

	var results []MonitorHistorical
	for rows.Next() {
		var row MonitorHistorical
		if err := rows.Scan(
			&row.MonitorID,
			&row.Region,
			&row.ProbeType,
			&row.Success,
			&row.FailureReason,
			&row.StatusCode,
			&row.LatencyMs,
			&row.ResponseBody,
			&row.TlsVersion,
			&row.TlsCipher,
			&row.TlsExpiry,
			&row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning monitor historical lookback: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating monitor historical lookback: %w", err)
	}
	return results, nil
}

func (s *PostgresStore) LatestTLSInfo(ctx context.Context, monitorID string) (MonitorHistorical, bool, error) {
	var row MonitorHistorical
	err := s.pool.QueryRow(ctx, `
		SELECT tls_version, tls_cipher, tls_expiry, tls_cert_not_before, tls_cert_issuer,
			tls_cert_subject, tls_cert_dn, tls_cert_fingerprint, COALESCE(tls_cert_is_expired, FALSE)
		FROM monitor_historical
		WHERE monitor_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, monitorID).Scan(
		&row.TlsVersion,
		&row.TlsCipher,
		&row.TlsExpiry,
		&row.TlsCertNotBefore,
		&row.TlsCertIssuer,
		&row.TlsCertSubject,
		&row.TlsCertDN,
		&row.TlsCertFingerprint,
		&row.TlsCertIsExpired,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MonitorHistorical{}, false, nil
		}
		return MonitorHistorical{}, false, fmt.Errorf("querying latest TLS info: %w", err)
	}
	return row, true, nil
}

func (s *PostgresStore) DailyAggregatesBeforeDate(ctx context.Context, monitorID string, beforeDate time.Time) ([]MonitorHistoricalDailyAggregate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_daily_aggregate
		WHERE monitor_id = $1 AND date < $2
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

func (s *PostgresStore) DailyAggregatesForDate(ctx context.Context, monitorID string, day time.Time) ([]MonitorHistoricalDailyAggregate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT monitor_id, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_daily_aggregate
		WHERE monitor_id = $1 AND date = $2
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

func (s *PostgresStore) RegionDailyAggregatesBeforeDate(ctx context.Context, monitorID string, beforeDate time.Time) ([]MonitorHistoricalRegionDailyAggregate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_region_daily_aggregate
		WHERE monitor_id = $1 AND date < $2
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

func (s *PostgresStore) RegionDailyAggregatesForDate(ctx context.Context, monitorID string, day time.Time) ([]MonitorHistoricalRegionDailyAggregate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT monitor_id, region, date, avg_latency_ms, min_latency_ms, max_latency_ms, success_rate
		FROM monitor_historical_region_daily_aggregate
		WHERE monitor_id = $1 AND date = $2
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
