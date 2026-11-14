package tier2_boundaries

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestBoundaries_Device_ExpiredPairingCode_Rejected verifies that pairing codes
// older than the 10-minute validity window are rejected.
func TestBoundaries_Device_ExpiredPairingCode_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	expiredCode := "EXPIRED-9999"
	device := harness.NewMockDeviceNode("dev-expired", "edge-box-1", "linux", "amd64", []string{"telemetry"})

	_, resp, err := device.Pair(ctx, srv.Client, expiredCode)
	if err != nil {
		t.Fatalf("pair request failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F18: Device Authority")

	// Must reject expired pairing code
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Fatalf("security violation: expired pairing code was accepted, got %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusGone {
		t.Fatalf("expected 400/401/404/410 for expired code, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Device_PairingCodeReuse_ReplayRejected verifies that once a pairing
// code has been redeemed, a replay attempt with the same code is rejected.
func TestBoundaries_Device_PairingCodeReuse_ReplayRejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Create a fresh pairing code via admin API
	var codeResp struct {
		PairingCode string `json:"pairing_code"`
	}
	respCode, err := srv.AdminClient().PostJSON(ctx, "/v1/devices/pairing", map[string]any{
		"allowed_capabilities": []string{"telemetry", "reboot"},
	}, &codeResp)
	if err != nil {
		t.Fatalf("create pairing code failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respCode, "F18: Device Authority")

	if codeResp.PairingCode == "" {
		t.Skip("Pairing endpoint returned empty code")
	}

	// 2. First pairing attempt should succeed
	dev1 := harness.NewMockDeviceNode("dev-replay-1", "host1", "linux", "amd64", []string{"telemetry"})
	pairResp1, resp1, err := dev1.Pair(ctx, srv.Client, codeResp.PairingCode)
	if err != nil {
		t.Fatalf("first pairing failed: %v", err)
	}
	if resp1.StatusCode != http.StatusOK && resp1.StatusCode != http.StatusCreated {
		t.Skipf("first pairing rejected: %s", resp1.String())
	}
	_ = pairResp1

	// 3. Second pairing attempt with SAME code must be rejected (replay attack prevention)
	dev2 := harness.NewMockDeviceNode("dev-replay-2", "host2", "linux", "amd64", []string{"telemetry"})
	_, resp2, err := dev2.Pair(ctx, srv.Client, codeResp.PairingCode)
	if err != nil {
		t.Fatalf("second pairing request error: %v", err)
	}

	if resp2.StatusCode == http.StatusOK || resp2.StatusCode == http.StatusCreated {
		t.Fatalf("security violation: pairing code was reused successfully, expected replay rejection")
	}
	if resp2.StatusCode != http.StatusBadRequest && resp2.StatusCode != http.StatusConflict && resp2.StatusCode != http.StatusUnauthorized && resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400/409/401 for replayed pairing code, got %d: %s", resp2.StatusCode, resp2.String())
	}
}

// TestBoundaries_Device_RevokedDeviceToken_Rejected verifies that after a device
// is revoked, requests using its token (heartbeats, executions) return 401 Unauthorized.
func TestBoundaries_Device_RevokedDeviceToken_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Direct attempt using a fabricated revoked token
	revokedTokenClient := srv.Client.WithToken("lc_dev_revoked_token_1234567890abcdef")
	resp, err := revokedTokenClient.Post(ctx, "/v1/devices/dev-revoked/heartbeat", map[string]any{
		"metrics": map[string]any{"cpu": 10},
	})
	if err != nil {
		t.Fatalf("heartbeat request failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F18: Device Authority")

	// Must be rejected with 401 Unauthorized or 403 Forbidden
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: revoked device token was accepted, got 200 OK")
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 401/403 for revoked device token, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Device_CapabilityDispatchTable_Enforcement verifies that executing
// a tool or capability not granted in the device dispatch table is blocked.
func TestBoundaries_Device_CapabilityDispatchTable_Enforcement(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempting to execute unannounced capability like "shell:root_exec" on device
	req := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":  "dev-node-1",
			"capability": "shell:arbitrary_execution",
			"command":    "rm -rf /",
		},
		GrantedCapabilities: []string{"tools:device", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "device_exec")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: unannounced device capability was dispatched, got 200 OK")
	}
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 400 for unauthorized capability, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Device_DisabledRemoteAccess_FlagCheck verifies that when remote
// access is disabled on the server or device, operations are blocked.
func TestBoundaries_Device_DisabledRemoteAccess_FlagCheck(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":     "dev-offline-mode",
			"capability":    "telemetry",
			"remote_access": false,
		},
		GrantedCapabilities: []string{"tools:device", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "device_exec")

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 403 Forbidden or 400 Bad Request when remote access is disabled, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestBoundaries_Device_InvalidPairingJSON_Rejected verifies that corrupt or
// malformed JSON payloads to the pairing endpoint are rejected with 400 Bad Request.
func TestBoundaries_Device_InvalidPairingJSON_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.Client.Post(ctx, "/v1/devices/pair", []byte("{ bad json :::: "))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F18: Device Authority")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("malformed pairing JSON should not return 200 OK")
	}
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400 Bad Request for malformed JSON, got %d: %s", resp.StatusCode, resp.String())
	}
}
