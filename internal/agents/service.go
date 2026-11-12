package agents

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/core"
)

type AuditRecorder interface {
	Append(ctx context.Context, e audit.Event) error
}

type Service struct {
	cfg         config.AgentConfig
	store       *Store
	registry    *Registry
	scheduler   *Scheduler
	runtime     *AgentRuntime
	aggregator  *Aggregator
	coordinator *TeamCoordinator
	audit       AuditRecorder
	log         *slog.Logger
}

func NewService(
	cfg config.AgentConfig,
	store *Store,
	generator Generator,
	auditLog AuditRecorder,
	log *slog.Logger,
) *Service {
	if log == nil {
		log = slog.Default()
	}
	reg := NewRegistry(store)
	sched := NewScheduler(cfg)
	cm := NewContextManager()
	rt := NewAgentRuntime(generator, cm)
	agg := NewAggregator(rt)
	coord := NewTeamCoordinator(reg, rt, agg, sched, store)

	return &Service{
		cfg:         cfg,
		store:       store,
		registry:    reg,
		scheduler:   sched,
		runtime:     rt,
		aggregator:  agg,
		coordinator: coord,
		audit:       auditLog,
		log:         log,
	}
}

// Module Lifecycle for core.Runtime

func (s *Service) Name() string { return "agents" }

func (s *Service) Start(ctx context.Context) error {
	// 1. Recover any interrupted executions from prior crash
	recovered, err := s.store.RecoverInterrupted(ctx)
	if err != nil {
		s.log.Error("failed to recover interrupted agent runs", "err", err)
	} else if recovered > 0 {
		s.log.Info("recovered interrupted agent runs", "count", recovered)
	}

	// 2. Load registered agents and teams into memory cache
	if err := s.registry.LoadFromStore(ctx); err != nil {
		s.log.Error("failed to load agent registry from store", "err", err)
		return err
	}

	s.log.Info("agent engine started",
		"agents", len(s.registry.ListAgents(true)),
		"teams", len(s.registry.ListTeams(true)),
		"max_runs", s.cfg.MaxConcurrentRuns,
		"max_agents", s.cfg.MaxConcurrentAgents,
	)
	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	s.scheduler.Shutdown()
	s.log.Info("agent engine stopped")
	return nil
}

func (s *Service) Health(ctx context.Context) core.Health {
	q, active := s.scheduler.ActiveCounts()
	return core.Health{
		Status: core.StatusOK,
		Detail: fmt.Sprintf("active: %d, queued: %d", active, q),
	}
}

// Registry Accessors

func (s *Service) Registry() *Registry   { return s.registry }
func (s *Service) Scheduler() *Scheduler { return s.scheduler }
func (s *Service) Store() *Store         { return s.store }

// Execution Methods

