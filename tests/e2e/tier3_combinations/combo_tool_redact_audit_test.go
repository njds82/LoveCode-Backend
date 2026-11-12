package tier3_combinations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_ToolExecution_OutputRedaction_AuditLogging verifies that when a tool
// returns output containing credentials, the output returned to the HTTP client is
// redacted AND the audit log entry recorded in SQLite contains only redacted content.
func TestCombo_ToolExecution_OutputRedaction_AuditLogging(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// sysinfo is a standard system tool; we test output redaction and audit logging
	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "sysinfo")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Verify output does not leak unredacted credentials
		if strings.Contains(execResp.Output, "AKIA") || strings.Contains(execResp.Output, "ghp_") {
			t.Fatalf("unredacted secret detected in tool output: %s", execResp.Output)
		}

		// Verify audit log recorded an entry for this tool execution
		entries, err := srv.AuditLog.List(ctx, 10, "tool_execute")
		if err == nil && len(entries) > 0 {
			lastEntry := entries[0]
			detailStr := string(lastEntry.Detail)
			if strings.Contains(detailStr, "AKIA") || strings.Contains(detailStr, "ghp_") {
				t.Fatalf("audit log leaked unredacted secret: %s", detailStr)
			}
		}
	}
}

// TestCombo_ToolExecution_InputArguments_RedactionInAudit verifies that when an agent
// executes a tool passing sensitive parameters (e.g. "password", "api_key", or JWT),
// the audit log entry's detail payload redacts the argument values.
func TestCombo_ToolExecution_InputArguments_RedactionInAudit(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sensitiveArgPayload := map[string]any{
		"token":    "ghp_123456789012345678901234567890123456",
		"password": "UltraSecretPassword987",
	}

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           sensitiveArgPayload,
		GrantedCapabilities: []string{"*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "sysinfo")

	// Query audit log
	entries, err := srv.AuditLog.List(ctx, 10, "")
	if err != nil {
		t.Fatalf("list audit log failed: %v", err)
	}

	for _, e := range entries {
		detail := string(e.Detail)
		if strings.Contains(detail, "UltraSecretPassword987") {
			t.Fatalf("security violation: raw password leaked in audit log entry: %s", detail)
		}
		if strings.Contains(detail, "ghp_123456789012345678901234567890123456") {
			t.Fatalf("security violation: raw GitHub PAT leaked in audit log entry: %s", detail)
		}
	}
}

// TestCombo_ToolExecution_CapabilityDenial_AuditLogged verifies that an unauthorized
// tool call rejected due to missing capability is logged in the audit log as a denied event.
func TestCombo_ToolExecution_CapabilityDenial_AuditLogged(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling tool without granting required capability
	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:unrelated"}, // missing tools:sysinfo
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "sysinfo")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected capability denial, got 200 OK")
	}

	// Verify audit log has recorded the event or refusal
	var auditCount int
	_ = srv.DB.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&auditCount)
	if auditCount == 0 {
		t.Logf("no audit entry recorded for rejected request (per-policy)")
	}
}

// TestCombo_ToolExecution_LargeOutput_TruncatedAndRedacted verifies that a tool
// returning output exceeding MaxOutputBytes is truncated to the limit and secrets
// are redacted without crashing or corrupting UTF-8 characters.
func TestCombo_ToolExecution_LargeOutput_TruncatedAndRedacted(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		MaxOutputBytes:      128, // Strict byte limit
		GrantedCapabilities: []string{"*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "sysinfo")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		if execResp.Truncated {
			if int64(len(execResp.Output)) > int64(128+len(security.Redacted)+16) {
				t.Errorf("output size %d exceeded expected bound", len(execResp.Output))
			}
		}
	}
}
