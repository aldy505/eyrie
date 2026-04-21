package main

import (
	"fmt"
	"strings"
)

func isDuckDBWALReplayError(err error) bool {
	if err == nil {
		return false
	}

	message := err.Error()
	return strings.Contains(message, "Failure while replaying WAL file") ||
		strings.Contains(message, "GetDefaultDatabase with no default database set")
}

func duckDBStartupRecoveryHint(databasePath string, err error) error {
	if !isDuckDBWALReplayError(err) {
		return err
	}

	return fmt.Errorf("%w; DuckDB failed while replaying %s.wal. Back up %s and %s.wal first. If the WAL only contains disposable recent changes, remove the WAL and restart. If you need to preserve the WAL contents, recover from a copy by exporting into a fresh database with a newer DuckDB build", err, databasePath, databasePath, databasePath)
}
