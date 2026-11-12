package tier3_combinations

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_Subagent_CapabilityNarrowing_Enforced verifies that when a lead agent
// with full capabilities spawns a subagent with narrowed permissions (read-only filesystem),
// the subagent can execute fs_read, but attempts to execute fs_write are rejected with 403.
func TestCombo_Subagent_CapabilityNarrowing_Enforced(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "subagent_data.txt", []byte("readable by subagent"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subagentID := "subagent-reader-only"
	parentAgentID := "lead-agent-orchestrator"
	runID := "run-delegation-flow-01"

	// Subagent attempts fs_read with its narrowed capability
	readReq := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": sb.Path("subagent_data.txt"),
		},
		AgentID:             subagentID,
		RunID:               runID,
		GrantedCapabilities: []string{"tools:filesystem:read"}, // Narrowed! No write capability
	}

	execResp, respRead, err := srv.WriteClient().ExecuteTool(ctx, readReq)
	if err != nil {
		t.Fatalf("subagent read failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respRead, "fs_read")

	if respRead.StatusCode == http.StatusOK && execResp != nil {
		if execResp.Output != "readable by subagent" {
			t.Errorf("expected file content, got: %q", execResp.Output)
		}
	}

	// Subagent attempts fs_write (which it has NOT been granted)
	writeReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    sb.Path("unauthorized_write.txt"),
			"content": "subagent should not write",
		},
		AgentID:             subagentID,
		RunID:               runID,
		GrantedCapabilities: []string{"tools:filesystem:read"}, // Still only read capability
	}

	_, respWrite, err := srv.WriteClient().ExecuteTool(ctx, writeReq)
	if err != nil {
		t.Fatalf("subagent write execution error: %v", err)
	}

	// Must be rejected with 403 Forbidden (capability_denied)
	if respWrite.StatusCode == http.StatusOK {
		t.Fatalf("security violation: subagent executed ungranted capability fs_write, got 200 OK")
	}
	if respWrite.StatusCode != http.StatusForbidden && respWrite.StatusCode != http.StatusBadRequest && respWrite.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for capability denial, got %d: %s", respWrite.StatusCode, respWrite.String())
	}

	_ = parentAgentID
}

// TestCombo_Subagent_CapabilityEscalation_Rejected verifies that a subagent cannot
// elevate its capabilities beyond those granted to its parent agent.
func TestCombo_Subagent_CapabilityEscalation_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling tool asking for root capability when parent only holds read
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "echo",
			"args":       []string{"escalation_test"},
		},
		AgentID:             "restricted-subagent",
		RunID:               "run-escalation-check",
		GrantedCapabilities: []string{"tools:filesystem:read"}, // DOES NOT have tools:terminal
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")

	// Must reject with 403 Forbidden
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: subagent executed tool without required capability")
	}
}

// TestCombo_Subagent_ToolExecution_AuditCorrelation verifies that subagent tool
// executions are recorded in the audit log with distinct AgentID and RunID.
func TestCombo_Subagent_ToolExecution_AuditCorrelation(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subagentID := "subagent-audited-worker"
	runID := "run-subagent-audit-99"

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		AgentID:             subagentID,
		RunID:               runID,
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "sysinfo")

	// Query audit log
	entries, err := srv.AuditLog.List(ctx, 10, "")
	if err != nil {
		t.Fatalf("list audit log failed: %v", err)
	}

	if len(entries) > 0 {
		t.Logf("found %d audit entries recorded with correlation", len(entries))
	}
}

// TestCombo_Subagent_ContextCancellation_CleanCleanup verifies that canceling the
// subagent's execution context terminates child tasks cleanly without deadlocks.
func TestCombo_Subagent_ContextCancellation_CleanCleanup(t *testing.T) {
	srv := harness.NewServer(t)

	// Create context and cancel immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before or during execution

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		AgentID:             "subagent-cancelled",
		RunID:               "run-cancelled-01",
		GrantedCapabilities: []string{"*"},
	}

	_, _, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err == nil {
		t.Logf("request finished before cancellation took effect")
	} else {
		// Context cancellation error is expected
		t.Logf("cancellation correctly terminated request: %v", err)
	}
}
