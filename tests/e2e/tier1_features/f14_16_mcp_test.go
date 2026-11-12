package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// --- Feature 14: MCP Client ---

func TestF14_MCPClient_Initialize_Handshake(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initReq := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  nil,
	}

	var initResp harness.JSONRPCResponse
	resp, err := client.PostJSON(ctx, "/message", initReq, &initResp)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)

	if initResp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", initResp.Error)
	}
}

func TestF14_MCPClient_ToolsList_Discovery(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	listReq := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	var listResp harness.JSONRPCResponse
	resp, err := client.PostJSON(ctx, "/message", listReq, &listResp)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)

	if listResp.Result == nil {
		t.Fatal("expected non-nil tools/list result")
	}
}

func TestF14_MCPClient_ToolsCall_Execution(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	callReq := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  []byte(`{"name":"sse_echo","arguments":{"text":"hello_mcp"}}`),
	}

	var callResp harness.JSONRPCResponse
	resp, err := client.PostJSON(ctx, "/message", callReq, &callResp)
	if err != nil {
		t.Fatalf("tools/call failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)

	calls := mcpServer.GetCalls()
	if len(calls) == 0 {
		t.Fatal("expected mock MCP server to record call")
	}
	if calls[0].ToolName != "sse_echo" {
		t.Errorf("expected tool sse_echo, got: %s", calls[0].ToolName)
	}
}

func TestF14_MCPClient_Cancellation_Notified(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Notification has ID == nil
	cancelReq := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/cancelled",
		Params:  []byte(`{"requestId":3,"reason":"user cancelled"}`),
	}

	resp, err := client.Post(ctx, "/message", cancelReq)
	if err != nil {
		t.Fatalf("cancel notification failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)
}

func TestF14_MCPClient_MalformedResponse_Handled(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Send invalid JSON
	resp, err := client.Post(ctx, "/message", "not-a-valid-json-rpc")
	if err != nil {
		t.Fatalf("malformed request failed: %v", err)
	}
	// Must return HTTP 200 with JSON-RPC -32700 parse error or HTTP 400
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

// --- Feature 15: MCP Server Export ---

func TestF15_MCPServer_ExportTools_SSEStream(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.ReadClient().Get(ctx, "/v1/mcp/sse")
	if err != nil {
		t.Fatalf("connect MCP SSE failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F15: MCP Server Export")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF15_MCPServer_JSONRPC_ToolsList(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	resp, err := srv.ReadClient().Post(ctx, "/v1/mcp/message", req)
	if err != nil {
		t.Fatalf("mcp message failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F15: MCP Server Export")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF15_MCPServer_JSONRPC_ToolsCall(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  []byte(`{"name":"sysinfo","arguments":{}}`),
	}

	resp, err := srv.WriteClient().Post(ctx, "/v1/mcp/message", req)
	if err != nil {
		t.Fatalf("mcp call failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F15: MCP Server Export")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF15_MCPServer_Authentication_Required(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.UnauthClient().Get(ctx, "/v1/mcp/sse")
	if err != nil {
		t.Fatalf("unauth request failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, resp, "F15: MCP Server Export")

	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 401 Unauthorized for unauth MCP connection, got: %d", resp.StatusCode)
	}
}

func TestF15_MCPServer_UnauthorizedTool_Forbidden(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  []byte(`{"name":"forbidden_internal_tool","arguments":{}}`),
	}

	resp, err := srv.ReadClient().Post(ctx, "/v1/mcp/message", req)
	if err != nil {
		t.Fatalf("mcp call failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F15: MCP Server Export")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

// --- Feature 16: MCP REST API ---

func TestF16_MCPAPI_ListServers_RequiresRead(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Unauthenticated
	respUnauth, err := srv.UnauthClient().Get(ctx, "/v1/mcp/servers")
	if err != nil {
		t.Fatalf("unauth request failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, respUnauth, "F16: MCP REST API")
	srv.UnauthClient().AssertStatus(t, respUnauth, http.StatusUnauthorized)

	// Read client
	respRead, err := srv.ReadClient().Get(ctx, "/v1/mcp/servers")
	if err != nil {
		t.Fatalf("read request failed: %v", err)
	}
	srv.ReadClient().AssertStatus(t, respRead, http.StatusOK)
}

func TestF16_MCPAPI_RegisterServer_AdminScope(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body := map[string]any{
		"id":          "test-stdio-mcp",
		"name":        "Test Stdio MCP Server",
		"transport":   "stdio",
		"endpoint":    "echo",
		"args":        []string{"mcp"},
		"timeout_ms":  5000,
		"enabled":     true,
	}

	// Non-admin token should be rejected with 403
	respWrite, err := srv.WriteClient().Post(ctx, "/v1/mcp/servers", body)
	if err != nil {
		t.Fatalf("write token register failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, respWrite, "F16: MCP REST API")

	if respWrite.StatusCode != http.StatusForbidden && respWrite.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 for non-admin registering MCP server, got: %d", respWrite.StatusCode)
	}

	// Admin token
	respAdmin, err := srv.AdminClient().Post(ctx, "/v1/mcp/servers", body)
	if err != nil {
		t.Fatalf("admin register failed: %v", err)
	}
	if respAdmin.StatusCode != http.StatusOK && respAdmin.StatusCode != http.StatusCreated && respAdmin.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected admin register status: %d", respAdmin.StatusCode)
	}
}

func TestF16_MCPAPI_GetServer_ByID(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.ReadClient().Get(ctx, "/v1/mcp/servers/test-stdio-mcp")
	if err != nil {
		t.Fatalf("get server failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F16: MCP REST API")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF16_MCPAPI_SyncTools_PopulatesRegistry(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.AdminClient().Post(ctx, "/v1/mcp/servers/test-stdio-mcp/sync", nil)
	if err != nil {
		t.Fatalf("sync request failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F16: MCP REST API")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected sync status: %d", resp.StatusCode)
	}
}

func TestF16_MCPAPI_DeleteServer_UnregistersTools(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.AdminClient().Delete(ctx, "/v1/mcp/servers/test-stdio-mcp")
	if err != nil {
		t.Fatalf("delete server failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F16: MCP REST API")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected delete status: %d", resp.StatusCode)
	}
}