// StartRun starts execution for an agent or team run.
func (s *Service) StartRun(ctx context.Context, spec RunSpec, actor string) (*RunRecord, error) {
	// Queue check
	releaseQueue, err := s.scheduler.CheckQueueLimits()
	if err != nil {
		s.recordAudit(ctx, actor, "resource_limit.exceeded", "", map[string]any{"reason": "queue_full"})
		return nil, ErrQueueFull
	}
	defer releaseQueue()

	runID := "run-" + newID(12)
	now := time.Now().UTC()

	run := RunRecord{
		ID:             runID,
		RunType:        spec.RunType,
		AgentID:        spec.AgentID,
		TeamID:         spec.TeamID,
		Status:         StatusPending,
		InputContext:   spec.Input,
		ConfigSnapshot: spec.ConfigSnapshot,
		CreatedAt:      now,
	}

	if err := s.store.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("create run in store: %w", err)
	}
	s.recordAudit(ctx, actor, "run.created", runID, map[string]any{"type": spec.RunType, "agent": spec.AgentID, "team": spec.TeamID})

	// Register any ephemeral dynamic agents specified
	if len(spec.DynamicAgents) > 0 {
		if err := s.scheduler.ValidateRunLimits(0, len(spec.DynamicAgents), 0); err != nil {
			run.Status = StatusFailed
			run.ErrorMessage = err.Error()
			comp := time.Now().UTC()
			run.CompletedAt = &comp
			_ = s.store.UpdateRun(ctx, run)
			return &run, err
		}
		for _, da := range spec.DynamicAgents {
			parentID := spec.AgentID
			if err := s.registry.RegisterDynamicAgent(runID, parentID, da, s.cfg.MaxDynamicAgents); err != nil {
				run.Status = StatusFailed
				run.ErrorMessage = err.Error()
				comp := time.Now().UTC()
				run.CompletedAt = &comp
				_ = s.store.UpdateRun(ctx, run)
				return &run, err
			}
			s.recordAudit(ctx, actor, "subagent.created", da.ID, map[string]any{"run_id": runID, "parent": parentID})
		}
	}

	// Execution timeout
	runTimeout := spec.Timeout
	if runTimeout <= 0 && s.cfg.MaxExecutionDuration.Std() > 0 {
		runTimeout = s.cfg.MaxExecutionDuration.Std()
	}

	// Acquire global run slot
	releaseRun, err := s.scheduler.AcquireRun(ctx)
	if err != nil {
		run.Status = StatusFailed
		run.ErrorMessage = err.Error()
		comp := time.Now().UTC()
		run.CompletedAt = &comp
		_ = s.store.UpdateRun(ctx, run)
		return &run, err
	}

	runCtx := context.Background()
	var runCancel context.CancelFunc
	if runTimeout > 0 {
		runCtx, runCancel = context.WithTimeout(runCtx, runTimeout)
	} else {
		runCtx, runCancel = context.WithCancel(runCtx)
	}
	s.scheduler.RegisterRun(runID, runCancel)

	startT := time.Now().UTC()
	run.Status = StatusRunning
	run.StartedAt = &startT
	_ = s.store.UpdateRun(ctx, run)
	s.recordAudit(ctx, actor, "run.started", runID, map[string]any{"type": spec.RunType})

	// Execute synchronously
	defer func() {
		s.scheduler.UnregisterRun(runID)
		runCancel()
		releaseRun()
		s.registry.CleanupDynamicAgents(runID)
	}()

	var execErr error
	var totalPrompt, totalComp, totalTokens int

	if spec.RunType == "team" || spec.TeamID != "" {
		team, err := s.registry.GetTeam(spec.TeamID)
		if err != nil {
			return s.failRun(ctx, run, err, actor)
		}
		if !team.Enabled {
			return s.failRun(ctx, run, fmt.Errorf("team %q is disabled", team.ID), actor)
		}
		if err := s.scheduler.ValidateRunLimits(len(team.Members), len(spec.DynamicAgents), 0); err != nil {
			return s.failRun(ctx, run, err, actor)
		}

		var results []*TaskExecutionResult
		switch team.Strategy {
		case StrategyParallel:
			results, execErr = s.coordinator.ExecuteParallel(runCtx, runID, team, spec.Input)
		case StrategyDependency:
			results, execErr = s.coordinator.ExecuteDependency(runCtx, runID, team, spec.Input)
		case StrategySequential:
			fallthrough
		default:
			results, execErr = s.coordinator.ExecuteSequential(runCtx, runID, team, spec.Input)
		}

		// Calculate tokens
		for _, r := range results {
			if r != nil {
				totalPrompt += r.PromptTokens
				totalComp += r.CompletionTokens
				totalTokens += r.TotalTokens
			}
		}

		// Synthesizer agent if configured
		var synthAgent *AgentDefinition
		if team.SynthesizerAgentID != "" {
			if sa, err := s.registry.GetAgent(runID, team.SynthesizerAgentID); err == nil {
				synthAgent = &sa
			}
		}

		// Aggregate results
		primaryID := ""
		if len(team.Members) > 0 {
			primaryID = team.Members[0].AgentID
		}
		aggRes, _ := s.aggregator.Aggregate(runCtx, team.AggregationPolicy, primaryID, synthAgent, results)
		if aggRes.HasConflicts {
			s.recordAudit(ctx, actor, "conflict.detected", runID, map[string]any{"conflicts": aggRes.ConflictDetails})
		}
		s.recordAudit(ctx, actor, "aggregation.completed", runID, map[string]any{"policy": team.AggregationPolicy, "contributing": aggRes.ContributingAgents})

		run.OutputResult = aggRes.FinalText
		run.StructuredOutput = aggRes.StructuredData

	} else {
		// Single Agent Execution
		agent, err := s.registry.GetAgent(runID, spec.AgentID)
		if err != nil {
			return s.failRun(ctx, run, err, actor)
		}
		if !agent.Enabled {
			return s.failRun(ctx, run, fmt.Errorf("agent %q is disabled", agent.ID), actor)
		}

		task := TaskRecord{
			ID:        runID + "-task-1",
			RunID:     runID,
			AgentID:   agent.ID,
			Name:      agent.Name,
			Status:    StatusPending,
			InputData: spec.Input,
			CreatedAt: time.Now().UTC(),
		}
		_ = s.store.CreateTask(ctx, task)

		releaseAgent, err := s.scheduler.AcquireAgent(runCtx)
		if err != nil {
			return s.failRun(ctx, run, err, actor)
		}

		taskCtx, taskCancel := context.WithCancel(runCtx)
		s.scheduler.RegisterTask(task.ID, taskCancel)

		task.Status = StatusRunning
		tStart := time.Now().UTC()
		task.StartedAt = &tStart
		_ = s.store.UpdateTask(ctx, task)

		s.recordAudit(ctx, actor, "task.started", task.ID, map[string]any{"agent": agent.ID})

		ci := ContextInput{
			Agent:     agent,
			TaskInput: spec.Input,
		}
		res, rErr := s.runtime.ExecuteTask(taskCtx, agent, task, ci)
		taskCancel()
		s.scheduler.UnregisterTask(task.ID)
		releaseAgent()

		if res == nil {
			res = &TaskExecutionResult{
				TaskID:       task.ID,
				AgentID:      agent.ID,
				Status:       StatusFailed,
				ErrorMessage: "nil execution result",
			}
		}

		comp := time.Now().UTC()
		task.Status = res.Status
		task.CompletedAt = &comp
		task.OutputData = res.OutputText
		task.StructuredOutput = res.StructuredOutput
		task.ErrorMessage = res.ErrorMessage
		_ = s.store.UpdateTask(ctx, task)

		totalPrompt = res.PromptTokens
		totalComp = res.CompletionTokens
		totalTokens = res.TotalTokens

		run.OutputResult = res.OutputText
		run.StructuredOutput = res.StructuredOutput
		execErr = rErr

		if res.Status == StatusCompleted {
			s.recordAudit(ctx, actor, "task.completed", task.ID, map[string]any{"agent": agent.ID})
		} else {
			s.recordAudit(ctx, actor, "task.failed", task.ID, map[string]any{"agent": agent.ID, "error": res.ErrorMessage})
		}
	}

	run.PromptTokens = totalPrompt
	run.CompletionTokens = totalComp
	run.TotalTokens = totalTokens

	compTime := time.Now().UTC()
	run.CompletedAt = &compTime

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) || errors.Is(execErr, context.DeadlineExceeded) {
		run.Status = StatusTimedOut
		run.ErrorMessage = "run execution timed out"
		_ = s.store.UpdateRun(ctx, run)
		s.recordAudit(ctx, actor, "run.timed_out", runID, nil)
		return &run, runCtx.Err()
	}
	if errors.Is(runCtx.Err(), context.Canceled) || errors.Is(execErr, context.Canceled) {
		run.Status = StatusCancelled
		run.ErrorMessage = "run execution cancelled"
		_ = s.store.UpdateRun(ctx, run)
		s.recordAudit(ctx, actor, "run.cancelled", runID, nil)
		return &run, runCtx.Err()
	}
	if execErr != nil {
		run.Status = StatusFailed
		run.ErrorMessage = execErr.Error()
		_ = s.store.UpdateRun(ctx, run)
		s.recordAudit(ctx, actor, "run.failed", runID, map[string]any{"error": execErr.Error()})
		return &run, execErr
	}

	run.Status = StatusCompleted
	_ = s.store.UpdateRun(ctx, run)
	s.recordAudit(ctx, actor, "run.completed", runID, map[string]any{"tokens": totalTokens})

	return &run, nil
}

