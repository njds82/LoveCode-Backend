package agents

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/config"
)

func TestSchedulerConcurrencyAndQueueLimits(t *testing.T) {
	cfg := config.AgentConfig{
		MaxConcurrentRuns:   2,
		MaxConcurrentAgents: 2,
		QueueLimit:          3,
	}
	sched := NewScheduler(cfg)

	// Fill queue up to limit
	c1, err1 := sched.CheckQueueLimits()
	if err1 != nil {
		t.Fatalf("first queue check failed: %v", err1)
	}
	defer c1()

	c2, err2 := sched.CheckQueueLimits()
	if err2 != nil {
		t.Fatalf("second queue check failed: %v", err2)
	}
	defer c2()

	c3, err3 := sched.CheckQueueLimits()
	if err3 != nil {
		t.Fatalf("third queue check failed: %v", err3)
	}
	defer c3()

	// 4th should be rejected with ErrQueueFull
	_, err4 := sched.CheckQueueLimits()
	if !errors.Is(err4, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err4)
	}

	// Release c3, now next check should succeed
	c3()
	c5, err5 := sched.CheckQueueLimits()
	if err5 != nil {
		t.Fatalf("expected success after release, got %v", err5)
	}
	defer c5()
}

func TestRunAndTaskCancellation(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := config.AgentConfig{MaxConcurrentRuns: 4, MaxConcurrentAgents: 4}
	gen := &fakeGenerator{
		delay: 500 * time.Millisecond,
		reply: "delayed output",
	}
	service := NewService(cfg, store, gen, nil, nil)
	ctx := context.Background()
	_ = service.Start(ctx)
	defer func() { _ = service.Stop(ctx) }()

	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{
		ID:      "slow-agent",
		Name:    "Slow Agent",
		Enabled: true,
	})

	// Start run in background goroutine
	done := make(chan *RunRecord, 1)
	var runID string
	var errCh = make(chan error, 1)

	go func() {
		spec := RunSpec{
			RunType: "agent",
			AgentID: "slow-agent",
			Input:   "work on this slowly",
		}
		run, err := service.StartRun(ctx, spec, "user")
		done <- run
		errCh <- err
	}()

	// Wait briefly for run to start
	time.Sleep(50 * time.Millisecond)

	runs, _ := store.ListRuns(ctx, 10, 0)
	if len(runs) > 0 {
		runID = runs[0].ID
		// Cancel the run
		if err := service.CancelRun(ctx, runID, "admin"); err != nil {
			t.Fatalf("CancelRun failed: %v", err)
		}
	}

	run := <-done
	<-errCh

	if run == nil {
		t.Fatal("expected run record, got nil")
	}
	if run.Status != StatusCancelled {
		t.Errorf("expected StatusCancelled, got %s", run.Status)
	}
}

func TestAggregatorSynthesizerAndConflicts(t *testing.T) {
	gen := &fakeGenerator{reply: "Synthesized unified consensus"}
	rt := NewAgentRuntime(gen, nil)
	agg := NewAggregator(rt)

	synthAgent := &AgentDefinition{
		ID:      "synthesizer",
		Name:    "Synthesizer",
		Enabled: true,
	}

	results := []*TaskExecutionResult{
		{
			TaskID:     "t1",
			AgentID:    "agent-pro",
			Status:     StatusCompleted,
			OutputText: "Yes, we approve the proposal.",
		},
		{
			TaskID:     "t2",
			AgentID:    "agent-con",
			Status:     StatusCompleted,
			OutputText: "No, we reject the proposal due to risks.",
		},
	}

	ctx := context.Background()
	out, err := agg.Aggregate(ctx, AggregationSynthesizer, "agent-pro", synthAgent, results)
	if err != nil {
		t.Fatalf("Aggregate failed: %v", err)
	}

	if !out.HasConflicts {
		t.Error("expected conflict to be detected between positive and negative sentiments")
	}
	if len(out.ConflictDetails) == 0 {
		t.Error("expected conflict details, got none")
	}
	if out.FinalText != "Synthesized unified consensus" {
		t.Errorf("expected synthesized output, got %s", out.FinalText)
	}
}

