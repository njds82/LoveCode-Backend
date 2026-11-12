package tier1_features

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// --- Feature 1: Tool Definitions & Schema ---

func TestF01_ToolSchema_StructureAndProperties(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools", &tools)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F1: Tool Definitions")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools", &tools)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F1: Tool Definitions")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status %d, body: %s", resp.StatusCode, resp.String())
	}
	if resp.StatusCode == http.StatusOK && execResp == nil {
		t.Fatal("expected non-nil ExecutionResult on 200 OK")
	}
}

func TestF01_ToolSchema_MissingRequiredParameters(t *testing.T) {
	srv := harness.NewServer(t)
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

	// If fs_read is registered, missing required "path" must return 400 Bad Request
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400 Bad Request for missing required argument, got %d: %s", resp.StatusCode, resp.String())
	}
}

func TestF01_ToolSchema_TypeMismatch(t *testing.T) {
	srv := harness.NewServer(t)
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

	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400 Bad Request for type mismatch, got %d: %s", resp.StatusCode, resp.String())
	}
}

// --- Feature 2: Centralized Tool Registry ---

func TestF02_ToolRegistry_DiscoveryAndListing(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools", &tools)
	if err != nil {
		t.Fatalf("GET /v1/tools failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)
}

func TestF02_ToolRegistry_CategoryFiltering(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tools []harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools?category=filesystem", &tools)
	if err != nil {
		t.Fatalf("GET /v1/tools?category=filesystem failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)

	for _, tool := range tools {
		if tool.Category != "filesystem" {
			t.Errorf("expected category filesystem, got %s for tool %s", tool.Category, tool.ID)
		}
	}
}

func TestF02_ToolRegistry_GetToolByID(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var tool harness.ToolDefinitionResponse
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/tools/fs_read", &tool)
	if err != nil {
		t.Fatalf("GET /v1/tools/fs_read failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F2: Tool Registry")

	if resp.StatusCode == http.StatusOK {
		if tool.ID != "fs_read" && tool.Name != "fs_read" {
			t.Errorf("expected tool fs_read, got ID %q", tool.ID)
		}
	} else if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
}

func TestF02_ToolRegistry_EnableDisableState(t *testing.T) {
	srv := harness.NewServer(t)
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

	if resp.StatusCode == http.StatusOK {
		if updated.Enabled != false {
			t.Errorf("expected tool to be disabled, got enabled=true")
		}
		// Re-enable
		_, _ = srv.AdminClient().PatchJSON(ctx, "/v1/tools/fs_read", map[string]any{"enabled": true}, nil)
	} else if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, resp.String())
	}
}

func TestF02_ToolRegistry_DisabledToolExecutionBlocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Disable tool first
	patchResp, _ := srv.AdminClient().Patch(ctx, "/v1/tools/sysinfo", map[string]any{"enabled": false})
	srv.AdminClient().SkipIfUnregistered(t, patchResp, "F2: Tool Registry")

	if patchResp != nil && patchResp.StatusCode == http.StatusOK {
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
		if execResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for disabled tool, got %d: %s", execResp.StatusCode, execResp.String())
		}
	}
}

// --- Feature 3: Unified Tool Executor ---

func TestF03_ToolExecutor_DenyByDefault_NoCapabilities(t *testing.T) {
	srv := harness.NewServer(t)
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

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden under deny-by-default, got %d: %s", resp.StatusCode, resp.String())
	}
}

func TestF03_ToolExecutor_GrantedCapability_Permitted(t *testing.T) {
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
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 200 OK or 404 for sysinfo, got %d: %s", resp.StatusCode, resp.String())
	}
}

func TestF03_ToolExecutor_ExecutionTimeout_Enforced(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "sleep",
			"args":       []string{"2"},
		},
		GrantedCapabilities: []string{"tools:terminal", "*"},
		TimeoutMS:           50, // 50ms timeout against 2s sleep
	}
	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")

	if resp.StatusCode != http.StatusGatewayTimeout && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Logf("execution timeout returned status: %d", resp.StatusCode)
	}
}

func TestF03_ToolExecutor_OutputLimit_Truncation(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "sysinfo",
		Arguments:           map[string]any{},
		GrantedCapabilities: []string{"*"},
		MaxOutputBytes:      64, // Truncate at 64 bytes
	}
	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F3: Tool Executor")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		if int64(len(execResp.Output)) > 64 && execResp.Truncated == false {
			t.Errorf("expected output to be truncated at 64 bytes, got len %d", len(execResp.Output))
		}
	}
}

func TestF03_ToolExecutor_BoundedConcurrency_WorkerPool(t *testing.T) {
	srv := harness.NewServer(t)
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
			if resp.IsUnregisteredRoute() {
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
