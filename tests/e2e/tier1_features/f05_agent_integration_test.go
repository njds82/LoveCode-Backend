package tier1_features

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/agents"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/tools"
	"github.com/lovecode/lovecode/tests/e2e/harness"
)

type multiTurnMockGenerator struct {
	mu    sync.Mutex
	index int
	turns []providers.Message
}

func (m *multiTurnMockGenerator) Generate(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (routing.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.index >= len(m.turns) {
		return routing.Result{}, errors.New("no more mock turns configured")
	}
	msg := m.turns[m.index]
	m.index++
	tok := 10
	tot := 20
	resp := &providers.ChatResponse{
		Message: msg,
		Usage: &providers.Usage{
			PromptTokens:     &tok,
			CompletionTokens: &tok,
			TotalTokens:      &tot,
		},
	}
	return routing.Result{
		Response:   resp,
		ProviderID: "mock-provider",
		ModelID:    "mock-model",
		Attempts:   1,
	}, nil
}

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

	// Register tool in server's registry
	_ = srv.Tools.Registry().RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "echo_tool",
		Name:                 "echo_tool",
		Category:             tools.CategoryCustom,
		RiskLevel:            tools.RiskLow,
		RequiredCapabilities: []string{"tools:echo"},
		ParametersSchema:     json.RawMessage(`{"type":"object"}`),
		Enabled:              true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		return &tools.ExecutionResult{
			ToolName: "echo_tool",
			Output:   "echoed output",
		}, nil
	}))

	// Setup mock 2-turn generator:
	// Turn 1: tool call
	// Turn 2: final answer consuming tool output
	gen := &multiTurnMockGenerator{
		turns: []providers.Message{
			{
				Role: providers.RoleAssistant,
				ToolCalls: []providers.ToolCall{
					{
						ID:        "tc-1",
						Name:      "echo_tool",
						Arguments: `{"text":"test"}`,
					},
				},
			},
			{
				Role:    providers.RoleAssistant,
				Content: []providers.ContentPart{providers.TextPart("Processed echoed output successfully.")},
			},
		},
	}

	rt := agents.NewAgentRuntime(gen, agents.NewContextManager())
	rt.SetToolExecutor(srv.Tools.Executor())
	rt.SetToolRegistry(srv.Tools.Registry())

	agent := agents.AgentDefinition{
		ID:           "test-worker",
		Name:         "Test Worker",
		Model:        "test-model",
		Capabilities: []string{"tools:echo"},
		MaxRequests:  3,
		Enabled:      true,
	}
	task := agents.TaskRecord{
		ID:        "task-f05-1",
		RunID:     "run-f05-1",
		AgentID:   agent.ID,
		InputData: "Execute echo_tool and summarize result",
	}

	res, err := rt.ExecuteTask(ctx, agent, task, agents.ContextInput{})
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}
	if res.Status != agents.StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %v", res.Status)
	}
	if res.OutputText != "Processed echoed output successfully." {
		t.Fatalf("unexpected final output: %s", res.OutputText)
	}
	if gen.index != 2 {
		t.Fatalf("expected 2 turns executed in loop, got %d", gen.index)
	}
}

func TestF05_Agent_TaskState_TransitionsRunningWaiting(t *testing.T) {
	srv := harness.NewServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = srv.Tools.Registry().RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "state_probe_tool",
		Name:                 "state_probe_tool",
		Category:             tools.CategoryCustom,
		RiskLevel:            tools.RiskLow,
		RequiredCapabilities: []string{"tools:probe"},
		ParametersSchema:     json.RawMessage(`{"type":"object"}`),
		Enabled:              true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		return &tools.ExecutionResult{
			ToolName: "state_probe_tool",
			Output:   "probe_ok",
		}, nil
	}))

	gen := &multiTurnMockGenerator{
		turns: []providers.Message{
			{
				Role: providers.RoleAssistant,
				ToolCalls: []providers.ToolCall{
					{
						ID:        "tc-state-1",
						Name:      "state_probe_tool",
						Arguments: `{}`,
					},
				},
			},
			{
				Role:    providers.RoleAssistant,
				Content: []providers.ContentPart{providers.TextPart("State test finished.")},
			},
		},
	}

	var transitions []agents.ExecutionStatus
	var mu sync.Mutex
	tracker := agents.TaskStatusUpdaterFunc(func(ctx context.Context, taskID string, status agents.ExecutionStatus, errMessage string) error {
		mu.Lock()
		transitions = append(transitions, status)
		mu.Unlock()
		return nil
	})

	rt := agents.NewAgentRuntime(gen, agents.NewContextManager())
	rt.SetToolExecutor(srv.Tools.Executor())
	rt.SetToolRegistry(srv.Tools.Registry())
	rt.SetTaskStatusUpdater(tracker)

	agent := agents.AgentDefinition{
		ID:           "state-test-agent",
		Name:         "State Test Agent",
		Model:        "test-model",
		Capabilities: []string{"tools:probe"},
		MaxRequests:  3,
		Enabled:      true,
	}
	task := agents.TaskRecord{
		ID:        "task-state-1",
		RunID:     "run-state-1",
		AgentID:   agent.ID,
		InputData: "Check state transitions",
	}

	res, err := rt.ExecuteTask(ctx, agent, task, agents.ContextInput{})
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}
	if res.Status != agents.StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %v", res.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(transitions) < 2 {
		t.Fatalf("expected at least 2 status transitions (waiting, running), got %v", transitions)
	}
	if transitions[0] != agents.StatusWaiting {
		t.Errorf("expected first transition to StatusWaiting on tool call, got %v", transitions[0])
	}
	if transitions[1] != agents.StatusRunning {
		t.Errorf("expected second transition to StatusRunning after tool result, got %v", transitions[1])
	}
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
