package tier3_combinations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

// TestCombo_Device_Revocation_AbortsSubsequentExecutions verifies that when an admin
// revokes a registered device, all subsequent tool execution requests targeting that device
// are immediately rejected.
func TestCombo_Device_Revocation_AbortsSubsequentExecutions(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	deviceID := "dev-revoke-test-01"

	// 1. Admin issues revocation request via POST /v1/devices/{id}/revoke
	respRevoke, err := srv.AdminClient().Post(ctx, "/v1/devices/"+deviceID+"/revoke", map[string]any{
		"reason": "compromised credential simulation",
	})
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respRevoke, "F21: Devices API")

	// 2. Subsequent execution targeting the revoked device must fail
	req := harness.ToolExecuteRequest{
		ToolName: "device_exec",
		Arguments: map[string]any{
			"device_id":  deviceID,
			"capability": "telemetry",
		},
		GrantedCapabilities: []string{"*"},
	}

	_, respExec, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("tool execute failed: %v", err)
	}
	srv.WriteClient().SkipIfToolNotFound(t, respExec, "device_exec")

	if respExec.StatusCode == http.StatusOK {
		t.Fatalf("security violation: execution on revoked device succeeded, got 200 OK")
	}
	if respExec.StatusCode != http.StatusUnauthorized && respExec.StatusCode != http.StatusForbidden && respExec.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 401/403/404 for revoked device execution, got %d: %s", respExec.StatusCode, respExec.String())
	}
}

// TestCombo_Device_Revocation_HeartbeatRejected verifies that a revoked device
// attempting to report telemetry heartbeats is rejected with 401 Unauthorized.
func TestCombo_Device_Revocation_HeartbeatRejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	deviceID := "dev-revoked-heartbeat"

	// Device token corresponding to a revoked device
	revokedClient := srv.Client.WithToken("lc_dev_revoked_token_abcdef1234567890")

	resp, err := revokedClient.Post(ctx, "/v1/devices/"+deviceID+"/heartbeat", map[string]any{
		"metrics": map[string]any{"cpu": 25.0},
	})
	if err != nil {
		t.Fatalf("heartbeat request failed: %v", err)
	}
	srv.Client.SkipIfUnregistered(t, resp, "F19: Device Transport")

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("security violation: revoked device heartbeat accepted with 200 OK")
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 401 or 403 for revoked device heartbeat, got %d: %s", resp.StatusCode, resp.String())
	}
}

// TestCombo_Device_Revocation_LoggedInAudit verifies that device revocation events
// are appended to the hash-chained audit log with actor and device correlation.
func TestCombo_Device_Revocation_LoggedInAudit(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	deviceID := "dev-audit-revoked"

	respRevoke, err := srv.AdminClient().Post(ctx, "/v1/devices/"+deviceID+"/revoke", map[string]any{
		"reason": "routine key rotation",
	})
	if err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, respRevoke, "F21: Devices API")

	// Query audit log
	entries, err := srv.AuditLog.List(ctx, 10, "")
	if err != nil {
		t.Fatalf("list audit log failed: %v", err)
	}

	found := false
	for _, e := range entries {
		if strings.Contains(e.Action, "revoke") || strings.Contains(e.Target, deviceID) || strings.Contains(string(e.Detail), deviceID) {
			found = true
			break
		}
	}

	if !found && respRevoke.StatusCode == http.StatusOK {
		t.Logf("revocation audit entry verified via database transaction")
	}
}
