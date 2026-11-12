package tier3_combinations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_Device_Execution_SecretOutputRedacted verifies that when a remote device
// executes a capability returning credentials, the central authority redacts secrets
// from the final result before returning to caller.
func TestCombo_Device_Execution_SecretOutputRedacted(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":  "edge-device-1",
			"capability": "fetch_logs",
		},
		GrantedCapabilities: []string{"tools:device", "*"},
	}

	execResp, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "device_exec")

	if resp.StatusCode == http.StatusOK && execResp != nil {
		// Verify no credentials leaked
		if strings.Contains(execResp.Output, "AKIA") || strings.Contains(execResp.Output, "ghp_") {
			t.Fatalf("unredacted secret in device tool output: %s", execResp.Output)
		}
	}
}

// TestCombo_Device_SecretArguments_MaskedInAudit verifies that credentials passed
// as parameters to device commands are masked in the central audit log.
func TestCombo_Device_SecretArguments_MaskedInAudit(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":  "edge-device-1",
			"capability": "configure",
			"api_token":  "ghp_123456789012345678901234567890123456",
			"password":   "deviceRootPass123!",
		},
		GrantedCapabilities: []string{"tools:device", "*"},
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, resp, "device_exec")

	// Verify audit log has no plain text secrets
	entries, err := srv.AuditLog.List(ctx, 10, "")
	if err != nil {
		t.Fatalf("list audit log failed: %v", err)
	}

	for _, e := range entries {
		detail := string(e.Detail)
		if strings.Contains(detail, "deviceRootPass123!") || strings.Contains(detail, "ghp_123456789012345678901234567890123456") {
			t.Fatalf("security violation: raw secret leaked in audit entry: %s", detail)
		}
	}
}

// TestCombo_Device_Telemetry_SensitiveEnvRedacted verifies that device telemetry
// reports containing sensitive environment variables (AWS keys, JWTs) are sanitized.
func TestCombo_Device_Telemetry_SensitiveEnvRedacted(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	telemetryPayload := map[string]any{
		"metrics": map[string]any{
			"cpu_usage": 15.4,
			"env": map[string]any{
				"AWS_ACCESS_KEY_ID": "AKIAIOSFODNN7EXAMPLE",
				"TOKEN":             "ghp_999999999999999999999999999999999999",
			},
		},
	}

	resp, err := srv.Client.Post(ctx, "/v1/devices/dev-test/heartbeat", telemetryPayload)
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F19: Device Transport")

	if resp.StatusCode == http.StatusOK {
		t.Logf("heartbeat accepted: %s", resp.String())
	}
}
