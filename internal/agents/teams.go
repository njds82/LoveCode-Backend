package agents

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type TeamCoordinator struct {
	registry   *Registry
	runtime    *AgentRuntime
	aggregator *Aggregator
	scheduler  *Scheduler
	store      *Store
}

func NewTeamCoordinator(
	reg *Registry,
	rt *AgentRuntime,
	agg *Aggregator,
	sched *Scheduler,
	st *Store,
) *TeamCoordinator {
	return &TeamCoordinator{
		registry:   reg,
		runtime:    rt,
		aggregator: agg,
		scheduler:  sched,
		store:      st,
	}
}

// ExecuteSequential executes team members in explicit sequential order.
func (tc *TeamCoordinator) ExecuteSequential(
	ctx context.Context,
	runID string,
	team TeamDefinition,
	input string,
) ([]*TaskExecutionResult, error) {
	// Determine member sequence
	var orderedMembers []TeamMember
	if len(team.ExecutionOrder) > 0 {
		memberMap := make(map[string]TeamMember, len(team.Members))
		for _, m := range team.Members {
			memberMap[m.AgentID] = m
		}
		for _, id := range team.ExecutionOrder {
			if m, ok := memberMap[id]; ok {
				orderedMembers = append(orderedMembers, m)
			}
		}
	} else {
		orderedMembers = append(orderedMembers, team.Members...)
	}

	results := make([]*TaskExecutionResult, 0, len(orderedMembers))
	var lastOutput string
	var allSummaries []string

	for i, m := range orderedMembers {
		if err := ctx.Err(); err != nil {
			return results, err
		}

		agent, err := tc.registry.GetAgent(runID, m.AgentID)
		if err != nil {
			return results, fmt.Errorf("load agent %q: %w", m.AgentID, err)
		}

		taskID := fmt.Sprintf("%s-task-%d", runID, i+1)
		task := TaskRecord{
			ID:        taskID,
			RunID:     runID,
			AgentID:   agent.ID,
			Name:      fmt.Sprintf("%s: %s", team.Name, agent.Name),
			Status:    StatusPending,
			InputData: input,
			CreatedAt: time.Now().UTC(),
		}
		if err := tc.store.CreateTask(ctx, task); err != nil {
			return results, fmt.Errorf("create task: %w", err)
		}

		// Context input
		ci := ContextInput{
			Agent:       agent,
			TaskInput:   input,
			HandoffInfo: lastOutput,
			SharedInfo:  lastOutput,
			Summaries:   allSummaries,
		}

		// Execute with agent concurrency limit
		release, err := tc.scheduler.AcquireAgent(ctx)
		if err != nil {
			return results, err
		}

		taskCtx, taskCancel := context.WithCancel(ctx)
		tc.scheduler.RegisterTask(task.ID, taskCancel)

		task.Status = StatusRunning
		now := time.Now().UTC()
		task.StartedAt = &now
		_ = tc.store.UpdateTask(ctx, task)

		res, execErr := tc.runtime.ExecuteTask(taskCtx, agent, task, ci)
		taskCancel()
		tc.scheduler.UnregisterTask(task.ID)
		release()

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
		_ = tc.store.UpdateTask(ctx, task)

		results = append(results, res)

		if execErr != nil || res.Status != StatusCompleted {
			// Handle failure policies
			if m.Required || team.FailurePolicy == FailureFailFast || team.FailurePolicy == "" {
				errMsg := res.ErrorMessage
				if errMsg == "" && execErr != nil {
					errMsg = execErr.Error()
				}
				return results, fmt.Errorf("required team member %q failed: %s", m.AgentID, errMsg)
			}
		} else {
			lastOutput = res.OutputText
			allSummaries = append(allSummaries, res.OutputText)
		}
	}

	return results, nil
}

