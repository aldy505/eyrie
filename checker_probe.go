package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/denisenkom/go-mssqldb"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type sqlPinger interface {
	PingContext(ctx context.Context) error
	Close() error
}

var (
	openSQLDatabase = func(driverName string, dsn string) (sqlPinger, error) {
		db, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, err
		}
		return db, nil
	}
	pingCommandContext = exec.CommandContext
)

func (c *Checker) probeMonitor(ctx context.Context, monitor Monitor) CheckerSubmissionRequest {
	switch monitor.EffectiveType() {
	case MonitorTypeTCP:
		return c.probeTCP(ctx, monitor)
	case MonitorTypeICMP:
		return c.probeICMP(ctx, monitor)
	case MonitorTypeRedis:
		return c.probeRedis(ctx, monitor)
	case MonitorTypePostgres:
		return c.probePostgres(ctx, monitor)
	case MonitorTypeMySQL:
		return c.probeMySQL(ctx, monitor)
	case MonitorTypeMSSQL:
		return c.probeMSSQL(ctx, monitor)
	case MonitorTypeClickHouse:
		return c.probeClickHouse(ctx, monitor)
	default:
		return c.probeHTTP(ctx, monitor)
	}
}

func sendRedisCommand(conn net.Conn, command string, arguments ...string) error {
	parts := append([]string{command}, arguments...)
	var payload bytes.Buffer
	payload.WriteString(fmt.Sprintf("*%d\r\n", len(parts)))
	for _, part := range parts {
		payload.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(part), part))
	}
	if _, err := conn.Write(payload.Bytes()); err != nil {
		return err
	}

	reply := make([]byte, 4096)
	n, err := conn.Read(reply)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("empty redis reply")
	}
	if reply[0] == '-' {
		return errors.New(strings.TrimSpace(string(reply[:n])))
	}
	return nil
}

func serverNameForAddress(address string) string {
	serverName := address
	if host, _, err := net.SplitHostPort(address); err == nil {
		serverName = host
	}
	return strings.TrimPrefix(strings.TrimSuffix(serverName, "]"), "[")
}
