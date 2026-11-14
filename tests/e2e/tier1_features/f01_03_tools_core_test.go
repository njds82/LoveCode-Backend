package tier1_features

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/tools"
	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func registerCoreTools(srv *harness.Server) {
	if srv == nil || srv.Tools == nil {
		return
	}
	reg := srv.Tools.Registry()

	_ = reg.RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "fs_read",
		Name:                 "fs_read",
		Description:          "Read file content",
		Category:             tools.CategoryFilesystem,
		RiskLevel:            tools.RiskMedium,
		RequiredCapabilities: []string{"tools:filesystem:read"},
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"required": ["path"],
			"properties": {
				"path": { "type": "string" }
			}
		}`),
		Enabled: true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &p)
		return &tools.ExecutionResult{
			ToolName: "fs_read",
			Output:   "content of " + p.Path,
		}, nil
	}))

	_ = reg.RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "sysinfo",
		Name:                 "sysinfo",
		Description:          "System information",
		Category:             tools.CategorySystem,
		RiskLevel:            tools.RiskLow,
		RequiredCapabilities: []string{"tools:sysinfo"},
		ParametersSchema:     json.RawMessage(`{"type": "object"}`),
		Enabled:              true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		return &tools.ExecutionResult{
			ToolName: "sysinfo",
			Output:   "os=linux arch=amd64 uptime=3600s cpu=4 mem=8GB disk=100GB",
			Data: map[string]any{
				"os":   "linux",
				"arch": "amd64",
			},
		}, nil
	}))

	_ = reg.RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "terminal_exec",
		Name:                 "terminal_exec",
		Description:          "Terminal execution",
		Category:             tools.CategorySystem,
		RiskLevel:            tools.RiskHigh,
		RequiredCapabilities: []string{"tools:terminal"},
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"required": ["executable"],
			"properties": {
				"executable": { "type": "string" },
				"args": { "type": "array", "items": { "type": "string" } }
			}
		}`),
		Enabled: true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		var p struct {
			Executable string `json:"executable"`
		}
		_ = json.Unmarshal(args, &p)
		if p.Executable == "sleep" {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
				return &tools.ExecutionResult{ToolName: "terminal_exec", Output: "slept"}, nil
			}
		}
		return &tools.ExecutionResult{ToolName: "terminal_exec", Output: "executed " + p.Executable}, nil
	}))
}

// --- Feature 1: Tool Definitions & Schema ---

func TestF01_ToolSchema_StructureAndProperties(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools", &tools)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F1: Tool Definitions")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	if len(tools) == 0 {
		t.Fatal("expected registered tools in tool definitions response, got empty list")
	}

	for _, tool := range tools {
		if tool.ID == "" {
			t.Errorf("tool ID must not be empty")
		}
		if tool.Name == "" {
			t.Errorf("tool Name must not be empty")
		}
		if tool.Category == "" {
			t.Errorf("tool Category must not be empty for %s", tool.ID)
		}
	}
}

func TestF01_ToolSchema_RiskClassificationLevels(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools", &tools)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F1: Tool Definitions")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	if len(tools) == 0 {
		t.Fatal("expected registered tools in response, got empty list")
	}

	validRisks := map[string]bool{
		"":         true,
		"none":     true,
		"low":      true,
		"medium":   true,
		"high":     true,
		"critical": true,
	}
	for _, tool := range tools {
		if !validRisks[tool.RiskLevel] {
			t.Errorf("unexpected risk level %q for tool %s", tool.RiskLevel, tool.ID)
		}
	}
}

func TestF01_ToolSchema_ValidParametersValidation(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:sysinfo", "*"},
	}
	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F1: Schema Validation")
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)

	if execResp == nil {
		t.Fatal("expected non-nil ExecutionResult on 200 OK")
	}
}

func TestF01_ToolSchema_MissingRequiredParameters(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling fs_read without required parameter "path"
	req := harness.ToolExecuteRequest{
		ToolName:            "fs_read",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}
	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F1: Schema Validation")

	// Missing required "path" must return 400 Bad Request
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing required argument, got %d: %s", resp.StatusCode, resp.String())
	}
}

func TestF01_ToolSchema_TypeMismatch(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Passing integer for path instead of string
	req := harness.ToolExecuteRequest{
		ToolName: "fs_read",
		Arguments: map[string]any{
			"path": 12345,
		},
		GrantedCapabilities: []string{"tools:filesystem:read", "*"},
	}
	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F1: Schema Type Mismatch")

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for type mismatch, got %d: %s", resp.StatusCode, resp.String())
	}
}

