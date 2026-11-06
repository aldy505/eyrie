package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type testConn struct {
	pool *pgxpool.Pool
}

func (c *testConn) ExecContext(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return c.pool.Exec(ctx, rebindTestSQL(query), args...)
}

func (c *testConn) QueryContext(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return c.pool.Query(ctx, rebindTestSQL(query), args...)
}

func (c *testConn) QueryRowContext(ctx context.Context, query string, args ...any) pgx.Row {
	return c.pool.QueryRow(ctx, rebindTestSQL(query), args...)
}

func (c *testConn) Close() error {
	return nil
}

func (s *PostgresStore) Conn(ctx context.Context) (*testConn, error) {
	return &testConn{pool: s.pool}, nil
}

func (s *PostgresStore) ExecContext(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return s.pool.Exec(ctx, rebindTestSQL(query), args...)
}

func (s *PostgresStore) QueryContext(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return s.pool.Query(ctx, rebindTestSQL(query), args...)
}

func (s *PostgresStore) QueryRowContext(ctx context.Context, query string, args ...any) pgx.Row {
	return s.pool.QueryRow(ctx, rebindTestSQL(query), args...)
}

// rebindTestSQL converts the question-mark placeholders used by the existing
// test fixtures into PostgreSQL's numbered placeholders. It ignores question
// marks inside single-quoted string literals.
func rebindTestSQL(query string) string {
	var builder strings.Builder
	builder.Grow(len(query) + 8)

	arg := 1
	inSingleQuote := false
	for i := 0; i < len(query); i++ {
		char := query[i]
		if char == '\'' {
			if inSingleQuote && i+1 < len(query) && query[i+1] == '\'' {
				builder.WriteByte(char)
				builder.WriteByte(query[i+1])
				i++
				continue
			}
			inSingleQuote = !inSingleQuote
			builder.WriteByte(char)
			continue
		}
		if char == '?' && !inSingleQuote {
			fmt.Fprintf(&builder, "$%d", arg)
			arg++
			continue
		}
		builder.WriteByte(char)
	}
	return builder.String()
}
