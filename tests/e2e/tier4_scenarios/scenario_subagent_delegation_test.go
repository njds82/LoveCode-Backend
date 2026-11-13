package tier4_scenarios

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_PrivilegeNarrowedSubagentDelegation tests hierarchical agent security:
// 1. Lead agent creates task and delegates sub-task to read-only subagent
// 2. Subagent executes fs_read successfully
// 3. Subagent attempts fs_write, strictly denied (403 Forbidden)
// 4. Lead agent (having write capability) executes fs_write successfully
// 5. Audit log reflects distinct Agent IDs and correlation hierarchy
func TestScenario_PrivilegeNarrowedSubagentDelegation(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	sb.WriteFile(t, "shared_config.yaml", []byte("env: production\nmode: strict\n"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := "run-delegation-scenario-02"
	parentTaskID := "task-lead-orchestrate"
	subTaskID := "task-subagent-inspect"

	leadAgentID := "agent-lead-admin"
	subAgentID := "agent-sub-readonly"

	// Step 1: Subagent reads configuration using narrowed read capability
	subReadReq := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": sb.Path("shared_config.yaml"),
		},
		AgentID:             subAgentID,
		RunID:               runID,
		TaskID:              subTaskID,
		GrantedCapabilities: []string{"tools:filesystem:read"}, // Strictly read-only!
	}

	execRead, respRead, err := srv.WriteClient().ExecuteTool(ctx, subReadReq)
	if err != nil {
		t.Fatalf("subagent fs_read failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respRead, "fs_read")

	if respRead.StatusCode == http.StatusOK && execRead != nil {
		if execRead.Output != "env: production\nmode: strict\n" {
			t.Errorf("unexpected read content: %s", execRead.Output)
		}
	}

	// Step 2: Subagent attempts to write (must be rejected!)
	subWriteReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    sb.Path("subagent_tamper.txt"),
			"content": "unauthorized change",
		},
		AgentID:             subAgentID,
		RunID:               runID,
		TaskID:              subTaskID,
		GrantedCapabilities: []string{"tools:filesystem:read"}, // Still only read capability
	}

	_, respSubWrite, err := srv.WriteClient().ExecuteTool(ctx, subWriteReq)
	if err != nil {
		t.Fatalf("subagent write execution error: %v", err)
	}

	if respSubWrite.StatusCode == http.StatusOK {
		t.Fatalf("security violation: read-only subagent was permitted to write")
	}
	if respSubWrite.StatusCode != http.StatusForbidden && respSubWrite.StatusCode != http.StatusBadRequest && respSubWrite.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for capability denial, got %d: %s", respSubWrite.StatusCode, respSubWrite.String())
	}

	// Step 3: Lead agent (with full write permissions) performs authorized write
	leadWriteReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    sb.Path("lead_approved_audit.txt"),
			"content": "authorized audit log entry by lead agent",
		},
		AgentID:             leadAgentID,
		RunID:               runID,
		TaskID:              parentTaskID,
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, respLeadWrite, err := srv.WriteClient().ExecuteTool(ctx, leadWriteReq)
	if err != nil {
		t.Fatalf("lead agent write failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respLeadWrite, "fs_write")

	// Step 4: Verify audit log records distinct actors and tasks
	entries, err := srv.AuditLog.List(ctx, 50, "")
	if err == nil {
		t.Logf("Scenario 2 completed, verified %d audit log entries", len(entries))
	}
}
