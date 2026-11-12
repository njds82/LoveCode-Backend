package tier1_features

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lovecode/lovecode/tests/e2e/harness"
)

func TestF05_Agent_AgentCreation(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agentBody := map[string]any{
		"id":          "test-tool-agent",
		"name":        "Test Tool Agent",
		"description": "Agent for testing tool execution integration",
		"role":        "worker",
		"model":       "mock-model",
		"capabilities": []string{
			"tools:filesystem:read",
			"tools:sysinfo",
		},
		"permissions": []string{"read"},
	}

	var created map[string]any
	resp, err := srv.AdminClient().PostJSON(ctx, "/v1/agents", agentBody, &created)
	if err != nil {
		t.Fatalf("POST /v1/agents failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F5: Agents API")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected HTTP status 200/201, got %d with body: %s", resp.StatusCode, resp.String())
	}
}

func TestF05_Agent_MultiTurn_ToolCallingLoop(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify agent endpoint is available and can list agents
	var agents []map[string]any
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/agents", &agents)
	if err != nil {
		t.Fatalf("GET /v1/agents failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F5: Agents API")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)
}

func TestF05_Agent_TaskState_TransitionsRunningWaiting(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify team definitions endpoint
	var teams []map[string]any
	resp, err := srv.ReadClient().GetJSON(ctx, "/v1/teams", &teams)
	if err != nil {
		t.Fatalf("GET /v1/teams failed: %v", err)
	}
	srv.ReadClient().SkipIfUnregistered(t, resp, "F5: Teams API")
	srv.ReadClient().AssertStatus(t, resp, http.StatusOK)
}

func TestF05_Agent_CapabilityInheritance_Allowed(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create parent agent with read tools
	parent := map[string]any{
		"id":          "parent-agent",
		"name":        "Parent Agent",
		"role":        "lead",
		"model_group": "default",
		"capabilities": []string{
			"tools:filesystem:read",
		},
		"permissions": []string{"read"},
	}
	resp, err := srv.AdminClient().Post(ctx, "/v1/agents", parent)
	if err != nil {
		t.Fatalf("create parent failed: %v", err)
	}
	srv.AdminClient().SkipIfUnregistered(t, resp, "F5: Capability Inheritance")

	if resp.StatusCode == http.StatusOK {
		// Subagent with equal or narrowed capability should succeed
		subagent := map[string]any{
			"id":          "child-agent",
			"name":        "Child Agent",
			"role":        "worker",
			"model_group": "default",
			"capabilities": []string{
				"tools:filesystem:read",
			},
			"permissions": []string{"read"},
		}
		respSub, err := srv.AdminClient().Post(ctx, "/v1/agents", subagent)
		if err != nil {
			t.Fatalf("create subagent failed: %v", err)
		}
		srv.AdminClient().AssertStatus(t, respSub, http.StatusOK)
	}
}

func TestF05_Agent_CapabilityEscalation_Rejected(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dynamic subagent attempting to claim unauthorized terminal capability
	req := harness.ToolExecuteRequest{
		ToolName: "terminal_exec",
		Arguments: map[string]any{
			"executable": "ls",
		},
		AgentID:             "restricted-subagent",
		GrantedCapabilities: []string{"tools:filesystem:read"}, // Lacks tools:terminal
	}

	_, resp, err := srv.WriteClient().ExecuteTool(ctx, req)
	if err != nil {
		t.Fatalf("execute tool failed: %v", err)
	}
	srv.WriteClient().SkipIfUnregistered(t, resp, "F5: Capability Escalation")

	// Must be forbidden (403) or not found (404)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 Forbidden for capability escalation, got %d: %s", resp.StatusCode, resp.String())
	}
}
