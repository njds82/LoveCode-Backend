package tier2_boundaries

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_Process_PID1_Immunity verifies that attempts to kill PID 1 (system init)
// are strictly prohibited and return 403 Forbidden.
func TestBoundaries_Process_PID1_Immunity(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    1,
			"signal": "SIGTERM",
		},
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "process_kill")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: killing PID 1 was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden or 400 Bad Request for PID 1 kill, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Process_SelfPID_Immunity verifies that attempts to terminate LoveCode's
// own process PID are strictly blocked with 403 Forbidden.
func TestBoundaries_Process_SelfPID_Immunity(t *testing.T) {
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
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "process_kill")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: self process kill was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden or 400 Bad Request for self PID kill, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Process_ParentPID_Immunity verifies that attempts to terminate LoveCode's
// parent process are blocked with 403 Forbidden.
func TestBoundaries_Process_ParentPID_Immunity(t *testing.T) {
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
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "process_kill")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: parent PID kill was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for parent PID kill, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Process_KernelPID2_Immunity verifies that PID 2 (kernel thread daemon kthreadd)
// is immune to termination requests.
func TestBoundaries_Process_KernelPID2_Immunity(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    2,
			"signal": "SIGKILL",
		},
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "process_kill")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: killing PID 2 was permitted, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for PID 2 kill, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Process_ZeroOrNegativePID_Rejected verifies that PID 0 (current process group)
// or negative PIDs (which signal all processes in a group) are rejected with validation error.
func TestBoundaries_Process_ZeroOrNegativePID_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, invalidPID := range []int{0, -1, -99} {
		req := harness.ToolExecuteRequest{
			ToolName: "process_kill",
			Arguments: map[string]any{
				"pid":    invalidPID,
				"signal": "SIGTERM",
			},
			GrantedCapabilities: []string{"tools:process", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "process_kill")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected PID %d to be rejected, got 200 OK", invalidPID)
		}
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 400 Bad Request for PID %d, got %d: %s", invalidPID, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_Process_NonExistentPID_Handled verifies that terminating a non-existent PID
// is handled gracefully (returns not found or error, without crashing the server).
func TestBoundaries_Process_NonExistentPID_Handled(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "process_kill",
		Arguments: map[string]any{
			"pid":    99999999, // extremely unlikely to exist
			"signal": "SIGTERM",
		},
		GrantedCapabilities: []string{"tools:process", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "process_kill")

	// Should not crash, and should return not found or error status
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected status code for non-existent PID: %d", resp.StatusCode)
	}
}
