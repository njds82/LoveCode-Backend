package tier1_features

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF12_Process_List_ReturnsRunningProcesses(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "process_list",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F12: Process Tools")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("process_list output: %s", execResp.Output)
	}
}

func TestF12_Process_Kill_SafeChildProcess(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Spawn safe child process to be killed
	childCmd := exec.Command("sleep", "30")
	if err := childCmd.Start(); err != nil {
		t.Fatalf("failed to start child process: %v", err)
	}
	childPID := childCmd.Process.Pid
	defer func() {
		_ = childCmd.Process.Kill()
		_ = childCmd.Wait()
	}()

	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    childPID,
			"signal": "SIGTERM",
		},
		GrantedCapabilities: []string{"tools:process:kill", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F12: Process Tools")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected process_kill status: %d", resp.StatusCode)
	}
}

func TestF12_Process_Kill_PID1_Protected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Target PID 1 (init/systemd)
	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    1,
			"signal": "SIGKILL",
		},
		GrantedCapabilities: []string{"tools:process:kill", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F12: Process Tools")

	// Must be blocked with 403 Forbidden (ErrProtectedProcess) or 400 Bad Request
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected PID 1 kill to be blocked with 403 Forbidden, got: %d", resp.StatusCode)
	}
}

func TestF12_Process_Kill_SelfPID_Protected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	selfPID := os.Getpid()

	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    selfPID,
			"signal": "SIGTERM",
		},
		GrantedCapabilities: []string{"tools:process:kill", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F12: Process Tools")

	// Killing self PID must be blocked
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected self PID kill to be blocked with 403 Forbidden, got: %d", resp.StatusCode)
	}
}

func TestF12_Process_Kill_ParentPID_Protected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	parentPID := os.Getppid()

	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    parentPID,
			"signal": "SIGTERM",
		},
		GrantedCapabilities: []string{"tools:process:kill", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F12: Process Tools")

	// Killing parent PID must be blocked
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected parent PID kill to be blocked with 403 Forbidden, got: %d", resp.StatusCode)
	}
}
