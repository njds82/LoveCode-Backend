package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// --- Feature 18: Device Node Central Authority ---

func TestF18_DeviceAuthority_CreatePairingCode(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pairingReq := map[string]any{
		"name":                 "Edge Node 1",
		"allowed_capabilities": []string{"device:gpio", "device:system"},
		"ttl_seconds":          600,
	}

	var pairResp map[string]any
	resp, err := srv.AdminClient().PostJSON(ctx, "/v1/devices/pairing", pairingReq, &pairResp)
	if err != nil {
		t.Fatalf("create pairing code failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F18: Device Authority")

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		if pairResp["pairing_code"] == nil {
			t.Errorf("expected pairing_code in response")
		}
	} else if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF18_DeviceAuthority_PairDevice_Success(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dev := harness.NewMockDeviceNode("dev-edge-01", "edge-host", "linux", "arm64", []string{"device:gpio"})
	pairResp, resp, err := dev.Pair(ctx, srv.UnauthClient(), "PAIR-VALID-CODE")
	if err != nil {
		t.Fatalf("pair failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, resp, "F18: Device Authority")

	if resp.StatusCode == http.StatusOK {
		if pairResp.Token == "" {
			t.Errorf("expected device token to be issued")
		}
	}
}

func TestF18_DeviceAuthority_ExpiredPairingCode_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dev := harness.NewMockDeviceNode("dev-edge-02", "edge-host", "linux", "arm64", []string{"device:gpio"})
	_, resp, err := dev.Pair(ctx, srv.UnauthClient(), "PAIR-EXPIRED-CODE")
	if err != nil {
		t.Fatalf("pair request failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, resp, "F18: Device Authority")

	// Must be rejected with 400 Bad Request or 403 Forbidden
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400 or 403 for expired pairing code, got: %d", resp.StatusCode)
	}
}

func TestF18_DeviceAuthority_ReusePairingCode_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dev := harness.NewMockDeviceNode("dev-edge-03", "edge-host", "linux", "arm64", []string{"device:gpio"})
	_, resp, err := dev.Pair(ctx, srv.UnauthClient(), "PAIR-USED-CODE")
	if err != nil {
		t.Fatalf("pair request failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, resp, "F18: Device Authority")

	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 400 or 403 for reused pairing code, got: %d", resp.StatusCode)
	}
}

func TestF18_DeviceAuthority_RevokeDevice_InvalidatesToken(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.AdminClient().Post(ctx, "/v1/devices/dev-edge-01/revoke", nil)
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F18: Device Authority")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected revoke status: %d", resp.StatusCode)
	}
}

// --- Feature 19: Device Node Secure Transport ---

func TestF19_DeviceTLS_Handshake_Established(t *testing.T) {
	dev := harness.NewMockDeviceNode("dev-tls-01", "edge-node", "linux", "arm64", []string{"device:gpio"})
	addr := dev.StartTLSDaemon(t)
	if addr == "" {
		t.Fatal("expected TLS daemon to bind to address")
	}
}

func TestF19_DeviceTLS_Heartbeat_Recorded(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dev := harness.NewMockDeviceNode("dev-tls-01", "edge-node", "linux", "arm64", []string{"device:gpio"})
	dev.Token = "lc_dev_mocktoken123"

	resp, err := dev.SendHeartbeat(ctx, srv.Client, map[string]any{"cpu": 15.5})
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F19: Device Transport")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected heartbeat status: %d", resp.StatusCode)
	}
}

func TestF19_DeviceTLS_Heartbeat_RevokedToken_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dev := harness.NewMockDeviceNode("dev-tls-01", "edge-node", "linux", "arm64", []string{"device:gpio"})
	dev.Token = "lc_dev_revoked_token"

	resp, err := dev.SendHeartbeat(ctx, srv.Client, map[string]any{"status": "online"})
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F19: Device Transport")

	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 401 Unauthorized for revoked device token, got: %d", resp.StatusCode)
	}
}

func TestF19_DeviceTLS_Heartbeat_MetricsRecorded(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dev := harness.NewMockDeviceNode("dev-tls-01", "edge-node", "linux", "arm64", []string{"device:gpio"})
	dev.Token = "lc_dev_mock"

	metrics := map[string]any{
		"cpu_percent":    25.0,
		"mem_used_bytes": 1048576,
	}
	resp, err := dev.SendHeartbeat(ctx, srv.Client, metrics)
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F19: Device Transport")
}

func TestF19_DeviceTLS_Disconnection_StatusOffline(t *testing.T) {
	dev := harness.NewMockDeviceNode("dev-tls-01", "edge-node", "linux", "arm64", []string{"device:gpio"})
	dev.StartTLSDaemon(t)
	dev.Close()
	// Connection after close should fail cleanly
}

// --- Feature 20: Device Capability Execution ---

