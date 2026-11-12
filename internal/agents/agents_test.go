package agents

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/storage"
)

type fakeGenerator struct {
	mu       sync.Mutex
	calls    int
	reply    string
	err      error
	delay    time.Duration
	lastReq  providers.ChatRequest
	attempts int
}

func (f *fakeGenerator) Generate(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (routing.Result, error) {
	f.mu.Lock()
	f.calls++
	f.lastReq = req
	reply := f.reply
	if reply == "" {
		reply = "mock response"
	}
	err := f.err
	delay := f.delay
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return routing.Result{}, ctx.Err()
		case <-time.After(delay):
		}
	}

	if err != nil {
		return routing.Result{}, err
	}

	pTokens := 10
	cTokens := 20
	tTokens := 30
	return routing.Result{
		Response: &providers.ChatResponse{
			Message: providers.TextMessage(providers.RoleAssistant, reply),
			Usage: &providers.Usage{
				PromptTokens:     &pTokens,
				CompletionTokens: &cTokens,
				TotalTokens:      &tTokens,
			},
		},
		ProviderID: "mock-prov",
		ModelID:    "mock-mod",
		Attempts:   1,
	}, nil
}

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_agents.db")
	db, err := storage.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("storage.Open failed: %v", err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("db.Migrate failed: %v", err)
	}
	store := NewStore(db.DB)
	return store, func() { _ = db.Close() }
}

func TestAgentRegistryCRUDAndPersistence(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	reg := NewRegistry(store)
	ctx := context.Background()

	// 1. Register agent
	agent := AgentDefinition{
		ID:            "agent-1",
		Name:          "Researcher",
		Role:          "Information Gathering",
		SystemPrompt:  "You are a careful researcher.",
		Model:         "mock:chat",
		MaxTokens:     1024,
		Timeout:       5 * time.Second,
		MaxRequests:   2,
		AutonomyLevel: AutonomyAutonomous,
		ContextPolicy: ContextIsolated,
		Permissions:   []string{"read", "search"},
		Enabled:       true,
	}

	if err := reg.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("RegisterAgent failed: %v", err)
	}

	// 2. Retrieve agent
	got, err := reg.GetAgent("", "agent-1")
	if err != nil {
		t.Fatalf("GetAgent failed: %v", err)
	}
	if got.Name != "Researcher" || got.Role != "Information Gathering" {
		t.Errorf("unexpected agent: %+v", got)
	}

	// 3. Update agent
	agent.Name = "Senior Researcher"
	if err := reg.RegisterAgent(ctx, agent); err != nil {
		t.Fatalf("update agent failed: %v", err)
	}
	got, _ = reg.GetAgent("", "agent-1")
	if got.Name != "Senior Researcher" {
		t.Errorf("expected Senior Researcher, got %s", got.Name)
	}

	// 4. Disable agent
	if err := reg.SetAgentEnabled(ctx, "agent-1", false); err != nil {
		t.Fatalf("SetAgentEnabled failed: %v", err)
	}
	list := reg.ListAgents(false)
	if len(list) != 0 {
		t.Errorf("expected 0 enabled agents, got %d", len(list))
	}
	listAll := reg.ListAgents(true)
	if len(listAll) != 1 {
		t.Errorf("expected 1 total agent, got %d", len(listAll))
	}

	// 5. Test persistence survives restart
	reg2 := NewRegistry(store)
	if err := reg2.LoadFromStore(ctx); err != nil {
		t.Fatalf("LoadFromStore failed: %v", err)
	}
	got2, err := reg2.GetAgent("", "agent-1")
	if err != nil {
		t.Fatalf("GetAgent from reloaded registry failed: %v", err)
	}
	if got2.Name != "Senior Researcher" {
		t.Errorf("persisted name mismatch: got %s", got2.Name)
	}

	// 6. Delete agent
	if err := reg2.DeleteAgent(ctx, "agent-1"); err != nil {
		t.Fatalf("DeleteAgent failed: %v", err)
	}
	if _, err := reg2.GetAgent("", "agent-1"); err == nil {
		t.Error("expected error getting deleted agent, got nil")
	}
}

