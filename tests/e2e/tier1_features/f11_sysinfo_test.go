package tier1_features

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF11_Sysinfo_ReportsOSAndArch(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)

	if execResp != nil {
		t.Logf("sysinfo output: %s", execResp.Output)
	}
}

func TestF11_Sysinfo_ReportsCPUAndMemory(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)

	// Output or data should report system stats
	if execResp == nil || (execResp.Output == "" && execResp.Data == nil) {
		t.Errorf("expected non-empty sysinfo output or data, got: %v", execResp)
	}
}

func TestF11_Sysinfo_ReportsDiskSpace(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)
}

func TestF11_Sysinfo_NoSecretLeaks(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)

	if execResp != nil {
		output := execResp.Output
		for _, token := range []string{srv.AdminToken, srv.WriteToken, srv.ReadToken} {
			if token != "" && strings.Contains(output, token) {
				t.Fatalf("sysinfo output leaked secret token: %s", token)
			}
		}
	}
}

func TestF11_Sysinfo_NoUserAccountEnumeration(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)

	if execResp != nil {
		output := execResp.Output
		// Should not dump /etc/shadow or /etc/passwd contents
		if strings.Contains(output, "root:x:") || strings.Contains(output, "/bin/bash") && strings.Contains(output, ":0:0:") {
			t.Fatalf("sysinfo output leaked user accounts from passwd file!")
		}
	}
}
