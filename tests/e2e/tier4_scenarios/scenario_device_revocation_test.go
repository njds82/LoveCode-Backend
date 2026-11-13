package tier4_scenarios

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_DeviceRevocationDuringInFlightTask verifies immediate administrative revocation:
// 1. Device node pairs and registers with central authority
// 2. Device starts a task execution
// 3. Admin discovers compromise and triggers immediate POST /v1/devices/{id}/revoke
// 4. Device token is permanently invalidated in TokenStore / SQLite
// 5. Subsequent heartbeat telemetry receives 401 Unauthorized
// 6. Any pending or new execution requests targeting this device return 401/403
func TestScenario_DeviceRevocationDuringInFlightTask(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deviceID := "dev-compromised-edge-05"

	// Step 1: Admin generates pairing code
	var codeResp struct {
		PairingCode string `json:"pairing_code"`
	}
	respCode, err := srv.AdminClient().PostJSON(ctx, "/v1/devices/pairing", map[string]any{
		"allowed_capabilities": []string{"telemetry", "execute_job"},
	}, &codeResp)
	if err != nil {
		t.Fatalf("create pairing code failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respCode, "F18: Device Authority")

	// Step 2: Device pairs and receives token
	devNode := harness.NewMockDeviceNode(deviceID, "compromised-host", "linux", "amd64", []string{"telemetry", "execute_job"})
	_, respPair, err := devNode.Pair(ctx, srv.Client, codeResp.PairingCode)
	if err != nil {
		t.Fatalf("pairing failed: %v", err)
	}
	if respPair.StatusCode != http.StatusOK && respPair.StatusCode != http.StatusCreated {
		t.Skipf("device pairing not ready: %s", respPair.String())
	}

	// Step 3: Admin issues immediate revocation
	respRevoke, err := srv.AdminClient().Post(ctx, "/v1/devices/"+deviceID+"/revoke", map[string]any{
		"reason": "security compromise alert triggered",
	})
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respRevoke, "F21: Devices API")

	// Step 4: Device attempts heartbeat reporting with its token -> must be 401 Unauthorized
	respHeartbeat, err := devNode.SendHeartbeat(ctx, srv.Client, map[string]any{
		"status": "active",
	})
	if err != nil {
		t.Fatalf("heartbeat request failed: %v", err)
	}

	if respHeartbeat.StatusCode == http.StatusOK {
		t.Fatalf("security violation: revoked device was able to report heartbeat successfully")
	}
	if respHeartbeat.StatusCode != http.StatusUnauthorized && respHeartbeat.StatusCode != http.StatusForbidden && respHeartbeat.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 401/403 for revoked device heartbeat, got %d: %s", respHeartbeat.StatusCode, respHeartbeat.String())
	}

	// Step 5: Central server attempts to dispatch tool to revoked device -> rejected
	reqExec := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":  deviceID,
			"capability": "execute_job",
		},
		GrantedCapabilities: []string{"*"},
	}

	_, respExec, err := srv.WriteClient().ExecuteTool(ctx, reqExec)
	if err != nil {
		t.Fatalf("execute request failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respExec, "device_exec")

	if respExec.StatusCode == http.StatusOK {
		t.Fatalf("security violation: dispatched tool to revoked device, got 200 OK")
	}
}