func TestDynamicAgentPrivilegeInheritance(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	reg := NewRegistry(store)
	ctx := context.Background()

	parent := AgentDefinition{
		ID:          "parent-agent",
		Name:        "Parent",
		Permissions: []string{"read", "write"},
		Enabled:     true,
	}
	_ = reg.RegisterAgent(ctx, parent)

	runID := "test-run-1"

	// 1. Dynamic agent with valid subset permissions
	childValid := AgentDefinition{
		ID:          "child-valid",
		Name:        "Child Valid",
		Permissions: []string{"read"},
	}
	if err := reg.RegisterDynamicAgent(runID, "parent-agent", childValid, 4); err != nil {
		t.Fatalf("RegisterDynamicAgent failed for valid child: %v", err)
	}

	// Check retrieval of dynamic agent
	gotChild, err := reg.GetAgent(runID, "child-valid")
	if err != nil {
		t.Fatalf("GetAgent failed for dynamic agent: %v", err)
	}
	if !gotChild.IsDynamic {
		t.Errorf("expected IsDynamic to be true")
	}

	// 2. Dynamic agent attempting privilege escalation
	childEscalated := AgentDefinition{
		ID:          "child-escalated",
		Name:        "Child Escalated",
		Permissions: []string{"admin", "read"},
	}
	if err := reg.RegisterDynamicAgent(runID, "parent-agent", childEscalated, 4); err == nil {
		t.Fatal("expected privilege escalation error, got nil")
	}

	// 3. Exceed max dynamic agents
	for i := 2; i <= 4; i++ {
		_ = reg.RegisterDynamicAgent(runID, "parent-agent", AgentDefinition{
			ID:   filepath.Join("child", string(rune('0'+i))),
			Name: "Child",
		}, 4)
	}
	// 5th should fail
	errExceed := reg.RegisterDynamicAgent(runID, "parent-agent", AgentDefinition{
		ID:   "child-overflow",
		Name: "Child Overflow",
	}, 4)
	if errExceed == nil {
		t.Fatal("expected max dynamic agents limit error, got nil")
	}

	// 4. Cleanup
	reg.CleanupDynamicAgents(runID)
	if _, err := reg.GetAgent(runID, "child-valid"); err == nil {
		t.Error("expected error after cleanup, got nil")
	}
}

func TestContextPoliciesAndLimits(t *testing.T) {
	cm := NewContextManager()

	agent := AgentDefinition{
		ID:           "test-agent",
		Name:         "Worker",
		Role:         "Tester",
		SystemPrompt: "Instructions for worker",
	}

	// 1. Isolated Context
	agent.ContextPolicy = ContextIsolated
	ci := ContextInput{
		Agent:       agent,
		TaskInput:   "Task input only",
		SharedInfo:  "Secret shared info that should be hidden",
		HandoffInfo: "Predecessor secret handoff",
	}
	msgs := cm.BuildMessages(ci)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(msgs))
	}
	userTxt := msgs[1].Text()
	if userTxt != "Task input only" {
		t.Errorf("isolated policy leaked extra context: %s", userTxt)
	}

	// 2. Shared Context
	ci.Agent.ContextPolicy = ContextShared
	msgsShared := cm.BuildMessages(ci)
	if len(msgsShared) < 2 {
		t.Errorf("shared policy missing messages: %v", msgsShared)
	}

	// 3. Handoff Context
	ci.Agent.ContextPolicy = ContextHandoff
	msgsHandoff := cm.BuildMessages(ci)
	handoffFound := false
	for _, m := range msgsHandoff {
		if m.Role == providers.RoleUser && strings.Contains(m.Text(), "Predecessor Output Handoff") {
			handoffFound = true
		}
	}
	if !handoffFound {
		t.Errorf("handoff info not found in handoff policy: %+v", msgsHandoff)
	}

	// 4. Summarized Context
	ci.Agent.ContextPolicy = ContextSummarized
	ci.Summaries = []string{"Step 1 succeeded", "Step 2 analyzed data"}
	msgsSummary := cm.BuildMessages(ci)
	summaryFound := false
	for _, m := range msgsSummary {
		if m.Role == providers.RoleUser && strings.Contains(m.Text(), "Previous Results Summary") {
			summaryFound = true
		}
	}
	if !summaryFound {
		t.Errorf("summary info not found in summarized policy: %+v", msgsSummary)
	}
}