func TestTeamFailurePolicies(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := config.AgentConfig{MaxConcurrentRuns: 4, MaxConcurrentAgents: 4}
	gen := &fakeGenerator{}
	service := NewService(cfg, store, gen, nil, nil)
	ctx := context.Background()
	_ = service.Start(ctx)
	defer func() { _ = service.Stop(ctx) }()

	// Register good agent and failing agent (disabled)
	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{ID: "good-agent", Name: "Good", Enabled: true})
	_ = service.Registry().RegisterAgent(ctx, AgentDefinition{ID: "failing-agent", Name: "Fail", Enabled: false})

	// 1. Fail Fast Policy
	failFastTeam := TeamDefinition{
		ID:            "team-fail-fast",
		Name:          "Fail Fast Team",
		Strategy:      StrategyParallel,
		FailurePolicy: FailureFailFast,
		Members: []TeamMember{
			{AgentID: "failing-agent", Required: true},
			{AgentID: "good-agent", Required: false},
		},
		Enabled: true,
	}
	_ = service.Registry().RegisterTeam(ctx, failFastTeam)

	runFF, errFF := service.StartRun(ctx, RunSpec{RunType: "team", TeamID: "team-fail-fast", Input: "test"}, "user")
	if errFF == nil || runFF.Status != StatusFailed {
		t.Errorf("expected fail fast failure, got status %s, err %v", runFF.Status, errFF)
	}

	// 2. Best Effort Policy
	bestEffortTeam := TeamDefinition{
		ID:            "team-best-effort",
		Name:          "Best Effort Team",
		Strategy:      StrategyParallel,
		FailurePolicy: FailureBestEffort,
		Members: []TeamMember{
			{AgentID: "failing-agent", Required: false},
			{AgentID: "good-agent", Required: false},
		},
		AggregationPolicy: AggregationCollectAll,
		Enabled:           true,
	}
	_ = service.Registry().RegisterTeam(ctx, bestEffortTeam)

	runBE, errBE := service.StartRun(ctx, RunSpec{RunType: "team", TeamID: "team-best-effort", Input: "test"}, "user")
	if errBE != nil || runBE.Status != StatusCompleted {
		t.Errorf("expected best effort success, got status %s, err %v", runBE.Status, errBE)
	}
}

func TestConcurrentExecutionsRaceSafety(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := config.AgentConfig{
		MaxConcurrentRuns:   8,
		MaxConcurrentAgents: 8,
		MaxParallelPerRun:   4,
		QueueLimit:          64,
	}
	gen := &fakeGenerator{reply: "concurrent success"}
	service := NewService(cfg, store, gen, nil, nil)
	ctx := context.Background()
	_ = service.Start(ctx)
	defer func() { _ = service.Stop(ctx) }()

	for i := 1; i <= 4; i++ {
		_ = service.Registry().RegisterAgent(ctx, AgentDefinition{
			ID:      string(rune('a' + i - 1)),
			Name:    "Worker",
			Enabled: true,
		})
	}

	team := TeamDefinition{
		ID:          "concurrent-team",
		Name:        "Concurrent Team",
		Strategy:    StrategyParallel,
		MaxParallel: 4,
		Members: []TeamMember{
			{AgentID: "a"},
			{AgentID: "b"},
			{AgentID: "c"},
			{AgentID: "d"},
		},
		Enabled: true,
	}
	_ = service.Registry().RegisterTeam(ctx, team)

	var wg sync.WaitGroup
	var completedCount int64
	workers := 8

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			run, err := service.StartRun(ctx, RunSpec{
				RunType: "team",
				TeamID:  "concurrent-team",
				Input:   "concurrent work",
			}, "test-user")
			if err == nil && run != nil && run.Status == StatusCompleted {
				atomic.AddInt64(&completedCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if completedCount != int64(workers) {
		t.Errorf("expected %d completed runs, got %d", workers, completedCount)
	}
}
