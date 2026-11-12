package agents

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/tools"
)

type multiTurnMockGenerator struct {
	mu        sync.Mutex
	callCount int
	turns     []providers.Message
}

func (m *multiTurnMockGenerator) Generate(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (routing.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.callCount
	m.callCount++

	if idx >= len(m.turns) {
		return routing.Result{
			Response: &providers.ChatResponse{
				Message: providers.TextMessage(providers.RoleAssistant, "default response"),
			},
		}, nil
	}

	return routing.Result{
		ProviderID: "mock-prov",
		ModelID:    "mock-model",
		Attempts:   1,
		Response: &providers.ChatResponse{
			Message: m.turns[idx],
		},
	}, nil
}

type statusTracker struct {
	mu          sync.Mutex
	transitions []ExecutionStatus
}

func (s *statusTracker) UpdateTaskStatus(ctx context.Context, taskID string, status ExecutionStatus, errMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transitions = append(s.transitions, status)
	return nil
}

func (s *statusTracker) History() []ExecutionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ExecutionStatus, len(s.transitions))
	copy(out, s.transitions)
	return out
}

func TestAgentRuntimeMultiTurnToolExecution(t *testing.T) {
	// Setup mock generator with 2 turns:
	// Turn 1: tool call
	// Turn 2: final answer
	gen := &multiTurnMockGenerator{
		turns: []providers.Message{
			{
				Role: providers.RoleAssistant,
				ToolCalls: []providers.ToolCall{
					{
						ID:        "tc-1",
						Name:      "calc",
						Arguments: `{"expr":"2+2"}`,
					},
				},
			},
			{
				Role:    providers.RoleAssistant,
				Content: []providers.ContentPart{providers.TextPart("The result of 2+2 is 4.")},
			},
		},
	}

	reg := tools.NewToolRegistry()
	_ = reg.RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "calc",
		Name:                 "calc",
		Category:             tools.CategoryCustom,
		RiskLevel:            tools.RiskLow,
		RequiredCapabilities: []string{"tools:calc"},
		RequiredPermissions:  []string{"read"},
		ParametersSchema:     json.RawMessage(`{"type":"object"}`),
		Enabled:              true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		return &tools.ExecutionResult{
			ToolName: "calc",
			Output:   "4",
		}, nil
	}))

	exec := tools.NewUnifiedToolExecutor(reg, nil)
	tracker := &statusTracker{}

	rt := NewAgentRuntime(gen, NewContextManager())
	rt.SetToolExecutor(exec)
	rt.SetToolRegistry(reg)
	rt.SetTaskStatusUpdater(tracker)

	agent := AgentDefinition{
		ID:           "math-agent",
		Name:         "Math Agent",
		Model:        "test-model",
		Capabilities: []string{"tools:calc"},
		Permissions:  []string{"read"},
		MaxRequests:  3,
		Enabled:      true,
	}

	task := TaskRecord{
		ID:        "task-123",
		RunID:     "run-456",
		AgentID:   agent.ID,
		InputData: "What is 2+2?",
	}

	res, err := rt.ExecuteTask(context.Background(), agent, task, ContextInput{})
	if err != nil {
		t.Fatalf("expected successful execution, got: %v", err)
	}

	if res.Status != StatusCompleted {
		t.Fatalf("expected StatusCompleted, got %v", res.Status)
	}
	if res.OutputText != "The result of 2+2 is 4." {
		t.Fatalf("unexpected final output: %s", res.OutputText)
	}

	// Verify status transitions: running -> waiting -> running
	history := tracker.History()
	if len(history) < 2 {
		t.Fatalf("expected at least 2 status transitions, got %v", history)
	}
	if history[0] != StatusWaiting || history[1] != StatusRunning {
		t.Fatalf("expected transitions [waiting, running], got %v", history)
	}
}

func TestDynamicAgentPrivilegeEnforcement(t *testing.T) {
	store := NewStore(nil) // in-memory registry tests don't require SQL for dynamic agents
	reg := NewRegistry(store)

	ctx := context.Background()
	_ = ctx

	// 1. Unparented dynamic agent declaring permissions must be rejected
	err := reg.RegisterDynamicAgent("run-1", "", AgentDefinition{
		ID:          "unparented-1",
		Name:        "Unparented Agent",
		Permissions: []string{"read"},
	}, 10)
	if err == nil {
		t.Fatal("expected error when unparented dynamic agent declares permissions")
	}

	// 2. Unparented dynamic agent declaring capabilities must be rejected
	err = reg.RegisterDynamicAgent("run-1", "", AgentDefinition{
		ID:           "unparented-2",
		Name:         "Unparented Agent",
		Capabilities: []string{"tools:fs:read"},
	}, 10)
	if err == nil {
		t.Fatal("expected error when unparented dynamic agent declares capabilities")
	}

	// 3. Child dynamic agent with non-existent parent must be rejected
	err = reg.RegisterDynamicAgent("run-1", "non-existent-parent", AgentDefinition{
		ID:   "child-1",
		Name: "Child Agent",
	}, 10)
	if err == nil {
		t.Fatal("expected error when parent agent does not exist")
	}

	// 4. Register a valid dynamic parent
	err = reg.RegisterDynamicAgent("run-1", "", AgentDefinition{
		ID:   "parent-agent",
		Name: "Parent Agent",
	}, 10)
	if err != nil {
		t.Fatalf("failed to register unparented agent with zero permissions: %v", err)
	}

	// Dynamic parent with permissions inside registry
	reg.mu.Lock()
	reg.dynamicAgents["run-1"]["parent-with-caps"] = AgentDefinition{
		ID:           "parent-with-caps",
		Name:         "Parent With Caps",
		Capabilities: []string{"tools:fs:read", "tools:calc"},
		Permissions:  []string{"read"},
		Enabled:      true,
	}
	reg.mu.Unlock()

	// Child requesting allowed subset
	err = reg.RegisterDynamicAgent("run-1", "parent-with-caps", AgentDefinition{
		ID:           "valid-child",
		Name:         "Valid Child",
		Capabilities: []string{"tools:calc"},
		Permissions:  []string{"read"},
	}, 10)
	if err != nil {
		t.Fatalf("expected child with subset to succeed, got: %v", err)
	}

	// Child attempting privilege escalation (extra capability)
	err = reg.RegisterDynamicAgent("run-1", "parent-with-caps", AgentDefinition{
		ID:           "escalating-child",
		Name:         "Escalating Child",
		Capabilities: []string{"tools:fs:read", "tools:terminal:exec"},
		Permissions:  []string{"read"},
	}, 10)
	if err == nil {
		t.Fatal("expected error for capability escalation")
	}
}