func TestAgentRuntimeSingleExecution(t *testing.T) {
	gen := &fakeGenerator{reply: "agent task completed successfully"}
	cm := NewContextManager()
	runtime := NewAgentRuntime(gen, cm)

	agent := AgentDefinition{
		ID:          "exec-agent",
		Name:        "Executor",
		Enabled:     true,
		MaxRequests: 1,
	}
	task := TaskRecord{
		ID:        "task-123",
		InputData: "hello agent",
	}

	res, err := runtime.ExecuteTask(context.Background(), agent, task, ContextInput{})
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}
	if res.Status != StatusCompleted {
		t.Errorf("expected status completed, got %s", res.Status)
	}
	if res.OutputText != "agent task completed successfully" {
		t.Errorf("unexpected output: %s", res.OutputText)
	}
	if res.TotalTokens != 30 {
		t.Errorf("expected 30 tokens, got %d", res.TotalTokens)
	}
}

func TestTeamSequentialAndParallelExecution(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := config.AgentConfig{
		MaxConcurrentRuns:   4,
		MaxConcurrentAgents: 4,
		MaxParallelPerRun:   4,
	}
	gen := &fakeGenerator{reply: "sequential step done"}
	service := NewService(cfg, store, gen, nil, nil)
	ctx := context.Background()
	_ = service.Start(ctx)
	defer func() { _ = service.Stop(ctx) }()

	// Register 2 agents
	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{
		ID:      "member-1",
		Name:    "Member One",
		Enabled: true,
	})
	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{
		ID:      "member-2",
		Name:    "Member Two",
		Enabled: true,
	})

	// 1. Sequential Team
	seqTeam := TeamDefinition{
		ID:       "seq-team",
		Name:     "Sequential Team",
		Strategy: StrategySequential,
		Members: []TeamMember{
			{AgentID: "member-1", Required: true},
			{AgentID: "member-2", Required: true},
		},
		AggregationPolicy: AggregationCollectAll,
		Enabled:           true,
	}
	_ = service.Registry().RegisterTeam(ctx, seqTeam)

	run, err := service.StartRun(ctx, RunSpec{
		RunType: "team",
		TeamID:  "seq-team",
		Input:   "sequential input",
	}, "test-user")
	if err != nil {
		t.Fatalf("Sequential StartRun failed: %v", err)
	}
	if run.Status != StatusCompleted {
		t.Errorf("expected run completed, got %s (err: %s)", run.Status, run.ErrorMessage)
	}

	tasks, err := service.Store().ListTasksByRun(ctx, run.ID)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("expected 2 tasks in sequential run, got %d (err: %v)", len(tasks), err)
	}

	// 2. Parallel Team
	parTeam := TeamDefinition{
		ID:          "par-team",
		Name:        "Parallel Team",
		Strategy:    StrategyParallel,
		MaxParallel: 2,
		Members: []TeamMember{
			{AgentID: "member-1"},
			{AgentID: "member-2"},
		},
		AggregationPolicy: AggregationOrdered,
		Enabled:           true,
	}
	_ = service.Registry().RegisterTeam(ctx, parTeam)

	parRun, err := service.StartRun(ctx, RunSpec{
		RunType: "team",
		TeamID:  "par-team",
		Input:   "parallel input",
	}, "test-user")
	if err != nil {
		t.Fatalf("Parallel StartRun failed: %v", err)
	}
	if parRun.Status != StatusCompleted {
		t.Errorf("expected parallel run completed, got %s", parRun.Status)
	}
}

