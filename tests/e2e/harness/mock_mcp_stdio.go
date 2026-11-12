package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func init() {
	if os.Getenv("LOVETEST_MCP_STDIO_HELPER") == "1" {
		RunMCPStdioLoop(os.Stdin, os.Stdout)
		os.Exit(0)
	}
}

// JSONRPCRequest represents a standard JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a standard JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError represents a standard JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// RunMCPStdioLoop reads JSON-RPC requests from r and writes responses to w.
func RunMCPStdioLoop(r io.Reader, w io.Writer) {
	scanner := bufio.NewScanner(r)
	encoder := json.NewEncoder(w)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		// Handle notifications (no ID)
		if req.ID == nil {
			// notification handled silently
			continue
		}

		resp := handleMCPMethod(req)
		_ = encoder.Encode(resp)
	}
}

func handleMCPMethod(req JSONRPCRequest) JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "mock-mcp-stdio",
					"version": "1.0.0",
				},
			},
		}

	case "tools/list":
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": []map[string]any{
					{
						"name":        "mock_echo",
						"description": "Echoes input text",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"message": map[string]any{"type": "string"},
							},
							"required": []string{"message"},
						},
					},
					{
						"name":        "mock_add",
						"description": "Adds two numbers",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"a": map[string]any{"type": "number"},
								"b": map[string]any{"type": "number"},
							},
							"required": []string{"a", "b"},
						},
					},
				},
			},
		}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
			}
		}

		switch params.Name {
		case "mock_echo":
			msg, _ := params.Arguments["message"].(string)
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "echo: " + msg},
					},
					"isError": false,
				},
			}
		case "mock_add":
			var a, b float64
			if v, ok := params.Arguments["a"].(float64); ok {
				a = v
			}
			if v, ok := params.Arguments["b"].(float64); ok {
				b = v
			}
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf("%v", a+b)},
					},
					"isError": false,
				},
			}
		default:
			return JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32601, Message: "Tool not found: " + params.Name},
			}
		}

	case "ping":
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: "Method not found: " + req.Method},
		}
	}
}

// MockMCPStdioProcess manages a running mock MCP stdio helper process.
type MockMCPStdioProcess struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	mu     sync.Mutex
	closed bool
}

// StartMockMCPStdio spawns the test binary in MCP stdio helper mode.
func StartMockMCPStdio(t testing.TB) *MockMCPStdioProcess {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("StartMockMCPStdio: get executable failed: %v", err)
	}

	cmd := exec.Command(exe, "-test.run=TestHelperMCPStdioProcess", "--")
	cmd.Env = append(os.Environ(), "LOVETEST_MCP_STDIO_HELPER=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StartMockMCPStdio: stdin pipe failed: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StartMockMCPStdio: stdout pipe failed: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("StartMockMCPStdio: start failed: %v", err)
	}

	proc := &MockMCPStdioProcess{
		Cmd:    cmd,
		Stdin:  stdin,
		Stdout: stdout,
	}

	t.Cleanup(func() {
		proc.Close()
	})

	return proc
}

// Close kills the mock MCP process and closes pipes.
func (p *MockMCPStdioProcess) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true

	if p.Stdin != nil {
		_ = p.Stdin.Close()
	}
	if p.Stdout != nil {
		_ = p.Stdout.Close()
	}
	if p.Cmd != nil && p.Cmd.Process != nil {
		_ = p.Cmd.Process.Kill()
		_ = p.Cmd.Wait()
	}
}

// Call sends a JSON-RPC request and decodes the response.
func (p *MockMCPStdioProcess) Call(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	reqID := time.Now().UnixNano()
	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = data
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  rawParams,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	reqBytes = append(reqBytes, '\n')

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.Stdin.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("write stdio failed: %w", err)
	}

	reader := bufio.NewReader(p.Stdout)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read stdio failed: %w", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal stdio response failed: %w, line: %s", err, string(line))
	}
	return &resp, nil
}
