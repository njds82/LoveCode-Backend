package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateIP_BlocksRestrictedSubnets(t *testing.T) {
	blockedIPs := []string{
		// Loopback
		"127.0.0.1", "127.0.0.2", "127.255.255.254", "::1", "::ffff:127.0.0.1",
		// RFC 1918 Private
		"10.0.0.1", "10.255.255.254", "172.16.0.1", "172.31.255.254", "192.168.0.1", "192.168.1.254",
		// Link-Local
		"169.254.1.1", "169.254.254.254", "fe80::1", "::ffff:169.254.1.1",
		// Cloud Metadata
		"169.254.169.254", "100.100.100.200",
		// Unspecified & Broadcast
		"0.0.0.0", "255.255.255.255", "::",
		// Carrier-Grade NAT (RFC 6598)
		"100.64.0.1", "100.127.255.254",
	}

	for _, ipStr := range blockedIPs {
		t.Run(ipStr, func(t *testing.T) {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", ipStr)
			}
			err := ValidateIP(ip)
			if err == nil {
				t.Errorf("expected ValidateIP to block %s, got nil", ipStr)
			}
			if !errors.Is(err, ErrSSRFBlocked) {
				t.Errorf("expected ErrSSRFBlocked, got: %v", err)
			}
		})
	}
}

func TestValidateIP_AllowsPublicIPs(t *testing.T) {
	publicIPs := []string{
		"8.8.8.8",
		"1.1.1.1",
		"93.184.216.34",
		"2606:4700:4700::1111",
	}

	for _, ipStr := range publicIPs {
		t.Run(ipStr, func(t *testing.T) {
			ip := net.ParseIP(ipStr)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", ipStr)
			}
			if err := ValidateIP(ip); err != nil {
				t.Errorf("expected ValidateIP to allow public IP %s, got %v", ipStr, err)
			}
		})
	}
}

func TestIsBlockedHostname(t *testing.T) {
	blocked := []string{
		"localhost", "LOCALHOST", "myhost.localhost", "app.local", "metadata.google.internal", "instance-data",
	}
	for _, h := range blocked {
		if !IsBlockedHostname(h) {
			t.Errorf("expected hostname %q to be blocked", h)
		}
	}

	allowed := []string{
		"example.com", "api.github.com", "google.com",
	}
	for _, h := range allowed {
		if IsBlockedHostname(h) {
			t.Errorf("expected hostname %q to be allowed", h)
		}
	}
}

func TestClient_SSRFProtection_BlocksLocalhostAndPrivate(t *testing.T) {
	client := NewClient(3)
	ctx := context.Background()

	targets := []string{
		"http://127.0.0.1:8080/sensitive",
		"http://localhost:8080/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/router",
		"http://172.16.0.1/secret",
		"http://192.168.1.1/setup",
		"http://100.100.100.200/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://instance-data/latest/meta-data/",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			_, err := client.Execute(ctx, RequestArgs{URL: target})
			if err == nil {
				t.Fatalf("expected SSRF block for %q, got nil error", target)
			}
			if !errors.Is(err, ErrSSRFBlocked) {
				t.Errorf("expected ErrSSRFBlocked for %q, got: %v", target, err)
			}
		})
	}
}

func TestClient_RedirectHopLimit(t *testing.T) {
	hopCount := 0
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hopCount++
		http.Redirect(w, r, fmt.Sprintf("%s/redirect-%d", ts.URL, hopCount), http.StatusFound)
	}))
	defer ts.Close()

	// Client with max 3 redirects and loopback allowed for the mock server
	client := NewClientWithOptions(ClientOptions{
		MaxRedirects:  3,
		AllowLoopback: true,
	})

	_, err := client.Execute(context.Background(), RequestArgs{
		URL: ts.URL + "/start",
	})
	if err == nil {
		t.Fatal("expected redirect limit error, got nil")
	}
	if !strings.Contains(err.Error(), "stopped after 3 redirects") {
		t.Errorf("expected 'stopped after 3 redirects', got: %v", err)
	}
}

func TestClient_RedirectToPrivateIP_Blocked(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/secret", http.StatusFound)
	}))
	defer ts.Close()

	// Even with loopback allowed for the test server, redirecting to cloud metadata is blocked!
	client := NewClientWithOptions(ClientOptions{
		MaxRedirects:  3,
		AllowLoopback: true,
	})
	_, err := client.Execute(context.Background(), RequestArgs{
		URL: ts.URL,
	})
	if err == nil {
		t.Fatal("expected SSRF error when redirecting to 169.254.169.254, got nil")
	}
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Errorf("expected ErrSSRFBlocked, got: %v", err)
	}
}

func TestClient_ResponseTruncation(t *testing.T) {
	largeBody := strings.Repeat("X", 500)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(largeBody))
	}))
	defer ts.Close()

	client := NewClientWithOptions(ClientOptions{
		AllowLoopback: true,
	})

	res, err := client.Execute(context.Background(), RequestArgs{
		URL:              ts.URL,
		MaxResponseBytes: 100,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Truncated {
		t.Errorf("expected response to be marked truncated")
	}
	if len(res.Body) != 100 {
		t.Errorf("expected 100 bytes, got %d", len(res.Body))
	}
}
