package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// MockMCPSSECall records a tool execution call received by the SSE mock.
type MockMCPSSECall struct {
	ToolName  string
	Arguments map[string]any
}

// MockMCPSSEServer is an HTTP/SSE server implementing the Model Context Protocol.
type MockMCPSSEServer struct {
	Server   *httptest.Server
	URL      string
	Calls    []MockMCPSSECall
	sseChans map[chan string]struct{}
	mu       sync.Mutex
	closed   bool
}

// NewMockMCPSSEServer starts a new mock MCP HTTP/SSE server.
func NewMockMCPSSEServer(t testing.TB) *MockMCPSSEServer {
	t.Helper()

	s := &MockMCPSSEServer{
		sseChans: make(map[chan string]struct{}),
	}

	mux := http.NewServeMux()

	// SSE streams on /events and /sse
	sseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Initial endpoint event
		fmt.Fprintf(w, "event: endpoint\ndata: /message\n\n")
		flusher.Flush()

		msgChan := make(chan string, 16)
		s.mu.Lock()
		s.sseChans[msgChan] = struct{}{}
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			delete(s.sseChans, msgChan)
			close(msgChan)
			s.mu.Unlock()
		}()

		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case msg, ok := <-msgChan:
				if !ok {
					return
				}
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				flusher.Flush()
			}
		}
	})

	mux.Handle("GET /events", sseHandler)
	mux.Handle("GET /sse", sseHandler)

	// JSON-RPC message receiver on /message
	mux.HandleFunc("POST /message", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &JSONRPCError{Code: -32700, Message: "Parse error"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		resp := s.handleRPC(req)
		respBytes, _ := json.Marshal(resp)

		// Broadcast to SSE streams
		s.mu.Lock()
		for ch := range s.sseChans {
			select {
			case ch <- string(respBytes):
			default:
			}
		}
		s.mu.Unlock()

		// Also return direct HTTP response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	})

	s.Server = httptest.NewServer(mux)
	s.URL = s.Server.URL

	t.Cleanup(func() {
		s.Close()
	})

	return s
}

func (s *MockMCPSSEServer) handleRPC(req JSONRPCRequest) JSONRPCResponse {
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
					"name":    "mock-mcp-sse",
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
						"name":        "sse_echo",
						"description": "SSE Echo tool",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"text": map[string]any{"type": "string"},
							},
							"required": []string{"text"},
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
		_ = json.Unmarshal(req.Params, &params)

		s.mu.Lock()
		s.Calls = append(s.Calls, MockMCPSSECall{
			ToolName:  params.Name,
			Arguments: params.Arguments,
		})
		s.mu.Unlock()

		text, _ := params.Arguments["text"].(string)
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "sse-echo: " + text},
				},
				"isError": false,
			},
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

// Close stops the mock SSE server.
func (s *MockMCPSSEServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.Server != nil {
		s.Server.Close()
	}
}

// GetCalls returns recorded tool calls.
func (s *MockMCPSSEServer) GetCalls() []MockMCPSSECall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MockMCPSSECall, len(s.Calls))
	copy(out, s.Calls)
	return out
}
