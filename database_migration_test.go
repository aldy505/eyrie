package main

import (
	"database/sql"
	"testing"
)

func TestMigrateDropsTimestampDefaults(t *testing.T) {
	tables := []struct {
		tableName  string
		columnName string
	}{
		{tableName: "monitor_historical", columnName: "created_at"},
		{tableName: "monitor_incident_state", columnName: "last_transition_at"},
		{tableName: "monitor_incident_state", columnName: "updated_at"},
		{tableName: "monitor_incidents", columnName: "started_at"},
		{tableName: "monitor_incidents", columnName: "created_at"},
		{tableName: "monitor_incidents", columnName: "updated_at"},
		{tableName: "monitor_incident_events", columnName: "created_at"},
	}

	for _, table := range tables {
		t.Run(table.tableName+"_"+table.columnName, func(t *testing.T) {
			var actual sql.NullString
			err := db.QueryRowContext(t.Context(), `
				SELECT column_default
				FROM information_schema.columns
				WHERE table_name = ? AND column_name = ?
			`, table.tableName, table.columnName).Scan(&actual)
			if err != nil {
				t.Fatalf("failed to inspect %s.%s default: %v", table.tableName, table.columnName, err)
			}

			if actual.Valid {
				t.Fatalf("expected %s.%s to have no default, got %q", table.tableName, table.columnName, actual.String)
			}
		})
	}
}
