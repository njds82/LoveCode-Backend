package agents

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/lovecode/lovecode/internal/config"
)

var (
	ErrQueueFull          = errors.New("agent execution queue is full")
	ErrConcurrencyLimit   = errors.New("concurrency limit exceeded")
	ErrExecutionCancelled = errors.New("execution cancelled")
)

type Scheduler struct {
	cfg config.AgentConfig

	runSem   chan struct{}
	agentSem chan struct{}

	queuedCount int64
	activeCount int64

	mu          sync.Mutex
	runCancels  map[string]context.CancelFunc
	taskCancels map[string]context.CancelFunc
	stopped     bool
}

func NewScheduler(cfg config.AgentConfig) *Scheduler {
	maxRuns := cfg.MaxConcurrentRuns
	if maxRuns <= 0 {
		maxRuns = 8
	}
	maxAgents := cfg.MaxConcurrentAgents
	if maxAgents <= 0 {
		maxAgents = 4
	}

	return &Scheduler{
		cfg:         cfg,
		runSem:      make(chan struct{}, maxRuns),
		agentSem:    make(chan struct{}, maxAgents),
		runCancels:  make(map[string]context.CancelFunc),
		taskCancels: make(map[string]context.CancelFunc),
	}
}

// CheckQueueLimits increments the queue counter or returns ErrQueueFull.
func (s *Scheduler) CheckQueueLimits() (func(), error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil, errors.New("scheduler is stopped")
	}
	queued := atomic.LoadInt64(&s.queuedCount)
	active := atomic.LoadInt64(&s.activeCount)
	limit := int64(s.cfg.QueueLimit)
	if limit > 0 && (queued+active) >= limit {
		s.mu.Unlock()
		return nil, ErrQueueFull
	}
	atomic.AddInt64(&s.queuedCount, 1)
	s.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			atomic.AddInt64(&s.queuedCount, -1)
		})
	}
	return cleanup, nil
}

// AcquireRun acquires a global run concurrency slot.
func (s *Scheduler) AcquireRun(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.runSem <- struct{}{}:
	}

	atomic.AddInt64(&s.activeCount, 1)

	var once sync.Once
	release := func() {
		once.Do(func() {
			atomic.AddInt64(&s.activeCount, -1)
			<-s.runSem
		})
	}
	return release, nil
}

// AcquireAgent acquires a slot for executing an individual agent.
func (s *Scheduler) AcquireAgent(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s.agentSem <- struct{}{}:
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			<-s.agentSem
		})
	}
	return release, nil
}

// RegisterRun records a run's cancellation function.
func (s *Scheduler) RegisterRun(runID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runCancels[runID] = cancel
}

// UnregisterRun unregisters a run's cancellation function.
func (s *Scheduler) UnregisterRun(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runCancels, runID)
}

// CancelRun triggers cancellation of an active run.
func (s *Scheduler) CancelRun(runID string) bool {
	s.mu.Lock()
	cancel, ok := s.runCancels[runID]
	s.mu.Unlock()

	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

// RegisterTask records a task's cancellation function.
func (s *Scheduler) RegisterTask(taskID string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskCancels[taskID] = cancel
}

// UnregisterTask unregisters a task's cancellation function.
func (s *Scheduler) UnregisterTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.taskCancels, taskID)
}

// CancelTask triggers cancellation of an active task.
func (s *Scheduler) CancelTask(taskID string) bool {
	s.mu.Lock()
	cancel, ok := s.taskCancels[taskID]
	s.mu.Unlock()

	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

// Shutdown cancels all active runs and tasks.
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	s.stopped = true
	runCancels := make([]context.CancelFunc, 0, len(s.runCancels))
	for _, c := range s.runCancels {
		if c != nil {
			runCancels = append(runCancels, c)
		}
	}
	taskCancels := make([]context.CancelFunc, 0, len(s.taskCancels))
	for _, c := range s.taskCancels {
		if c != nil {
			taskCancels = append(taskCancels, c)
		}
	}
	s.mu.Unlock()

	for _, c := range runCancels {
		c()
	}
	for _, c := range taskCancels {
		c()
	}
}

// Config returns the scheduler's configuration.
func (s *Scheduler) Config() config.AgentConfig {
	return s.cfg
}

// ActiveCounts returns current queue and active execution counts.
func (s *Scheduler) ActiveCounts() (int64, int64) {
	return atomic.LoadInt64(&s.queuedCount), atomic.LoadInt64(&s.activeCount)
}

// ValidateRunLimits checks whether a run specification respects configured limits.
func (s *Scheduler) ValidateRunLimits(memberCount int, dynamicAgentCount int, delegationDepth int) error {
	if s.cfg.MaxTeamSize > 0 && memberCount > s.cfg.MaxTeamSize {
		return fmt.Errorf("team size %d exceeds limit of %d", memberCount, s.cfg.MaxTeamSize)
	}
	if s.cfg.MaxDynamicAgents > 0 && dynamicAgentCount > s.cfg.MaxDynamicAgents {
		return fmt.Errorf("dynamic agents count %d exceeds limit of %d", dynamicAgentCount, s.cfg.MaxDynamicAgents)
	}
	if s.cfg.MaxDelegationDepth > 0 && delegationDepth > s.cfg.MaxDelegationDepth {
		return fmt.Errorf("delegation depth %d exceeds limit of %d", delegationDepth, s.cfg.MaxDelegationDepth)
	}
	return nil
}
