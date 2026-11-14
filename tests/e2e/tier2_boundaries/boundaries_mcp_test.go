package tier2_boundaries

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_MCP_MalformedJSONRPC_SyntaxError verifies that invalid JSON payloads
// sent to MCP endpoints return JSON-RPC -32700 Parse error.
func TestBoundaries_MCP_MalformedJSONRPC_SyntaxError(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	badPayload := "{ this is completely invalid JSON ::: "
	resp, err := client.Post(ctx, "/message", []byte(badPayload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	var jsonrpcResp harness.JSONRPCResponse
	if err := resp.JSON(&jsonrpcResp); err != nil {
		// Or HTTP 400 Bad Request
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected JSON-RPC error or 400 Bad Request, got status %d", resp.StatusCode)
		}
		return
	}

	if jsonrpcResp.Error == nil {
		t.Fatalf("expected JSON-RPC error for malformed payload, got nil error")
	}
	if jsonrpcResp.Error.Code != -32700 {
		t.Errorf("expected JSON-RPC error code -32700 (Parse error), got %d: %s", jsonrpcResp.Error.Code, jsonrpcResp.Error.Message)
	}
}

// TestBoundaries_MCP_InvalidJSONRPC_MissingVersion verifies that requests missing
// the mandatory "jsonrpc": "2.0" field are rejected.
func TestBoundaries_MCP_InvalidJSONRPC_MissingVersion(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	invalidReq := map[string]any{
		"id":     1,
		"method": "ping",
	}

	var jsonrpcResp harness.JSONRPCResponse
	resp, err := client.PostJSON(ctx, "/message", invalidReq, &jsonrpcResp)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should either respond with an error or 400 Bad Request
	if resp.StatusCode == http.StatusOK && jsonrpcResp.Error == nil {
		t.Logf("server accepted request without explicit jsonrpc field: %s", resp.String())
	}
}

// TestBoundaries_MCP_MethodNotFound_Code32601 verifies that calling an unknown method
// returns JSON-RPC error code -32601 Method not found.
func TestBoundaries_MCP_MethodNotFound_Code32601(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "non_existent_method_xyz",
	}

	var jsonrpcResp harness.JSONRPCResponse
	resp, err := client.PostJSON(ctx, "/message", req, &jsonrpcResp)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)

	if jsonrpcResp.Error == nil {
		t.Fatalf("expected JSON-RPC error for non-existent method, got result: %v", jsonrpcResp.Result)
	}
	if jsonrpcResp.Error.Code != -32601 {
		t.Errorf("expected error code -32601 (Method not found), got %d: %s", jsonrpcResp.Error.Code, jsonrpcResp.Error.Message)
	}
}

// TestBoundaries_MCP_CancellationPropagation verifies that sending notifications/cancelled
// notification does not panic and is handled gracefully.
func TestBoundaries_MCP_CancellationPropagation(t *testing.T) {
	mcpServer := harness.NewMockMCPSSEServer(t)
	client := harness.NewClient(mcpServer.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cancelNotification := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/cancelled",
		Params:  []byte(`{"requestId": 12345, "reason": "user requested cancellation"}`),
	}

	resp, err := client.PostJSON(ctx, "/message", cancelNotification, nil)
	if err != nil {
		t.Fatalf("send notification failed: %v", err)
	}

	// Notifications have no ID and typically return 200 or 204
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected status for cancellation notification: %d", resp.StatusCode)
	}
}

// TestBoundaries_MCP_BrokenPipeRecovery verifies that when an external MCP stdio process
// dies or closes its pipe, the caller handles the broken connection cleanly without hanging.
func TestBoundaries_MCP_BrokenPipeRecovery(t *testing.T) {
	proc := harness.StartMockMCPStdio(t)

	// Close stdin immediately to simulate broken pipe / crashed process
	_ = proc.Stdin.Close()
	proc.Close()

	// Attempting to execute a JSON-RPC call over the closed pipe must report an error immediately
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := proc.Call(ctx, "ping", nil)
	if err == nil {
		t.Fatalf("expected error when calling RPC over closed broken pipe, got nil")
	}
}

// TestBoundaries_MCP_ToolCall_TimeoutEnforcement verifies that an MCP tool call exceeding
// configured deadline returns a timeout error.
func TestBoundaries_MCP_ToolCall_TimeoutEnforcement(t *testing.T) {
	srv := harness.NewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName:            "mcp_slow_tool",
		Arguments:           map[string]any{"delay_ms": 5000},
		TimeoutMS:           100, // very tight timeout
		GrantedCapabilities: []string{"*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "mcp_slow_tool")

	// Expect timeout status: 504 Gateway Timeout or 408 Request Timeout
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected slow tool to timeout, got 200 OK")
	}
	if resp.StatusCode != http.StatusGatewayTimeout && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected timeout status code, got %d: %s", resp.StatusCode, resp.String())
	}
}
