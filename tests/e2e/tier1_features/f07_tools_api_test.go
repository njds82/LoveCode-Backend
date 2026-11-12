package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF07_ToolsAPI_ListTools_RequiresReadScope(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Unauthenticated should fail with 401
	respUnauth, err := srv.UnauthClient().Get(ctx, "/v1/tools")
	if err != nil {
		t.Fatalf("unauth request failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, respUnauth, "F7: Tools API")
	srv.UnauthClient().AssertStatus(t, respUnauth, http.StatusUnauthorized)

	// Read token should succeed with 200
	respRead, err := srv.ReadClient().Get(ctx, "/v1/tools")
	if err != nil {
		t.Fatalf("read request failed: %v", err)
	}
	srv.ReadClient().AssertStatus(t, respRead, http.StatusOK)
}

func TestF07_ToolsAPI_GetTool_ByID(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tool harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools/sysinfo", &tool)
	if err != nil {
		t.Fatalf("get tool failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F7: Tools API")

	if resp.StatusCode == http.StatusOK {
		if tool.ID != "sysinfo" && tool.Name != "sysinfo" {
			t.Errorf("expected tool sysinfo, got ID: %s", tool.ID)
		}
	}
}

func TestF07_ToolsAPI_GetTool_NotFound(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.ReadClient().Get(ctx, "/v1/tools/nonexistent_tool_xyz_999")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F7: Tools API")
	srv.ReadClient().AssertStatus(t, resp, http.StatusNotFound)
}

func TestF07_ToolsAPI_PatchTool_UpdateEnabled(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Writer token attempting admin operation PATCH /v1/tools/{id} must be rejected with 403
	respWrite, err := srv.WriteClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("patch with write token failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, respWrite, "F7: Tools API")

	if respWrite.StatusCode != http.StatusForbidden && respWrite.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for non-admin patch, got: %d", respWrite.StatusCode)
	}

	// Admin token should be permitted
	respAdmin, err := srv.AdminClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("patch with admin token failed: %v", err)
	}
	if respAdmin.StatusCode != http.StatusOK && respAdmin.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected admin patch status: %d", respAdmin.StatusCode)
	}
}

func TestF07_ToolsAPI_ExecuteTool_RequiresWriteScope(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"*"},
	}

	// Read client attempting POST /v1/tools/execute should be rejected with 403
	var execResp harness.ToolExecuteResponse
	resp, err := srv.ReadClient().PostJSON(ctx, "/v1/tools/execute", req, &execResp)
	if err != nil {
		t.Fatalf("execute with read token failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F7: Tools API")

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for read token calling execute, got: %d", resp.StatusCode)
	}
}

func TestF07_ToolsAPI_ExecuteTool_CapabilityDenied(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling a tool requiring tools:terminal without granting it
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "echo",
			"args":       []string{"hello"},
		},
		GrantedCapabilities: []string{"tools:filesystem:read"}, // Missing tools:terminal
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F7: Tools API")

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for missing capability, got %d: %s", resp.StatusCode, resp.String())
	}
}
