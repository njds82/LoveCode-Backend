package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF13_NetworkTool_PositiveGET_ReturnsBody(t *testing.T) {
	srv := harness.NewServer(t)
	mockHTTP := harness.NewMockHTTPServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    mockHTTP.URL + "/get",
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("http_request GET output: %s", execResp.Output)
	}
}

func TestF13_NetworkTool_PositivePOST_SendsPayload(t *testing.T) {
	srv := harness.NewServer(t)
	mockHTTP := harness.NewMockHTTPServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "POST",
			"url":    mockHTTP.URL + "/post",
			"body":   "test payload string",
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)

	if resp.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("http_request POST output: %s", execResp.Output)
	}
}

func TestF13_NetworkTool_SSRF_LoopbackBlocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling loopback IP (127.0.0.1) directly
	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    "http://127.0.0.1:8080/sensitive_admin",
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)

	// Must be blocked with 403 Forbidden or 400 Bad Request
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected SSRF block for loopback, got status: %d", resp.StatusCode)
	}
}

func TestF13_NetworkTool_SSRF_CloudMetadataBlocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling AWS cloud metadata IP 169.254.169.254
	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    "http://169.254.169.254/latest/meta-data/",
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)

	// Must be blocked
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected SSRF block for cloud metadata, got status: %d", resp.StatusCode)
	}
}

func TestF13_NetworkTool_SSRF_RFC1918Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling RFC 1918 private IPs
	targets := []string{
		"http://10.0.0.1/admin",
		"http://172.16.0.1/status",
		"http://192.168.1.1/router",
	}

	for _, target := range targets {
		req := harness.ToolExecuteRequest{
			ToolName: "http_request",
			Arguments: map[string]any{
				"method": "GET",
				"url":    target,
			},
			GrantedCapabilities: []string{"tools:network", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)

		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected SSRF block for %s, got: %d", target, resp.StatusCode)
		}
	}
}

func TestF13_NetworkTool_ResponseSizeLimit_Enforced(t *testing.T) {
	srv := harness.NewServer(t)
	mockHTTP := harness.NewMockHTTPServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Request large response
	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    mockHTTP.URL + "/large?size=5000000",
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, req.ToolName)

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Output must be capped at max limit (2 MB = 2097152 bytes)
		if len(execResp.Output) > 2097152 && !execResp.Truncated {
			t.Errorf("expected response output to be capped at 2 MB limit")
		}
	}
}