func (s *Service) failRun(ctx context.Context, run RunRecord, err error, actor string) (*RunRecord, error) {
	comp := time.Now().UTC()
	run.Status = StatusFailed
	run.ErrorMessage = err.Error()
	run.CompletedAt = &comp
	_ = s.store.UpdateRun(ctx, run)
	s.recordAudit(ctx, actor, "run.failed", run.ID, map[string]any{"error": err.Error()})
	return &run, err
}

func (s *Service) CancelRun(ctx context.Context, runID string, actor string) error {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status.IsTerminal() {
		return fmt.Errorf("cannot cancel run in terminal status %q", run.Status)
	}

	s.scheduler.CancelRun(runID)

	comp := time.Now().UTC()
	run.Status = StatusCancelled
	run.ErrorMessage = "cancelled by request"
	run.CompletedAt = &comp
	_ = s.store.UpdateRun(ctx, *run)
	s.recordAudit(ctx, actor, "run.cancelled", runID, nil)
	return nil
}

func (s *Service) CancelTask(ctx context.Context, taskID string, actor string) error {
	task, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status.IsTerminal() {
		return fmt.Errorf("cannot cancel task in terminal status %q", task.Status)
	}

	s.scheduler.CancelTask(taskID)

	comp := time.Now().UTC()
	task.Status = StatusCancelled
	task.ErrorMessage = "cancelled by request"
	task.CompletedAt = &comp
	_ = s.store.UpdateTask(ctx, *task)
	s.recordAudit(ctx, actor, "task.cancelled", taskID, nil)
	return nil
}

func (s *Service) recordAudit(ctx context.Context, actor, action, target string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	if actor == "" {
		actor = "anonymous"
	}
	_ = s.audit.Append(ctx, audit.Event{
		Actor:  actor,
		Action: action,
		Target: target,
		Detail: detail,
	})
}

func newID(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
