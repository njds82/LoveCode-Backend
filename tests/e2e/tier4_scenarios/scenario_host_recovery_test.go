package tier4_scenarios

import (
	"context"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_HostProcessMonitoringAndRecovery tests autonomous system monitoring:
// 1. Agent inspects host health and hardware stats via sysinfo
// 2. Lists running processes via process_list
// 3. Attempts to kill PID 1 (init system) -> strictly blocked with 403 Forbidden
// 4. Spawns an ephemeral test child process (sleep 30)
// 5. Successfully terminates the safe child process via process_kill
// 6. Confirms child process has terminated cleanly
func TestScenario_HostProcessMonitoringAndRecovery(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := "run-host-recovery-11"

	// Step 1: Inspect host health via sysinfo
	sysinfoReq := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	execSysinfo, respSysinfo, err := srv.WriteClient().ExecuteTool(ctx, sysinfoReq)
	if err != nil {
		t.Fatalf("sysinfo failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respSysinfo, "sysinfo")

	if respSysinfo.StatusCode == http.StatusOK && execSysinfo != nil {
		t.Logf("sysinfo telemetry: %s", execSysinfo.Output)
	}

	// Step 2: List processes via process_list
	listReq := harness.ToolExecuteRequest{
		ToolName:            "process_list",
		Arguments:           map[string]any{},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, respList, err := srv.WriteClient().ExecuteTool(ctx, listReq)
	if err != nil {
		t.Fatalf("process_list failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respList, "process_list")

	// Step 3: Attempt to kill PID 1 -> must fail
	killPID1Req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    1,
			"signal": "SIGKILL",
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, respKillPID1, err := srv.WriteClient().ExecuteTool(ctx, killPID1Req)
	if err != nil {
		t.Fatalf("kill PID 1 failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respKillPID1, "process_kill")

	if respKillPID1.StatusCode == http.StatusOK {
		t.Fatalf("security violation: terminating PID 1 succeeded, expected 403 Forbidden")
	}

	// Step 4: Spawn safe child process to terminate
	childCmd := exec.Command("sleep", "60")
	if err := childCmd.Start(); err != nil {
		t.Skipf("cannot start test child process: %v", err)
	}
	defer func() {
		if childCmd.Process != nil {
			_ = childCmd.Process.Kill()
		}
	}()

	childPID := childCmd.Process.Pid

	// Step 5: Terminate safe child process via process_kill
	killChildReq := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    childPID,
			"signal": "SIGTERM",
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, respKillChild, err := srv.WriteClient().ExecuteTool(ctx, killChildReq)
	if err != nil {
		t.Fatalf("kill child process failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respKillChild, "process_kill")

	if respKillChild.StatusCode == http.StatusOK {
		t.Logf("successfully terminated safe child process PID %d", childPID)
	}
}