// ExecuteParallel executes team members in parallel up to max parallel concurrency.
func (tc *TeamCoordinator) ExecuteParallel(
	ctx context.Context,
	runID string,
	team TeamDefinition,
	input string,
) ([]*TaskExecutionResult, error) {
	maxParallel := team.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 2
	}
	sem := make(chan struct{}, maxParallel)

	results := make([]*TaskExecutionResult, len(team.Members))
	errs := make([]error, len(team.Members))

	var wg sync.WaitGroup
	ctxTeam, cancelTeam := context.WithCancel(ctx)
	defer cancelTeam()

	for idx, member := range team.Members {
		wg.Add(1)
		go func(i int, m TeamMember) {
			defer wg.Done()

			select {
			case <-ctxTeam.Done():
				results[i] = &TaskExecutionResult{
					AgentID:      m.AgentID,
					Status:       StatusCancelled,
					ErrorMessage: "parallel team execution cancelled",
				}
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			agent, err := tc.registry.GetAgent(runID, m.AgentID)
			if err != nil {
				errs[i] = err
				results[i] = &TaskExecutionResult{
					AgentID:      m.AgentID,
					Status:       StatusFailed,
					ErrorMessage: err.Error(),
				}
				if m.Required || team.FailurePolicy == FailureFailFast {
					cancelTeam()
				}
				return
			}

			taskID := fmt.Sprintf("%s-ptask-%d", runID, i+1)
			task := TaskRecord{
				ID:        taskID,
				RunID:     runID,
				AgentID:   agent.ID,
				Name:      fmt.Sprintf("%s: %s", team.Name, agent.Name),
				Status:    StatusPending,
				InputData: input,
				CreatedAt: time.Now().UTC(),
			}
			_ = tc.store.CreateTask(ctxTeam, task)

			releaseAgent, err := tc.scheduler.AcquireAgent(ctxTeam)
			if err != nil {
				errs[i] = err
				results[i] = &TaskExecutionResult{
					TaskID:       task.ID,
					AgentID:      agent.ID,
					Status:       StatusCancelled,
					ErrorMessage: err.Error(),
				}
				return
			}
			defer releaseAgent()

			taskCtx, taskCancel := context.WithCancel(ctxTeam)
			tc.scheduler.RegisterTask(task.ID, taskCancel)
			defer func() {
				taskCancel()
				tc.scheduler.UnregisterTask(task.ID)
			}()

			now := time.Now().UTC()
			task.Status = StatusRunning
			task.StartedAt = &now
			_ = tc.store.UpdateTask(ctxTeam, task)

			ci := ContextInput{
				Agent:     agent,
				TaskInput: input,
			}
			res, execErr := tc.runtime.ExecuteTask(taskCtx, agent, task, ci)
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
			_ = tc.store.UpdateTask(ctxTeam, task)

			results[i] = res

			if execErr != nil || res.Status != StatusCompleted {
				errs[i] = execErr
				if m.Required || team.FailurePolicy == FailureFailFast {
					cancelTeam()
				}
			}
		}(idx, member)
	}

	wg.Wait()

	// Check if any required member failed
	for i, m := range team.Members {
		if m.Required && (results[i] == nil || results[i].Status != StatusCompleted) {
			errMsg := "required member failed"
			if results[i] != nil && results[i].ErrorMessage != "" {
				errMsg = results[i].ErrorMessage
			}
			return results, fmt.Errorf("required team member %q failed: %s", m.AgentID, errMsg)
		}
	}

	return results, nil
}

