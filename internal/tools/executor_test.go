package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/audit"
)

type mockAuditRecorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (m *mockAuditRecorder) Append(ctx context.Context, e audit.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

func (m *mockAuditRecorder) Events() []audit.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]audit.Event, len(m.events))
	copy(out, m.events)
	return out
}

func TestUnifiedToolExecutor(t *testing.T) {
	reg := NewToolRegistry()
	mockAudit := &mockAuditRecorder{}
	exec := NewUnifiedToolExecutor(reg, mockAudit)

	// Register a secured tool
	secToolDef := ToolDefinition{
		ID:                   "fs_read",
		Name:                 "fs_read",
		Description:          "Read file",
		Category:             CategoryFilesystem,
		RiskLevel:            RiskMedium,
		RequiredCapabilities: []string{"tools:fs:read"},
		RequiredPermissions:  []string{"read"},
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"required": ["path"],
			"properties": {
				"path": { "type": "string" }
			}
		}`),
		Enabled: true,
	}

	_ = reg.RegisterTool(WrapHandler(secToolDef, func(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &p)
		if p.Path == "/slow" {
			select {
			case <-time.After(500 * time.Millisecond):
				return &ExecutionResult{Output: "done"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if p.Path == "/leak" {
			return &ExecutionResult{
				Output: "credentials: Bearer mySecretToken12345678 and key sk-abcdef1234567890abcdef",
				Data: map[string]any{
					"api_key": "raw_secret_value",
					"file":    p.Path,
				},
			}, nil
		}
		if p.Path == "/large" {
			return &ExecutionResult{
				Output: strings.Repeat("A", 1000),
			}, nil
		}
		if p.Path == "/boundary_leak" {
			// Secret "AKIAIOSFODNN7EXAMPLE" spans across byte offset 100 (starts at 95, ends at 115)
			return &ExecutionResult{
				Output: strings.Repeat("A", 94) + " " + "AKIAIOSFODNN7EXAMPLE",
			}, nil
		}
		return &ExecutionResult{
			Output: "contents of " + p.Path,
		}, nil
	}))

	t.Run("Capability Denied (No Capabilities)", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: nil,
			GrantedPermissions:  []string{"read"},
		}
		_, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/tmp/a"}`))
		if !errors.Is(err, ErrCapabilityDenied) {
			t.Fatalf("expected ErrCapabilityDenied, got %v", err)
		}
	})

	t.Run("Capability Denied (Wrong Capability)", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:net:http"},
			GrantedPermissions:  []string{"read"},
		}
		_, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/tmp/a"}`))
		if !errors.Is(err, ErrCapabilityDenied) {
			t.Fatalf("expected ErrCapabilityDenied, got %v", err)
		}
	})

	t.Run("Capability Allowed (Prefix Wildcard)", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:*"},
			GrantedPermissions:  []string{"read"},
		}
		res, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/tmp/a"}`))
		if err != nil {
			t.Fatalf("expected success with wildcard capability, got %v", err)
		}
		if res.Output != "contents of /tmp/a" {
			t.Fatalf("unexpected output: %s", res.Output)
		}
	})

	t.Run("Capability Allowed (Universal Wildcard)", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "admin-agent",
			GrantedCapabilities: []string{"*"},
			GrantedPermissions:  []string{"*"},
		}
		res, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/tmp/b"}`))
		if err != nil {
			t.Fatalf("expected success with universal wildcard, got %v", err)
		}
		if res.Output != "contents of /tmp/b" {
			t.Fatalf("unexpected output: %s", res.Output)
		}
	})

	t.Run("Permission Denied", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:read"},
			GrantedPermissions:  []string{"write"}, // missing "read"
		}
		_, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/tmp/a"}`))
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("expected ErrPermissionDenied, got %v", err)
		}
	})

	t.Run("Schema Validation Error", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:read"},
			GrantedPermissions:  []string{"read"},
		}
		// Missing required property "path"
		_, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{}`))
		if !errors.Is(err, ErrSchemaValidation) {
			t.Fatalf("expected ErrSchemaValidation, got %v", err)
		}
	})

	t.Run("Timeout Propagation", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:read"},
			GrantedPermissions:  []string{"read"},
			Timeout:             50 * time.Millisecond,
		}
		_, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/slow"}`))
		if !errors.Is(err, ErrExecutionTimeout) {
			t.Fatalf("expected ErrExecutionTimeout, got %v", err)
		}
	})

	t.Run("Output Truncation", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:read"},
			GrantedPermissions:  []string{"read"},
			MaxOutputBytes:      100,
		}
		res, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/large"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Truncated {
			t.Fatal("expected Truncated=true")
		}
		if len(res.Output) != 100 {
			t.Fatalf("expected output length 100, got %d", len(res.Output))
		}
	})

	t.Run("Secret Spanning Truncation Boundary Redacted Before Truncation", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:read"},
			GrantedPermissions:  []string{"read"},
			MaxOutputBytes:      100,
		}
		res, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/boundary_leak"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Truncated {
			t.Fatal("expected Truncated=true")
		}
		if len(res.Output) != 100 {
			t.Fatalf("expected output length 100, got %d", len(res.Output))
		}
		// The 20-character AWS key "AKIAIOSFODNN7EXAMPLE" must NOT leak as a truncated prefix ("AKIAI")
		if strings.Contains(res.Output, "AKIA") {
			t.Fatalf("secret prefix leaked across truncation boundary: %s", res.Output)
		}
		// Verify redaction was applied before truncation: the start of [REDACTED] must be present
		if !strings.Contains(res.Output, "[REDA") {
			t.Fatalf("expected redacted prefix at boundary, got %s", res.Output)
		}
	})

	t.Run("Secret Redaction", func(t *testing.T) {
		execCtx := ExecutionContext{
			Actor:               "agent-1",
			GrantedCapabilities: []string{"tools:fs:read"},
			GrantedPermissions:  []string{"read"},
		}
		res, err := exec.Execute(context.Background(), execCtx, "fs_read", json.RawMessage(`{"path":"/leak"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(res.Output, "mySecretToken") || strings.Contains(res.Output, "sk-abcdef") {
			t.Fatalf("secrets leaked in output string: %s", res.Output)
		}
		if !strings.Contains(res.Output, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] in output string, got: %s", res.Output)
		}

		dataMap := res.Data.(map[string]any)
		if dataMap["api_key"] != "[REDACTED]" {
			t.Fatalf("expected api_key redacted in data map, got: %v", dataMap["api_key"])
		}
		if dataMap["file"] != "/leak" {
			t.Fatalf("expected safe field preserved in data map, got: %v", dataMap["file"])
		}
	})

	t.Run("Audit Trail Logged", func(t *testing.T) {
		events := mockAudit.Events()
		if len(events) == 0 {
			t.Fatal("expected audit events to be recorded")
		}

		foundExecuted := false
		foundDenied := false
		for _, e := range events {
			if e.Action == "tool.executed" {
				foundExecuted = true
				if e.Target != "fs_read" {
					t.Fatalf("unexpected target in audit: %s", e.Target)
				}
			}
			if e.Action == "tool.denied" {
				foundDenied = true
			}
		}
		if !foundExecuted {
			t.Fatal("missing tool.executed event")
		}
		if !foundDenied {
			t.Fatal("missing tool.denied event")
		}
	})
}
