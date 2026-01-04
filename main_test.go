package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

var db *sql.DB

func TestMain(m *testing.M) {
	var err error
	db, err = sql.Open("duckdb", "")
	if err != nil {
		slog.Error("failed to open duckdb", slog.String("error", err.Error()))
		os.Exit(1)
		return
	}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), time.Minute)
	err = Migrate(db, setupCtx, true)
	if err != nil {
		slog.Error("failed to migrate duckdb", slog.String("error", err.Error()))
		setupCancel()
		os.Exit(1)
		return
	}
	setupCancel()

	exitCode := m.Run()
	if err := db.Close(); err != nil {
		slog.Error("failed to close duckdb", slog.String("error", err.Error()))
	}

	os.Exit(exitCode)
}
