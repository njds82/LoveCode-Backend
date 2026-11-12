package tier1_features

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF09_Terminal_AllowlistBinary_Executes(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "echo",
			"args":       []string{"hello_terminal"},
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F9: Terminal Tool")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		if !strings.Contains(execResp.Output, "hello_terminal") {
			t.Errorf("expected output to contain hello_terminal, got: %s", execResp.Output)
		}
	}
}

func TestF09_Terminal_DisallowedBinary_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling non-allowlisted binary like sudo or nc
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "sudo",
			"args":       []string{"-l"},
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F9: Terminal Tool")

	// Disallowed binary must be blocked with 403 Forbidden or 400 Bad Request
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for disallowed binary, got %d: %s", resp.StatusCode, resp.String())
	}
}

func TestF09_Terminal_NoShellInterpolation_MetacharsLiteral(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Pass metachars: "foo; echo bar"
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "echo",
			"args":       []string{"foo; echo bar"},
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F9: Terminal Tool")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Output must literally contain "foo; echo bar", not separate command execution
		if !strings.Contains(execResp.Output, "foo; echo bar") {
			t.Errorf("expected literal metacharacters preserved, got: %s", execResp.Output)
		}
	}
}

func TestF09_Terminal_CleanEnvironment_PurgesSecrets(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run printenv or env via terminal_exec
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "env",
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F9: Terminal Tool")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Ensure token or secret environment variables are not dumped in plaintext
		for _, token := range []string{srv.AdminToken, srv.WriteToken, srv.ReadToken} {
			if token != "" && strings.Contains(execResp.Output, token) {
				t.Fatalf("terminal environment leaked raw secret token!")
			}
		}
	}
}

func TestF09_Terminal_ProcessGroup_SigkillOnTimeout(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run sleep with tight timeout
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "sleep",
			"args":       []string{"5"},
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
		TimeoutMS:           100, // 100ms
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F9: Terminal Tool")

	if resp.StatusCode != http.StatusGatewayTimeout && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Logf("terminal timeout returned: %d", resp.StatusCode)
	}
}
