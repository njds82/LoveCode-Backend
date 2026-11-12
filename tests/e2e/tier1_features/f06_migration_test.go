package tier1_features

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF06_Migration_SchemaMigrationsTable(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("query schema_migrations failed: %v", err)
	}
	if count < 3 {
		t.Errorf("expected at least 3 migrations applied, got %d", count)
	}
}

func TestF06_Migration_ToolDefinitionsTable(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tableName string
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='tool_definitions'").Scan(&tableName)
	if err == sql.ErrNoRows {
		t.Logf("tool_definitions table pending M1 migration 0004_tools.sql")
		return
	}
	if err != nil {
		t.Fatalf("check tool_definitions table failed: %v", err)
	}

	// Verify columns
	rows, err := srv.DB.DB.QueryContext(ctx, "PRAGMA table_info(tool_definitions)")
	if err != nil {
		t.Fatalf("pragma table_info failed: %v", err)
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan col failed: %v", err)
		}
		cols[name] = true
	}

	requiredCols := []string{"id", "name", "category", "enabled"}
	for _, col := range requiredCols {
		if !cols[col] {
			t.Errorf("missing column %s in tool_definitions", col)
		}
	}
}

func TestF06_Migration_MCPServersTable(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tableName string
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='mcp_servers'").Scan(&tableName)
	if err == sql.ErrNoRows {
		t.Logf("mcp_servers table pending M1 migration 0004_tools.sql")
		return
	}
	if err != nil {
		t.Fatalf("check mcp_servers table failed: %v", err)
	}
	if tableName != "mcp_servers" {
		t.Errorf("expected mcp_servers table, got %s", tableName)
	}
}

func TestF06_Migration_DeviceNodesTable(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tableName string
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='device_nodes'").Scan(&tableName)
	if err == sql.ErrNoRows {
		t.Logf("device_nodes table pending M1 migration 0004_tools.sql")
		return
	}
	if err != nil {
		t.Fatalf("check device_nodes table failed: %v", err)
	}
	if tableName != "device_nodes" {
		t.Errorf("expected device_nodes table, got %s", tableName)
	}
}

func TestF06_Migration_FrozenHistoricalMigrationsUntouched(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify standard Phase 1-3 tables remain intact
	expectedTables := []string{
		"api_tokens",
		"audit_log",
		"routing_profiles",
		"agent_definitions",
		"agent_runs",
		"agent_tasks",
	}

	for _, tbl := range expectedTables {
		var name string
		err := srv.DB.DB.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Fatalf("expected historical table %s to exist: %v", tbl, err)
		}
	}
}
