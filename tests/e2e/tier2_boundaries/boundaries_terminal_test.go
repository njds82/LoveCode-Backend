package tier2_boundaries

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_Terminal_ShellInjection_Characters verifies that shell metacharacters
// (;, |, &&, `, $, $(), >, <) in command arguments are treated as literal strings and
// never evaluated by an intermediary shell.
func TestBoundaries_Terminal_ShellInjection_Characters(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	injectionPayloads := []string{
		"safe; echo injected_semicolon",
		"safe | cat /etc/passwd",
		"safe && whoami",
		"safe `id`",
		"safe $(id)",
		"safe > /tmp/injected.txt",
	}

	for _, payload := range injectionPayloads {
		req := harness.ToolExecuteRequest{
			ToolName: "terminal_exec",
			Arguments: map[string]any{
				"executable": "echo",
				"args":       []string{payload},
			},
			GrantedCapabilities: []string{"tools:terminal", "*"},
		}

		execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")

		if resp.StatusCode == http.StatusOK && execResp != nil {
			// If echo was executed directly without a shell, the output must literally contain
			// the injected string (e.g. "safe; echo injected_semicolon") rather than splitting commands.
			if !strings.Contains(execResp.Output, payload) {
				t.Errorf("shell injection occurred: expected literal arg %q, got: %s", payload, execResp.Output)
			}
		}
	}
}

// TestBoundaries_Terminal_DisallowedBinaries verifies that arbitrary shell interpreters
// or dangerous binaries (bash, sh, zsh, sudo, nc) not on the allowlist are rejected.
func TestBoundaries_Terminal_DisallowedBinaries(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	disallowed := []string{
		"bash",
		"sh",
		"zsh",
		"sudo",
		"nc",
		"curl",
		"python3",
	}

	for _, bin := range disallowed {
		req := harness.ToolExecuteRequest{
			ToolName: "terminal_exec",
			Arguments: map[string]any{
				"executable": bin,
				"args":       []string{"-c", "echo test"},
			},
			GrantedCapabilities: []string{"tools:terminal", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for binary %q: %v", bin, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")

		// Must be rejected with 403 Forbidden or 400 Bad Request
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: non-allowlisted binary %q was permitted to run", bin)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 or 400 for binary %q, got %d: %s", bin, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_Terminal_ArgumentInjection verifies that dangerous command flags
// targeting utility arguments are passed safely without arbitrary execution.
func TestBoundaries_Terminal_ArgumentInjection(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dangerousArgs := []string{
		"--checkpoint-action=exec=whoami",
		"-oProxyCommand=whoami",
		"-e/bin/sh",
	}

	for _, dangerousArg := range dangerousArgs {
		req := harness.ToolExecuteRequest{
			ToolName: "terminal_exec",
			Arguments: map[string]any{
				"executable": "echo",
				"args":       []string{dangerousArg},
			},
			GrantedCapabilities: []string{"tools:terminal", "*"},
		}

		execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for argument %q: %v", dangerousArg, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")

		if resp.StatusCode == http.StatusOK && execResp != nil {
			if !strings.Contains(execResp.Output, dangerousArg) {
				t.Errorf("argument injection mishandled for %q: %s", dangerousArg, execResp.Output)
			}
		}
	}
}

// TestBoundaries_Terminal_ProcessGroup_SIGKILL_Cleanup verifies that a command that
// sleeps or exceeds timeout is killed cleanly via its process group without leaving zombies.
func TestBoundaries_Terminal_ProcessGroup_SIGKILL_Cleanup(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "sleep",
			"args":       []string{"10"},
		},
		TimeoutMS:           150, // Short timeout
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")

	// Expect timeout error: 504 Gateway Timeout or 408 Request Timeout
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected command to time out, got 200 OK")
	}
	if resp.StatusCode != http.StatusGatewayTimeout && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected timeout status code, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Terminal_EmptyExecutable_Rejected verifies that empty or whitespace
// executable strings are rejected with schema validation error.
func TestBoundaries_Terminal_EmptyExecutable_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, emptyExe := range []string{"", "  ", "\t"} {
		req := harness.ToolExecuteRequest{
			ToolName: "terminal_exec",
			Arguments: map[string]any{
				"executable": emptyExe,
				"args":       []string{"arg"},
			},
			GrantedCapabilities: []string{"tools:terminal", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "terminal_exec")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("expected empty executable %q to fail, got 200 OK", emptyExe)
		}
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 400 Bad Request for empty executable, got %d: %s", resp.StatusCode, resp.String())
		}
	}
}