// ExecuteDependency executes tasks respecting dependency DAGs, with cycle detection.
func (tc *TeamCoordinator) ExecuteDependency(
	ctx context.Context,
	runID string,
	team TeamDefinition,
	input string,
) ([]*TaskExecutionResult, error) {
	// Build graph and check cycles
	membersByAgent := make(map[string]TeamMember, len(team.Members))
	for _, m := range team.Members {
		membersByAgent[m.AgentID] = m
	}

	deps := make(map[string][]string)
	for agentID, depList := range team.Dependencies {
		deps[agentID] = depList
	}
	for _, m := range team.Members {
		if len(m.Dependencies) > 0 {
			deps[m.AgentID] = append(deps[m.AgentID], m.Dependencies...)
		}
	}

	if err := detectCycles(team.Members, deps); err != nil {
		return nil, fmt.Errorf("dependency cycle detected: %w", err)
	}

	maxParallel := team.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 2
	}
	sem := make(chan struct{}, maxParallel)

	type taskState struct {
		member   TeamMember
		taskID   string
		status   ExecutionStatus
		res      *TaskExecutionResult
		deps     []string
		doneChan chan struct{}
	}

	states := make(map[string]*taskState, len(team.Members))
	for i, m := range team.Members {
		tID := fmt.Sprintf("%s-deptask-%d", runID, i+1)
		states[m.AgentID] = &taskState{
			member:   m,
			taskID:   tID,
			status:   StatusPending,
			deps:     deps[m.AgentID],
			doneChan: make(chan struct{}),
		}
	}

	ctxTeam, cancelTeam := context.WithCancel(ctx)
	defer cancelTeam()

	var wg sync.WaitGroup
	var resultsLock sync.Mutex
	results := make([]*TaskExecutionResult, 0, len(team.Members))
	var firstFatalErr error
	var fatalErrOnce sync.Once

	for _, state := range states {
		wg.Add(1)
		go func(st *taskState) {
			defer wg.Done()
			defer close(st.doneChan)

			// 1. Wait for all prerequisite dependencies
			var depOutputs []string
			for _, depID := range st.deps {
				depState, exists := states[depID]
				if !exists {
					continue
				}
				select {
				case <-ctxTeam.Done():
					st.status = StatusCancelled
					st.res = &TaskExecutionResult{
						TaskID:       st.taskID,
						AgentID:      st.member.AgentID,
						Status:       StatusCancelled,
						ErrorMessage: "execution cancelled",
					}
					resultsLock.Lock()
					results = append(results, st.res)
					resultsLock.Unlock()
					return
				case <-depState.doneChan:
					// Check if dependency succeeded
					if depState.status != StatusCompleted {
						st.status = StatusFailed
						st.res = &TaskExecutionResult{
							TaskID:       st.taskID,
							AgentID:      st.member.AgentID,
							Status:       StatusFailed,
							ErrorMessage: fmt.Sprintf("dependency %q failed or cancelled", depID),
						}
						resultsLock.Lock()
						results = append(results, st.res)
						resultsLock.Unlock()

						if st.member.Required || team.FailurePolicy == FailureFailFast {
							fatalErrOnce.Do(func() {
								firstFatalErr = fmt.Errorf("required dependency %q failed", depID)
								cancelTeam()
							})
						}
						return
					}
					if depState.res != nil {
						depOutputs = append(depOutputs, depState.res.OutputText)
					}
				}
			}

			// 2. All dependencies succeeded; acquire slot and run
			select {
			case <-ctxTeam.Done():
				st.status = StatusCancelled
				st.res = &TaskExecutionResult{
					TaskID:       st.taskID,
					AgentID:      st.member.AgentID,
					Status:       StatusCancelled,
					ErrorMessage: "execution cancelled",
				}
				resultsLock.Lock()
				results = append(results, st.res)
				resultsLock.Unlock()
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			agent, err := tc.registry.GetAgent(runID, st.member.AgentID)
			if err != nil {
				st.status = StatusFailed
				st.res = &TaskExecutionResult{
					TaskID:       st.taskID,
					AgentID:      st.member.AgentID,
					Status:       StatusFailed,
					ErrorMessage: err.Error(),
				}
				resultsLock.Lock()
				results = append(results, st.res)
				resultsLock.Unlock()
				return
			}

			task := TaskRecord{
				ID:           st.taskID,
				RunID:        runID,
				AgentID:      agent.ID,
				Name:         fmt.Sprintf("%s: %s", team.Name, agent.Name),
				Status:       StatusPending,
				InputData:    input,
				Dependencies: st.deps,
				CreatedAt:    time.Now().UTC(),
			}
			_ = tc.store.CreateTask(ctxTeam, task)

			releaseAgent, err := tc.scheduler.AcquireAgent(ctxTeam)
			if err != nil {
				st.status = StatusCancelled
				st.res = &TaskExecutionResult{
					TaskID:       st.taskID,
					AgentID:      agent.ID,
					Status:       StatusCancelled,
					ErrorMessage: err.Error(),
				}
				resultsLock.Lock()
				results = append(results, st.res)
				resultsLock.Unlock()
				return
			}
			defer releaseAgent()

			taskCtx, taskCancel := context.WithCancel(ctxTeam)
			tc.scheduler.RegisterTask(task.ID, taskCancel)
			defer func() {
				taskCancel()
				tc.scheduler.UnregisterTask(task.ID)
			}()

			now := time.Now().UTC()
			task.Status = StatusRunning
			task.StartedAt = &now
			_ = tc.store.UpdateTask(ctxTeam, task)

			ci := ContextInput{
				Agent:       agent,
				TaskInput:   input,
				SharedInfo:  fmt.Sprintf("Outputs from dependencies: %v", depOutputs),
				HandoffInfo: fmt.Sprintf("%v", depOutputs),
				Summaries:   depOutputs,
			}

			res, execErr := tc.runtime.ExecuteTask(taskCtx, agent, task, ci)
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
			_ = tc.store.UpdateTask(ctxTeam, task)

			st.status = res.Status
			st.res = res

			resultsLock.Lock()
			results = append(results, res)
			resultsLock.Unlock()

			if execErr != nil || res.Status != StatusCompleted {
				if st.member.Required || team.FailurePolicy == FailureFailFast {
					fatalErrOnce.Do(func() {
						firstFatalErr = fmt.Errorf("team member %q failed: %s", st.member.AgentID, res.ErrorMessage)
						cancelTeam()
					})
				}
			}
		}(state)
	}

	wg.Wait()

	if firstFatalErr != nil {
		return results, firstFatalErr
	}

	return results, nil
}

// detectCycles checks for circular dependencies among team members using DFS.
func detectCycles(members []TeamMember, deps map[string][]string) error {
	visited := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited

	var visit func(node string, path []string) error
	visit = func(node string, path []string) error {
		state := visited[node]
		if state == 1 {
			return fmt.Errorf("cycle: %s -> %s", formatCyclePath(path), node)
		}
		if state == 2 {
			return nil
		}
		visited[node] = 1
		currentPath := append(path, node)
		for _, dep := range deps[node] {
			if err := visit(dep, currentPath); err != nil {
				return err
			}
		}
		visited[node] = 2
		return nil
	}

	for _, m := range members {
		if visited[m.AgentID] == 0 {
			if err := visit(m.AgentID, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatCyclePath(path []string) string {
	res := ""
	for i, p := range path {
		if i > 0 {
			res += " -> "
		}
		res += p
	}
	return res
}
