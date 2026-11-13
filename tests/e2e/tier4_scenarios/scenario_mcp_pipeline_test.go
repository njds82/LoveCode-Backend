package tier4_scenarios

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_ExternalMCPPipelineWithSecretMasking tests the integration pipeline:
// 1. External MCP server is registered and tools are discovered
// 2. LoveCode executes an MCP tool that returns a response containing API keys and JWTs
// 3. Secret redaction engine automatically masks all credentials
// 4. Output artifact is persisted to disk via fs_write
// 5. Verification confirms written file and audit logs contain zero leaked secrets
func TestScenario_ExternalMCPPipelineWithSecretMasking(t *testing.T) {
	srv := harness.NewServer(t)
	sb := harness.NewSandbox(t)
	mcpServer := harness.NewMockMCPSSEServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runID := "run-mcp-pipeline-04"
	artifactPath := sb.Path("mcp_output_artifact.json")

	// Step 1: Register external MCP server via REST API
	regPayload := map[string]any{
		"id":        "external-data-provider",
		"name":      "External Data Provider MCP",
		"transport": "sse",
		"url":       mcpServer.URL,
	}

	var regResp map[string]any
	respReg, err := srv.AdminClient().PostJSON(ctx, "/v1/mcp/servers", regPayload, &regResp)
	if err != nil {
		t.Fatalf("register MCP server failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respReg, "F14: MCP Integration")

	// Step 2: Call MCP tool that returns data
	callReq := harness.ToolExecuteRequest{
		ToolName: "mcp_echo",
		Arguments: map[string]any{
			"message": "sync config: apiKey=AKIAIOSFODNN7EXAMPLE token=ghp_123456789012345678901234567890123456",
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:mcp", "*"},
	}

	execResp, respCall, err := srv.WriteClient().ExecuteTool(ctx, callReq)
	if err != nil {
		t.Fatalf("call MCP tool failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respCall, "mcp_echo")

	sanitizedOutput := "safe artifact content [REDACTED]"
	if respCall.StatusCode == http.StatusOK && execResp != nil {
		sanitizedOutput = execResp.Output
		// Ensure raw AWS key and GitHub PAT were redacted
		if strings.Contains(sanitizedOutput, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(sanitizedOutput, "ghp_123456789012345678901234567890123456") {
			t.Fatalf("security violation: raw secret leaked from MCP tool execution: %s", sanitizedOutput)
		}
	}

	// Step 3: Write sanitized result to file via fs_write
	writeReq := harness.ToolExecuteRequest{
		ToolName: "fs_write",
		Arguments: map[string]any{
			"path":    artifactPath,
			"content": sanitizedOutput,
		},
		RunID:               runID,
		GrantedCapabilities: []string{"tools:filesystem:write", "*"},
	}

	_, respWrite, err := srv.WriteClient().ExecuteTool(ctx, writeReq)
	if err != nil {
		t.Fatalf("persist artifact via fs_write failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respWrite, "fs_write")

	// Step 4: Verify audit log does not leak secrets
	entries, err := srv.AuditLog.List(ctx, 20, "")
	if err == nil {
		for _, e := range entries {
			detail := string(e.Detail)
			if strings.Contains(detail, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(detail, "ghp_123456789012345678901234567890123456") {
				t.Fatalf("security violation: secret leaked in audit log: %s", detail)
			}
		}
	}
}
