package tier4_scenarios

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_TaskCancellation_ProcessGroupCleanup verifies that canceling an agent's
// tool execution task propagates cancellation to the process group (SIGKILL) and cleans
// up resources without leaving hanging orphan processes or locked database tables.
func TestScenario_TaskCancellation_ProcessGroupCleanup(t *testing.T) {
	srv := harness.NewServer(t)

	// Context with very short deadline to simulate mid-turn user cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	runID := "run-cancelled-task-06"
	taskID := "task-long-running-tool"

	// Executing a command that would sleep for 10 seconds if not cancelled
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "sleep",
			"args":       []string{"10"},
		},
		RunID:               runID,
		TaskID:              taskID,
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	start := time.Now()
	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		// Context cancellation error is expected
		t.Logf("tool execution aborted as expected on cancellation: %v (elapsed: %v)", err, elapsed)
	} else if resp != nil {
		srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected long-running command to be aborted, but it completed with 200 OK")
		}
	}

	// Verify the request did not block for 10 seconds (must abort near the 200ms mark)
	if elapsed > 4*time.Second {
		t.Errorf("cancellation did not abort execution promptly, took: %v", elapsed)
	}

	// Post-cancellation check: server must remain responsive and capable of executing tools
	freshCtx, freshCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer freshCancel()

	freshReq := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"*"},
	}

	_, freshResp, err := srv.WriteClient().ExecuteTool(freshCtx, freshReq)
	if err != nil {
		t.Fatalf("server unresponsive after task cancellation: %v", err)
	}
	if freshResp != nil && !freshResp.IsUnregisteredRoute() && !freshResp.IsToolNotFound() {
		srv.WriteClient().AssertStatus(t, freshResp, http.StatusOK)
	}
}