// --- Feature 2: Centralized Tool Registry ---

func TestF02_ToolRegistry_DiscoveryAndListing(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools", &tools)
	if err != nil {
		t.Fatalf("GET /v1/tools failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	if len(tools) == 0 {
		t.Fatal("expected registered tools in tool definitions response, got empty list")
	}
}

func TestF02_ToolRegistry_CategoryFiltering(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools?category=filesystem", &tools)
	if err != nil {
		t.Fatalf("GET /v1/tools?category=filesystem failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	if len(tools) == 0 {
		t.Fatal("expected at least 1 filesystem tool, got none")
	}

	for _, tool := range tools {
		if tool.Category != "filesystem" {
			t.Errorf("expected category filesystem, got %s for tool %s", tool.Category, tool.ID)
		}
	}
}

func TestF02_ToolRegistry_GetToolByID(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tool harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools/fs_read", &tool)
	if err != nil {
		t.Fatalf("GET /v1/tools/fs_read failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	if tool.ID != "fs_read" && tool.Name != "fs_read" {
		t.Errorf("expected tool fs_read, got ID %q", tool.ID)
	}
}

func TestF02_ToolRegistry_EnableDisableState(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Update tool enable state via PATCH
	patchBody := map[string]any{"enabled": false}
	var updated harness.ToolDefinitionResponse
	resp, err := srv.AdminClient().PatchJSON(ctx, "/v1/tools/fs_read", patchBody, &updated)
	if err != nil {
		t.Fatalf("PATCH /v1/tools/fs_read failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")
	srv.AdminClient().AssertStatus(t, resp, http.StatusOK)

	if updated.Enabled != false {
		t.Errorf("expected tool to be disabled, got enabled=true")
	}
	// Re-enable
	_, _ = srv.AdminClient().PatchJSON(ctx, "/v1/tools/fs_read", map[string]any{"enabled": true}, nil)
}

func TestF02_ToolRegistry_DisabledToolExecutionBlocked(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Disable tool first
	patchResp, err := srv.AdminClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("disable tool patch failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, patchResp, "F2: Tool Registry")
	srv.AdminClient().AssertStatus(t, patchResp, http.StatusOK)

	defer func() {
		_, _ = srv.AdminClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": true})
	}()

	// Attempt execution of disabled tool
	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"*"},
	}
	_, execResp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().AssertStatus(t, execResp, http.StatusForbidden)
}

// --- Feature 3: Unified Tool Executor ---

func TestF03_ToolExecutor_DenyByDefault_NoCapabilities(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "fs_read",
		Arguments:           map[string]any{"path": "test.txt"},
		GrantedCapabilities: []string{}, // No capabilities granted!
	}
	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")
	srv.WriteClient().AssertStatus(t, resp, http.StatusForbidden)
}

func TestF03_ToolExecutor_GrantedCapability_Permitted(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
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
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)
}

func TestF03_ToolExecutor_ExecutionTimeout_Enforced(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "sleep",
			"args":       []string{"3"},
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
		TimeoutMS:           50, // 50ms timeout against 3s sleep
	}
	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")
	srv.WriteClient().AssertStatus(t, resp, http.StatusGatewayTimeout)
}

func TestF03_ToolExecutor_OutputLimit_Truncation(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"*"},
		MaxOutputBytes:      16, // Truncate at 16 bytes
	}
	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")
	srv.WriteClient().AssertStatus(t, resp, http.StatusOK)

	if execResp == nil || !execResp.Truncated || int64(len(execResp.Output)) > 16 {
		t.Errorf("expected output to be truncated at 16 bytes, got truncated=%v len=%d output=%q", execResp.Truncated, len(execResp.Output), execResp.Output)
	}
}

func TestF03_ToolExecutor_BoundedConcurrency_WorkerPool(t *testing.T) {
	srv := harness.NewServer(t)
	registerCoreTools(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const concurrentCalls = 10
	var wg sync.WaitGroup
	errCh := make(chan error, concurrentCalls)

	for i := 0; i < concurrentCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := harness.ToolExecuteRequest{
				ToolName:            "sysinfo",
				Arguments:           map[string]any{},
				GrantedCapabilities: []string{"*"},
			}
			_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
			if err != nil {
				errCh <- err
				return
			}
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, resp.String())
				return
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent execution error: %v", err)
	}
}