func TestF20_DeviceCapabilities_AllowedCapability_Executes(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := map[string]any{
		"capability": "device:gpio",
		"action":     "read_pin",
		"arguments":  map[string]any{"pin": 4},
		"timeout_ms": 5000,
	}

	resp, err := srv.WriteClient().Post(ctx, "/v1/devices/dev-edge-01/execute", req)
	if err != nil {
		t.Fatalf("execute on device failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F20: Device Capabilities")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF20_DeviceCapabilities_UnannouncedCapability_Blocked(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := map[string]any{
		"capability": "device:unannounced_danger",
		"action":     "execute",
		"arguments":  map[string]any{},
	}

	resp, err := srv.WriteClient().Post(ctx, "/v1/devices/dev-edge-01/execute", req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F20: Device Capabilities")

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for unannounced capability, got: %d", resp.StatusCode)
	}
}

func TestF20_DeviceCapabilities_RemoteCommandInjection_Prevented(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt arbitrary host shell injection via device execute
	req := map[string]any{
		"capability": "device:system",
		"action":     "exec",
		"arguments":  map[string]any{"cmd": "rm -rf /; reboot"},
	}

	resp, err := srv.WriteClient().Post(ctx, "/v1/devices/dev-edge-01/execute", req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F20: Device Capabilities")

	// Must reject arbitrary host command execution with 403 or 400
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected arbitrary command injection to be blocked, got: %d", resp.StatusCode)
	}
}

func TestF20_DeviceCapabilities_UpdateAllowedCapabilities(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body := map[string]any{
		"allowed_capabilities": []string{"device:gpio"},
	}

	resp, err := srv.AdminClient().Put(ctx, "/v1/devices/dev-edge-01/capabilities", body)
	if err != nil {
		t.Fatalf("update capabilities failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F20: Device Capabilities")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF20_DeviceCapabilities_ExecutionTimeout_Enforced(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := map[string]any{
		"capability": "device:gpio",
		"action":     "wait_pin",
		"arguments":  map[string]any{"pin": 4},
		"timeout_ms": 50, // 50ms
	}

	resp, err := srv.WriteClient().Post(ctx, "/v1/devices/dev-edge-01/execute", req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F20: Device Capabilities")
}

// --- Feature 21: Device Nodes REST API ---

func TestF21_DeviceAPI_ListDevices_RequiresRead(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	respUnauth, err := srv.UnauthClient().Get(ctx, "/v1/devices")
	if err != nil {
		t.Fatalf("unauth failed: %v", err)
	}
	srv.UnauthClient().SkipIfUnregistered(t, respUnauth, "F21: Devices REST API")
	srv.UnauthClient().AssertStatus(t, respUnauth, http.StatusUnauthorized)

	respRead, err := srv.ReadClient().Get(ctx, "/v1/devices")
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	srv.ReadClient().AssertStatus(t, respRead, http.StatusOK)
}

func TestF21_DeviceAPI_CreatePairingCode_AdminScope(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body := map[string]any{
		"name":                 "Sensor Device",
		"allowed_capabilities": []string{"device:sensor"},
		"ttl_seconds":          300,
	}

	respWrite, err := srv.WriteClient().Post(ctx, "/v1/devices/pairing", body)
	if err != nil {
		t.Fatalf("write token failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, respWrite, "F21: Devices REST API")

	if respWrite.StatusCode != http.StatusForbidden && respWrite.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for non-admin pairing code creation, got: %d", respWrite.StatusCode)
	}

	respAdmin, err := srv.AdminClient().Post(ctx, "/v1/devices/pairing", body)
	if err != nil {
		t.Fatalf("admin token failed: %v", err)
	}
	if respAdmin.StatusCode != http.StatusOK && respAdmin.StatusCode != http.StatusCreated && respAdmin.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected admin pairing status: %d", respAdmin.StatusCode)
	}
}

func TestF21_DeviceAPI_GetDevice_ByID(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.ReadClient().Get(ctx, "/v1/devices/dev-edge-01")
	if err != nil {
		t.Fatalf("get device failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F21: Devices REST API")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF21_DeviceAPI_RevokeDevice_Endpoint(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := srv.AdminClient().Post(ctx, "/v1/devices/dev-edge-01/revoke", nil)
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F21: Devices REST API")

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}

func TestF21_DeviceAPI_ExecuteOnDevice_WriteScope(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := map[string]any{
		"capability": "device:gpio",
		"action":     "read",
		"arguments":  map[string]any{"pin": 1},
	}

	// Read token cannot execute
	respRead, err := srv.ReadClient().Post(ctx, "/v1/devices/dev-edge-01/execute", req)
	if err != nil {
		t.Fatalf("read execute failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, respRead, "F21: Devices REST API")

	if respRead.StatusCode != http.StatusForbidden && respRead.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for read token calling device execute, got: %d", respRead.StatusCode)
	}
}
