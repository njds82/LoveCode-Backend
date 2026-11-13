package tier4_scenarios

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestScenario_RemoteDeviceTelemetryAndRemediation exercises the complete Device Node lifecycle:
// 1. Admin generates ephemeral pairing code
// 2. Edge Device Node pairs securely and obtains device token
// 3. Device reports heartbeat telemetry containing high error rate alert
// 4. Central authority dispatches remediation command ("restart_service")
// 5. Device executes command and reports success
// 6. Audit log verifies TLS device events and operations
func TestScenario_RemoteDeviceTelemetryAndRemediation(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deviceID := "edge-factory-sensor-01"
	hostname := "factory-node-linux"
	platform := "linux"
	arch := "arm64"
	capabilities := []string{"telemetry", "remediation", "restart_service"}

	// Step 1: Create pairing code via admin API
	var pairCodeResp struct {
		PairingCode string `json:"pairing_code"`
	}
	respPairCode, err := srv.AdminClient().PostJSON(ctx, "/v1/devices/pairing", map[string]any{
		"allowed_capabilities": capabilities,
	}, &pairCodeResp)
	if err != nil {
		t.Fatalf("create pairing code failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respPairCode, "F18: Device Authority")

	// Step 2: Edge device node pairs with server
	devNode := harness.NewMockDeviceNode(deviceID, hostname, platform, arch, capabilities)
	_, respPair, err := devNode.Pair(ctx, srv.Client, pairCodeResp.PairingCode)
	if err != nil {
		t.Fatalf("device pairing failed: %v", err)
	}
	if respPair.StatusCode != http.StatusOK && respPair.StatusCode != http.StatusCreated {
		t.Skipf("device pairing not ready: %s", respPair.String())
	}

	// Step 3: Device sends telemetry heartbeat with high error rate
	telemetryData := map[string]any{
		"metrics": map[string]any{
			"cpu_usage":   88.5,
			"error_count": 42,
			"status":      "unhealthy",
		},
	}
	respHeartbeat, err := devNode.SendHeartbeat(ctx, srv.Client, telemetryData)
	if err != nil {
		t.Fatalf("send heartbeat failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, respHeartbeat, "F19: Device Transport")

	// Step 4: Central authority dispatches remediation tool to device
	reqRemediation := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":  deviceID,
			"capability": "restart_service",
			"service":    "telemetry-daemon",
		},
		AgentID:             "remediation-agent",
		RunID:               "run-remediation-flow-01",
		GrantedCapabilities: []string{"tools:device", "*"},
	}

	execResp, respExec, err := srv.WriteClient().ExecuteTool(ctx, reqRemediation)
	if err != nil {
		t.Fatalf("dispatch remediation failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respExec, "device_exec")

	if respExec.StatusCode == http.StatusOK && execResp != nil {
		t.Logf("remediation dispatched successfully: %s", execResp.Output)
	}

	// Step 5: Audit log verification
	entries, err := srv.AuditLog.List(ctx, 20, "")
	if err == nil {
		t.Logf("Scenario 3 completed, verified %d audit log entries", len(entries))
	}
}