func TestDependencyDAGExecutionAndCycleDetection(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := config.AgentConfig{MaxConcurrentRuns: 4, MaxConcurrentAgents: 4}
	gen := &fakeGenerator{reply: "dag step finished"}
	service := NewService(cfg, store, gen, nil, nil)
	ctx := context.Background()
	_ = service.Start(ctx)
	defer func() { _ = service.Stop(ctx) }()

	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{ID: "dag-a", Name: "A", Enabled: true})
	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{ID: "dag-b", Name: "B", Enabled: true})
	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{ID: "dag-c", Name: "C", Enabled: true})

	// 1. Cycle detection
	cycleTeam := TeamDefinition{
		ID:       "cycle-team",
		Name:     "Cycle Team",
		Strategy: StrategyDependency,
		Members: []TeamMember{
			{AgentID: "dag-a", Dependencies: []string{"dag-c"}},
			{AgentID: "dag-b", Dependencies: []string{"dag-a"}},
			{AgentID: "dag-c", Dependencies: []string{"dag-b"}},
		},
		Enabled: true,
	}
	_ = service.Registry().RegisterTeam(ctx, cycleTeam)

	_, errCycle := service.StartRun(ctx, RunSpec{RunType: "team", TeamID: "cycle-team", Input: "start"}, "user")
	if errCycle == nil {
		t.Fatal("expected cycle detection error, got nil")
	}

	// 2. Valid DAG: A -> B -> C
	validDagTeam := TeamDefinition{
		ID:       "valid-dag-team",
		Name:     "Valid DAG Team",
		Strategy: StrategyDependency,
		Members: []TeamMember{
			{AgentID: "dag-a"},
			{AgentID: "dag-b", Dependencies: []string{"dag-a"}},
			{AgentID: "dag-c", Dependencies: []string{"dag-b"}},
		},
		AggregationPolicy: AggregationOrdered,
		Enabled:           true,
	}
	_ = service.Registry().RegisterTeam(ctx, validDagTeam)

	dagRun, err := service.StartRun(ctx, RunSpec{RunType: "team", TeamID: "valid-dag-team", Input: "dag input"}, "user")
	if err != nil {
		t.Fatalf("valid DAG StartRun failed: %v", err)
	}
	if dagRun.Status != StatusCompleted {
		t.Errorf("expected dag run completed, got %s (err: %s)", dagRun.Status, dagRun.ErrorMessage)
	}
}

func TestRestartInterruptedRecovery(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Insert an interrupted running run and task
	now := time.Now().UTC()
	_ = store.CreateRun(ctx, RunRecord{
		ID:        "interrupted-run",
		RunType:   "agent",
		AgentID:   "dummy",
		Status:    StatusRunning,
		CreatedAt: now,
		StartedAt: &now,
	})
	_ = store.CreateTask(ctx, TaskRecord{
		ID:        "interrupted-task",
		RunID:     "interrupted-run",
		AgentID:   "dummy",
		Status:    StatusRunning,
		CreatedAt: now,
		StartedAt: &now,
	})

	// Run recovery
	count, err := store.RecoverInterrupted(ctx)
	if err != nil {
		t.Fatalf("RecoverInterrupted failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 recovered run, got %d", count)
	}

	run, _ := store.GetRun(ctx, "interrupted-run")
	if run.Status != StatusFailed || run.ErrorMessage != "interrupted by system restart" {
		t.Errorf("unexpected run state after recovery: status=%s, err=%s", run.Status, run.ErrorMessage)
	}

	task, _ := store.GetTask(ctx, "interrupted-task")
	if task.Status != StatusFailed || task.ErrorMessage != "interrupted by system restart" {
		t.Errorf("unexpected task state after recovery: status=%s, err=%s", task.Status, task.ErrorMessage)
	}
}
