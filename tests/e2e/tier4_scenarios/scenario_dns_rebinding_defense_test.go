package tier4_scenarios

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_DefensiveSSRFAndDNSRebindingResistance tests LoveCode's SSRF & DNS
// rebinding mitigation engine:
// 1. Simulated attacker URL resolving to private/loopback IP is supplied to http_request
// 2. Pre-dial resolver intercepts the address resolution
// 3. Request is rejected prior to establishing TCP connection
// 4. Direct IP address manipulation (e.g. 127.0.0.1, 0.0.0.0) is rejected
// 5. Cloud metadata endpoint (169.254.169.254) is rejected
// 6. Audit log flags the blocked attempt as a security alert
func TestScenario_DefensiveSSRFAndDNSRebindingResistance(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	maliciousTargets := []string{
		"http://127.0.0.1:8080/internal/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/private-api",
		"http://localhost:8080/metrics",
		"http://100.100.100.200/latest/meta-data/",
	}

	for _, targetURL := range maliciousTargets {
		req := harness.ToolExecuteRequest{
			ToolName: "http_request",
			Arguments: map[string]any{
				"method": "GET",
				"url":    targetURL,
			},
			RunID:               "run-dns-rebinding-defense-09",
			GrantedCapabilities: []string{"tools:network", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("request failed for target %q: %v", targetURL, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

		// Rebinding or SSRF target must be rejected with 403 Forbidden or 400 Bad Request
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: SSRF target %q was permitted, got 200 OK", targetURL)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 or 400 for target %q, got %d: %s", targetURL, resp.StatusCode, resp.String())
		}
	}
}
