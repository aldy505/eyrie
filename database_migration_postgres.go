package main

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/postgres/*.sql
var postgresMigrationFiles embed.FS

const postgresMigrationLockKey int64 = 0x6579726965

// MigratePostgres applies the embedded PostgreSQL migrations. Each migration
// is recorded in schema_migrations so startup is idempotent and concurrent
// server instances cannot apply the same migration twice.
func MigratePostgres(ctx context.Context, pool *pgxpool.Pool, directionUp bool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, postgresMigrationLockKey); err != nil {
		return fmt.Errorf("acquiring migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, postgresMigrationLockKey)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	files, err := postgresMigrationFiles.ReadDir("migrations/postgres")
	if err != nil {
		return fmt.Errorf("reading postgres migrations directory: %w", err)
	}

	names := make([]string, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		names = append(names, file.Name())
	}

	sort.SliceStable(names, func(i, j int) bool {
		first := strings.SplitN(names[i], "_", 2)[0]
		second := strings.SplitN(names[j], "_", 2)[0]
		firstTime, _ := strconv.ParseInt(first, 10, 64)
		secondTime, _ := strconv.ParseInt(second, 10, 64)
		return firstTime < secondTime
	})

	if !directionUp {
		for left, right := 0, len(names)-1; left < right; left, right = left+1, right-1 {
			names[left], names[right] = names[right], names[left]
		}
	}

	for _, name := range names {
		version := strings.TrimSuffix(name, filepath.Ext(name))

		var applied bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
			return fmt.Errorf("checking migration %s: %w", version, err)
		}
		if applied == directionUp {
			continue
		}

		content, err := postgresMigrationFiles.ReadFile("migrations/postgres/" + name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", version, err)
		}
		script := extractGooseSection(string(content), directionUp)

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("beginning migration %s: %w", version, err)
		}

		if err := executeSQLScript(ctx, tx, script); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("executing migration %s: %w", version, err)
		}

		if directionUp {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recording migration %s: %w", version, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("committing migration %s: %w", version, err)
		}
	}

	return nil
}

func extractGooseSection(content string, up bool) string {
	startMarker := "-- +goose Up"
	if !up {
		startMarker = "-- +goose Down"
	}

	var builder strings.Builder
	foundStart := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == startMarker {
			foundStart = true
			continue
		}
		if !foundStart {
			continue
		}
		if trimmed == "-- +goose StatementEnd" {
			break
		}
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	return builder.String()
}

func executeSQLScript(ctx context.Context, tx pgx.Tx, script string) error {
	for _, statement := range splitSQLStatements(script) {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func splitSQLStatements(script string) []string {
	var statements []string
	var builder strings.Builder
	var inSingleQuote, inDoubleQuote bool

	for i := 0; i < len(script); i++ {
		char := script[i]
		switch {
		case char == '\'' && !inDoubleQuote:
			if inSingleQuote && i+1 < len(script) && script[i+1] == '\'' {
				builder.WriteByte(char)
				builder.WriteByte(script[i+1])
				i++
				continue
			}
			inSingleQuote = !inSingleQuote
			builder.WriteByte(char)
		case char == '"' && !inSingleQuote:
			inDoubleQuote = !inDoubleQuote
			builder.WriteByte(char)
		case char == ';' && !inSingleQuote && !inDoubleQuote:
			statements = append(statements, builder.String())
			builder.Reset()
		default:
			builder.WriteByte(char)
		}
	}

	if strings.TrimSpace(builder.String()) != "" {
		statements = append(statements, builder.String())
	}

	return statements
}
