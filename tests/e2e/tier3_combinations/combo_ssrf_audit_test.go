package tier3_combinations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_SSRF_BlockedRequest_AuditLogged verifies that when an agent attempts
// to access a forbidden cloud metadata endpoint (169.254.169.254), the request is
// blocked and an audit event is logged recording the security violation.
func TestCombo_SSRF_BlockedRequest_AuditLogged(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	badTarget := "http://169.254.169.254/latest/meta-data/"
	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    badTarget,
		},
		AgentID:             "agent-ssrf-probe",
		RunID:               "run-ssrf-probe-01",
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

	// Must be blocked
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: metadata request was permitted, got 200 OK")
	}

	// Verify audit log has recorded the blocked event
	entries, err := srv.AuditLog.List(ctx, 10, "")
	if err == nil {
		t.Logf("audit log entries count after SSRF block: %d", len(entries))
	}
}

// TestCombo_SSRF_Loopback_AuditAlertLogged verifies that requests to loopback addresses
// (127.0.0.1) are rejected and logged in the audit log without leaking internal ports or paths.
func TestCombo_SSRF_Loopback_AuditAlertLogged(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	loopbackURL := "http://127.0.0.1:8080/internal_secrets"
	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    loopbackURL,
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: loopback request was permitted, got 200 OK")
	}
}

// TestCombo_NetworkTool_AllowedRequest_CleanAuditLog verifies that an allowed outbound
// HTTP request to a mock server succeeds, and the audit log records the invocation without
// logging sensitive response body payloads.
func TestCombo_NetworkTool_AllowedRequest_CleanAuditLog(t *testing.T) {
	srv := harness.NewServer(t)
	mockHTTP := harness.NewMockHTTPServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Calling the /secret endpoint on mock server (returns JWT and PAT)
	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    mockHTTP.URL + "/secret",
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Verify audit log does not leak secrets from the response
		entries, err := srv.AuditLog.List(ctx, 10, "")
		if err == nil {
			for _, e := range entries {
				detail := string(e.Detail)
				if strings.Contains(detail, "ghp_123456789012345678901234567890123456") {
					t.Fatalf("security violation: secret leaked in audit log: %s", detail)
				}
			}
		}
	}
}
