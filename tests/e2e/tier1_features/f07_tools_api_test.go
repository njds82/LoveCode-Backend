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
	registerCoreTools(srv)
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
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tool harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools/sysinfo", &tool)
	if err != nil {
		t.Fatalf("get tool failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F7: Tools API")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	if tool.ID != "sysinfo" && tool.Name != "sysinfo" {
		t.Errorf("expected tool sysinfo, got ID: %s", tool.ID)
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
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Writer token attempting admin operation PATCH /v1/tools/{id} must be rejected with 403
	respWrite, err := srv.WriteClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("patch with write token failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, respWrite, "F7: Tools API")
	srv.WriteClient().AssertStatus(t, respWrite, http.StatusForbidden)

	// Admin token should be permitted
	respAdmin, err := srv.AdminClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("patch with admin token failed: %v", err)
	}
	srv.AdminClient().AssertStatus(t, respAdmin, http.StatusOK)
}

func TestF07_ToolsAPI_ExecuteTool_RequiresWriteScope(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}

	// Read client attempting POST /v1/tools/execute should be rejected with 403
	resp, err := srv.ReadClient().Post(ctx, "/v1/tools/execute", req)
	if err != nil {
		t.Fatalf("execute with read token failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F7: Tools API")
	srv.ReadClient().AssertStatus(t, resp, http.StatusForbidden)
}

func TestF07_ToolsAPI_ExecuteTool_CapabilityDenied(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
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
	srv.WriteClient().AssertStatus(t, resp, http.StatusForbidden)
}
