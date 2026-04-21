package main

import (
	"context"
	"time"

	"github.com/guregu/null/v5"
)

func (c *Checker) probePostgres(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	return c.probeDatabase(ctx, monitor, "pgx", monitor.Postgres.DSN)
}

func (c *Checker) probeMySQL(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	return c.probeDatabase(ctx, monitor, "mysql", monitor.MySQL.DSN)
}

func (c *Checker) probeMSSQL(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	return c.probeDatabase(ctx, monitor, "sqlserver", monitor.MSSQL.DSN)
}

func (c *Checker) probeClickHouse(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	return c.probeDatabase(ctx, monitor, "clickhouse", monitor.ClickHouse.DSN)
}

func (c *Checker) probeDatabase(ctx context.Context, monitor Monitor, driverName string, dsn string) CheckerSubmissionRequest {
	start := time.Now()
	timeout := monitor.EffectiveTimeout(10 * time.Second)
	submission := CheckerSubmissionRequest{
		MonitorID: monitor.ID,
		ProbeType: string(monitor.EffectiveType()),
		Timestamp: time.Now().UTC(),
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	db, err := openSQLDatabase(driverName, dsn)
	if err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}
	defer db.Close()

	if err := db.PingContext(pingCtx); err != nil {
		submission.LatencyMs = time.Since(start).Milliseconds()
		submission.FailureReason = null.StringFrom(err.Error())
		return submission
	}

	submission.LatencyMs = time.Since(start).Milliseconds()
	submission.Success = true
	return submission
}
