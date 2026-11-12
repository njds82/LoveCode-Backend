package tier2_boundaries

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_SSRF_Loopback_Blocked verifies that requests targeting loopback
// interfaces (127.0.0.1, ::1, localhost, 127.0.0.2) are strictly blocked.
func TestBoundaries_SSRF_Loopback_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	loopbacks := []string{
		"http://127.0.0.1:8080/internal",
		"http://127.0.0.2:80/admin",
		"http://127.255.255.254:80/",
		"http://localhost:8080/secret",
		"http://[::1]:8080/metrics",
		"http://0.0.0.0:8080/status",
	}

	for _, url := range loopbacks {
		req := harness.ToolExecuteRequest{
			ToolName: "http_request",
			Arguments: map[string]any{
				"method": "GET",
				"url":    url,
			},
			GrantedCapabilities: []string{"tools:network", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for loopback URL %q: %v", url, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: loopback request %q was permitted, got 200 OK", url)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request for %q, got %d: %s", url, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_SSRF_RFC1918_PrivateIPs_Blocked verifies that private IPv4 addresses
// (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) are blocked to prevent intranet scans.
func TestBoundaries_SSRF_RFC1918_PrivateIPs_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	privateURLs := []string{
		"http://10.0.0.1/admin",
		"http://10.255.255.254:8080/",
		"http://172.16.0.1/internal",
		"http://172.31.255.254/",
		"http://192.168.0.1/router",
		"http://192.168.1.254:9000/",
	}

	for _, url := range privateURLs {
		req := harness.ToolExecuteRequest{
			ToolName: "http_request",
			Arguments: map[string]any{
				"method": "GET",
				"url":    url,
			},
			GrantedCapabilities: []string{"tools:network", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for private URL %q: %v", url, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: private IP request %q was permitted, got 200 OK", url)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 Forbidden or 400 Bad Request for %q, got %d: %s", url, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_SSRF_LinkLocal_Blocked verifies that link-local addresses
// (169.254.0.0/16 and fe80::/10) are blocked.
func TestBoundaries_SSRF_LinkLocal_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	linkLocalURLs := []string{
		"http://169.254.1.1/config",
		"http://169.254.254.254/",
		"http://[fe80::1]/linklocal",
	}

	for _, url := range linkLocalURLs {
		req := harness.ToolExecuteRequest{
			ToolName: "http_request",
			Arguments: map[string]any{
				"method": "GET",
				"url":    url,
			},
			GrantedCapabilities: []string{"tools:network", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for link-local URL %q: %v", url, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: link-local request %q was permitted, got 200 OK", url)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 or 400 for %q, got %d: %s", url, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_SSRF_CloudMetadata_Blocked verifies that known cloud metadata
// endpoints (AWS, GCP, Azure, Alibaba) are strictly blocked.
func TestBoundaries_SSRF_CloudMetadata_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cloudMetadataURLs := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.169.254/computeMetadata/v1/",
		"http://100.100.100.200/latest/meta-data/", // Alibaba Cloud metadata
		"http://instance-data/latest/meta-data/",
		"http://metadata.google.internal/",
	}

	for _, url := range cloudMetadataURLs {
		req := harness.ToolExecuteRequest{
			ToolName: "http_request",
			Arguments: map[string]any{
				"method": "GET",
				"url":    url,
			},
			GrantedCapabilities: []string{"tools:network", "*"},
		}

		_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
		if err != nil {
			t.Fatalf("execute failed for cloud metadata URL %q: %v", url, err)
		}
		srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

		if resp.StatusCode == http.StatusOK {
			t.Fatalf("security violation: cloud metadata request %q was permitted, got 200 OK", url)
		}
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 403 or 400 for cloud metadata %q, got %d: %s", url, resp.StatusCode, resp.String())
		}
	}
}

// TestBoundaries_SSRF_RedirectLimits_MaxHops verifies that redirect chains exceeding
// the maximum hop count (3 hops) or redirecting to private addresses are blocked.
func TestBoundaries_SSRF_RedirectLimits_MaxHops(t *testing.T) {
	srv := harness.NewServer(t)
	mockHTTP := harness.NewMockHTTPServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Redirect chain with 5 hops (limit is 3)
	loopURL := fmt.Sprintf("%s/redirect?hops=5&target=/get", mockHTTP.URL)

	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    loopURL,
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

	// Redirect loop exceeding hops must fail
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected redirect loop exceeding 3 hops to fail, got 200 OK")
	}
}

// TestBoundaries_SSRF_RedirectToPrivateIP_Blocked verifies that an initial request
// to a public URL that returns a 302 redirecting to a private/loopback IP is blocked.
func TestBoundaries_SSRF_RedirectToPrivateIP_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	mockHTTP := harness.NewMockHTTPServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Redirect targeting cloud metadata or loopback
	evilRedirectURL := fmt.Sprintf("%s/redirect?hops=1&target=http://169.254.169.254/secret", mockHTTP.URL)

	req := harness.ToolExecuteRequest{
		ToolName: "http_request",
		Arguments: map[string]any{
			"method": "GET",
			"url":    evilRedirectURL,
		},
		GrantedCapabilities: []string{"tools:network", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "http_request")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: redirect to private IP succeeded, got 200 OK")
	}
}
