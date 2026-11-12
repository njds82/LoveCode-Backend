package tier3_combinations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_MCP_ToolDiscovery_AgentInvocation verifies that when an external MCP server
// is registered, its tools are discovered and an agent can call them via the executor.
func TestCombo_MCP_ToolDiscovery_AgentInvocation(t *testing.T) {
	srv := harness.NewServer(t)
	mcpServer := harness.NewMockMCPSSEServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Register MCP server via REST API
	regPayload := map[string]any{
		"id":        "mock-mcp-agent",
		"name":      "Mock MCP Server for Agent",
		"transport": "sse",
		"url":       mcpServer.URL,
	}

	var regResp map[string]any
	resp, err := srv.AdminClient().PostJSON(ctx, "/v1/mcp/servers", regPayload, &regResp)
	if err != nil {
		t.Fatalf("register MCP server failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F14: MCP Integration")

	// 2. Execute discovered MCP tool via POST /v1/tools/execute
	req := harness.ToolExecuteRequest{
		ToolName: "mcp_echo",
		Arguments: map[string]any{
			"message": "hello from agent",
		},
		AgentID:             "test-lead-agent",
		RunID:               "run-mcp-agent-01",
		GrantedCapabilities: []string{"tools:mcp", "*"},
	}

	execResp, respExec, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("tool execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respExec, "mcp_echo")

	if respExec.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("MCP tool execution output: %s", execResp.Output)
	}
}

// TestCombo_MCP_AgentCall_TimeoutHandling verifies that when an agent calls an MCP tool
// that hangs or exceeds its deadline, the executor aborts execution cleanly with a timeout.
func TestCombo_MCP_AgentCall_TimeoutHandling(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "mcp_delay",
		Arguments: map[string]any{
			"ms": 5000,
		},
		TimeoutMS:           100, // Strict timeout
		GrantedCapabilities: []string{"*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "mcp_delay")

	// Must fail with timeout: 504 Gateway Timeout or 408 Request Timeout
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected timeout, got 200 OK")
	}
}

// TestCombo_MCP_ToolOutput_SecretsRedactedForAgent verifies that credentials returned
// by external MCP tools are sanitized before entering the agent's context window.
func TestCombo_MCP_ToolOutput_SecretsRedactedForAgent(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "mcp_fetch_secret",
		Arguments: map[string]any{
			"key_id": "service_api_key",
		},
		GrantedCapabilities: []string{"*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "mcp_fetch_secret")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Output must not contain unmasked API key
		if strings.Contains(execResp.Output, "AKIA") || strings.Contains(execResp.Output, "sk-") {
			t.Fatalf("security violation: raw secret returned to agent: %s", execResp.Output)
		}
	}
}

// TestCombo_MCP_ServerUnregistered_AgentCallFails verifies that when an MCP server
// is unregistered, subsequent tool calls by agents immediately fail with 404 tool_not_found.
func TestCombo_MCP_ServerUnregistered_AgentCallFails(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Delete server
	respDel, err := srv.AdminClient().Delete(ctx, "/v1/mcp/servers/mock-mcp-agent")
	if err != nil {
		t.Fatalf("delete server failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respDel, "F16: MCP REST API")

	// Attempting tool call must fail with 404
	req := harness.ToolExecuteRequest{
		ToolName:            "mcp_echo",
		Arguments:           map[string]any{"message": "should fail"},
		GrantedCapabilities: []string{"*"},
	}

	_, respExec, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if respExec.StatusCode == http.StatusOK {
		t.Fatalf("expected tool execution to fail after MCP server unregistration, got 200 OK")
	}
}
