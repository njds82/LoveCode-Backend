package harness

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestHelperMCPStdioProcess(t *testing.T) {
	// Dummy test target for -test.run=TestHelperMCPStdioProcess
}

func TestHarness_ServerStartupAndHealth(t *testing.T) {
	srv := NewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.Client.Get(ctx, "/v1/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	srv.Client.AssertStatus(t, resp, http.StatusOK)
}

func TestHarness_TokensAndAuth(t *testing.T) {
	srv := NewServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Unauthenticated request to protected endpoint /v1/status should fail with 401
	unauth := srv.UnauthClient()
	resp, err := unauth.Get(ctx, "/v1/status")
	if err != nil {
		t.Fatalf("unauth request failed: %v", err)
	}
	unauth.AssertStatus(t, resp, http.StatusUnauthorized)

	// Read client with read scope should succeed on /v1/status
	reader := srv.ReadClient()
	resp, err = reader.Get(ctx, "/v1/status")
	if err != nil {
		t.Fatalf("read request failed: %v", err)
	}
	reader.AssertStatus(t, resp, http.StatusOK)
}

func TestHarness_Sandbox(t *testing.T) {
	sb := NewSandbox(t)
	sb.WriteFile(t, "hello.txt", []byte("world"))
	sb.AssertFileExists(t, "hello.txt")
	sb.AssertFileEquals(t, "hello.txt", "world")
	sb.AssertFileNotExists(t, "missing.txt")

	repoDir := sb.InitGitRepo(t, "repo")
	if repoDir == "" {
		t.Fatal("expected non-empty repoDir")
	}
	sb.AssertFileExists(t, "repo/README.md")
}

func TestHarness_MockHTTPServer(t *testing.T) {
	mockHTTP := NewMockHTTPServer(t)

	client := NewClient(mockHTTP.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Get(ctx, "/get")
	if err != nil {
		t.Fatalf("mock http get failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)

	reqs := mockHTTP.GetRequests()
	if len(reqs) == 0 {
		t.Fatal("expected at least 1 recorded request")
	}
}

func TestHarness_MockMCPSSEServer(t *testing.T) {
	mcpServer := NewMockMCPSSEServer(t)

	client := NewClient(mcpServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initReq := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	}
	var initResp JSONRPCResponse
	resp, err := client.PostJSON(ctx, "/message", initReq, &initResp)
	if err != nil {
		t.Fatalf("mcp initialize failed: %v", err)
	}
	client.AssertStatus(t, resp, http.StatusOK)
	if initResp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", initResp.Error)
	}
}

func TestHarness_MockDeviceNode(t *testing.T) {
	dev := NewMockDeviceNode("dev-1", "test-host", "linux", "amd64", []string{"device:gpio"})
	addr := dev.StartTLSDaemon(t)
	if addr == "" {
		t.Fatal("expected non-empty TLS daemon address")
	}
}
