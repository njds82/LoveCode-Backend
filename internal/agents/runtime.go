package agents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
)

// Generator defines the internal generation contract satisfied by *routing.Router.
type Generator interface {
	Generate(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (routing.Result, error)
}

type AgentRuntime struct {
	generator Generator
	context   *ContextManager
}

func NewAgentRuntime(generator Generator, cm *ContextManager) *AgentRuntime {
	if cm == nil {
		cm = NewContextManager()
	}
	return &AgentRuntime{
		generator: generator,
		context:   cm,
	}
}

type TaskExecutionResult struct {
	TaskID           string
	AgentID          string
	Status           ExecutionStatus
	OutputText       string
	StructuredOutput map[string]any
	ErrorMessage     string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ProviderID       string
	ModelID          string
	Attempts         int
	Duration         time.Duration
}

// ExecuteTask executes a single agent against a task.
func (rt *AgentRuntime) ExecuteTask(ctx context.Context, agent AgentDefinition, task TaskRecord, ci ContextInput) (*TaskExecutionResult, error) {
	if !agent.Enabled {
		return nil, fmt.Errorf("agent %q is disabled", agent.ID)
	}

	start := time.Now()
	res := &TaskExecutionResult{
		TaskID:  task.ID,
		AgentID: agent.ID,
		Status:  StatusRunning,
	}

	// Apply task or agent timeout if specified
	taskTimeout := task.Timeout
	if taskTimeout <= 0 {
		taskTimeout = agent.Timeout
	}
	if taskTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, taskTimeout)
		defer cancel()
	}

	// 1. Build context messages
	ci.Agent = agent
	ci.TaskInput = task.InputData
	msgs := rt.context.BuildMessages(ci)
	if len(msgs) == 0 {
		return nil, errors.New("cannot execute task with empty messages")
	}

	// 2. Prepare ChatRequest
	model := agent.Model
	if model == "" {
		model = "auto"
	}
	req := providers.ChatRequest{
		Model:    model,
		Messages: msgs,
	}
	if agent.MaxTokens > 0 {
		maxT := agent.MaxTokens
		req.MaxTokens = &maxT
	}
	if agent.Params != nil {
		if temp, ok := agent.Params["temperature"].(float64); ok {
			req.Temperature = &temp
		}
		if topP, ok := agent.Params["top_p"].(float64); ok {
			req.TopP = &topP
		}
	}

	// 3. Determine max requests (bounded iterations)
	maxRequests := agent.MaxRequests
	if maxRequests <= 0 {
		maxRequests = 1
	}

	var lastGen routing.Result
	var genErr error

	for iteration := 0; iteration < maxRequests; iteration++ {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				res.Status = StatusTimedOut
				res.ErrorMessage = "task execution timed out"
				res.Duration = time.Since(start)
				return res, err
			}
			res.Status = StatusCancelled
			res.ErrorMessage = "task execution cancelled"
			res.Duration = time.Since(start)
			return res, err
		}

		lastGen, genErr = rt.generator.Generate(ctx, req, agent.RoutingProfile, "", "")
		if genErr != nil {
			break
		}

		if lastGen.Response != nil {
			if lastGen.Response.Usage != nil {
				if lastGen.Response.Usage.PromptTokens != nil {
					res.PromptTokens += *lastGen.Response.Usage.PromptTokens
				}
				if lastGen.Response.Usage.CompletionTokens != nil {
					res.CompletionTokens += *lastGen.Response.Usage.CompletionTokens
				}
				if lastGen.Response.Usage.TotalTokens != nil {
					res.TotalTokens += *lastGen.Response.Usage.TotalTokens
				}
			}
			res.ProviderID = lastGen.ProviderID
			res.ModelID = lastGen.ModelID
			res.Attempts += lastGen.Attempts
			res.OutputText = lastGen.Response.Message.Text()
		}

		// For single-turn tasks, we are done
		break
	}

	res.Duration = time.Since(start)

	if genErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(genErr, context.DeadlineExceeded) {
			res.Status = StatusTimedOut
			res.ErrorMessage = "generation timed out"
			return res, genErr
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(genErr, context.Canceled) {
			res.Status = StatusCancelled
			res.ErrorMessage = "generation cancelled"
			return res, genErr
		}
		res.Status = StatusFailed
		res.ErrorMessage = genErr.Error()
		return res, genErr
	}

	res.Status = StatusCompleted
	res.StructuredOutput = map[string]any{
		"agent_id":    agent.ID,
		"model_id":    res.ModelID,
		"provider_id": res.ProviderID,
		"attempts":    res.Attempts,
	}
	return res, nil
}
