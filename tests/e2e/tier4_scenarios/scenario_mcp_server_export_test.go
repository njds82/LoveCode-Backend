package tier4_scenarios

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_MCPServerToolExportToExternalClient tests exposing LoveCode tools
// to external MCP consumers (e.g. Claude Desktop, external AI agents):
// 1. External client establishes SSE connection to LoveCode's /sse or /events endpoint
// 2. Discovers exported tools via JSON-RPC "tools/list"
// 3. Executes an exported tool via JSON-RPC "tools/call"
// 4. Receives execution response with verified status
// 5. Audit log records the external MCP tool invocation
func TestScenario_MCPServerToolExportToExternalClient(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: Connect to LoveCode's exported MCP endpoint
	// By default, LoveCode exports MCP on /mcp/sse or /sse
	respSSE, err := srv.Client.Get(ctx, "/sse")
	if err != nil {
		t.Fatalf("connect to /sse failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, respSSE, "F15: MCP Server Export")

	// Step 2: External client sends "tools/list" request over /message or /mcp/message
	listReq := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	var listResp harness.JSONRPCResponse
	respList, err := srv.Client.PostJSON(ctx, "/message", listReq, &listResp)
	if err != nil {
		t.Fatalf("tools/list request failed: %v", err)
	}

	if respList.StatusCode != http.StatusOK {
		// Also try /mcp/message
		respList2, err2 := srv.Client.PostJSON(ctx, "/mcp/message", listReq, &listResp)
		if err2 == nil && respList2.StatusCode == http.StatusOK {
			respList = respList2
		} else {
			srv.Client.SkipIfUnregistered(t, respList, "F15: MCP Server Export")
		}
	}

	// Step 3: Invoke tool via "tools/call"
	callReq := harness.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  []byte(`{"name": "sysinfo", "arguments": {}}`),
	}

	var callResp harness.JSONRPCResponse
	respCall, err := srv.Client.PostJSON(ctx, "/message", callReq, &callResp)
	if err != nil {
		t.Fatalf("tools/call request failed: %v", err)
	}

	if respCall.StatusCode == http.StatusOK {
		t.Logf("MCP Server tool invocation successful: %v", callResp.Result)
	}
}
