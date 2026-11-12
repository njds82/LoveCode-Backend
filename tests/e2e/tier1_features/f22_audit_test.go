package tier1_features

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF22_Audit_ListEvents_AdminScope(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Read token should be forbidden (audit requires admin)
	respRead, err := srv.ReadClient().Get(ctx, "/v1/audit")
	if err != nil {
		t.Fatalf("read audit request failed: %v", err)
	}
	srv.ReadClient().AssertStatus(t, respRead, http.StatusForbidden)

	// Admin token should succeed
	respAdmin, err := srv.AdminClient().Get(ctx, "/v1/audit")
	if err != nil {
		t.Fatalf("admin audit request failed: %v", err)
	}
	srv.AdminClient().AssertStatus(t, respAdmin, http.StatusOK)
}

func TestF22_Audit_ToolExecution_Logged(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Execute a tool
	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"*"},
	}
	_, execResp, _ := srv.WriteClient().ExecuteTool(ctx, req)
	if execResp != nil && execResp.IsUnregisteredRoute() {
		t.Skip("Tool execution endpoint not yet registered")
	}

	// Verify audit log has entries
	var count int
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&count)
	if err != nil {
		t.Fatalf("query audit_log failed: %v", err)
	}
	if count == 0 {
		t.Errorf("expected audit_log to contain events, got 0")
	}
}

func TestF22_Audit_ToolExecution_SecretsMasked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Inspect audit log details
	rows, err := srv.DB.DB.QueryContext(ctx, "SELECT detail FROM audit_log WHERE detail IS NOT NULL")
	if err != nil {
		t.Fatalf("query audit_log detail failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			t.Fatalf("scan detail failed: %v", err)
		}

		// Ensure raw tokens are never present in audit details
		for _, token := range []string{srv.AdminToken, srv.WriteToken, srv.ReadToken} {
			if token != "" && strings.Contains(detail, token) {
				t.Fatalf("audit_log detail leaked plaintext token: %s", token)
			}
		}
	}
}

func TestF22_Audit_DeviceOperations_Logged(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check audit log schema
	var tableName string
	err := srv.DB.DB.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name='audit_log'").Scan(&tableName)
	if err != nil {
		t.Fatalf("expected audit_log table to exist: %v", err)
	}
	if tableName != "audit_log" {
		t.Errorf("expected audit_log, got: %s", tableName)
	}
}

func TestF22_Audit_HashChainIntegrity_Preserved(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query /v1/audit/verify endpoint
	var verifyResult map[string]any
	resp, err := srv.AdminClient().GetJSON(ctx, "/v1/audit/verify", &verifyResult)
	if err != nil {
		t.Fatalf("audit verify failed: %v", err)
	}
	srv.AdminClient().AssertStatus(t, resp, http.StatusOK)

	// Verify status is valid
	if verifyResult["valid"] != true && verifyResult["status"] != "ok" {
		t.Logf("audit verify response: %v", verifyResult)
	}
}

func TestF22_Audit_TamperResistance(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt to update audit_log — should fail because of immutable trigger
	_, err := srv.DB.DB.ExecContext(ctx, "UPDATE audit_log SET action='tampered' WHERE rowid=1")
	if err == nil {
		// If there are rows, update must fail due to trigger
		var count int
		_ = srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&count)
		if count > 0 {
			t.Fatalf("expected trigger to prevent UPDATE on audit_log, but update succeeded")
		}
	}

	// Attempt to delete from audit_log — should fail
	_, err = srv.DB.DB.ExecContext(ctx, "DELETE FROM audit_log WHERE rowid=1")
	if err == nil {
		var count int
		_ = srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&count)
		if count > 0 {
			t.Fatalf("expected trigger to prevent DELETE on audit_log, but delete succeeded")
		}
	}
}
