package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/tools"
)

// Generator defines the internal generation contract satisfied by *routing.Router.
type Generator interface {
	Generate(ctx context.Context, req providers.ChatRequest, profileID, groupID, providerID string) (routing.Result, error)
}

// ToolRegistry defines the interface for querying tools available to agents.
type ToolRegistry interface {
	ListTools() []tools.ToolDefinition
}

// TaskStatusUpdater defines the interface for reporting task status transitions.
type TaskStatusUpdater interface {
	UpdateTaskStatus(ctx context.Context, taskID string, status ExecutionStatus, errMessage string) error
}

// TaskStatusUpdaterFunc is a function adapter implementing TaskStatusUpdater.
type TaskStatusUpdaterFunc func(ctx context.Context, taskID string, status ExecutionStatus, errMessage string) error

func (f TaskStatusUpdaterFunc) UpdateTaskStatus(ctx context.Context, taskID string, status ExecutionStatus, errMessage string) error {
	return f(ctx, taskID, status, errMessage)
}

type AgentRuntime struct {
	generator    Generator
	context      *ContextManager
	toolExecutor tools.ToolExecutor
	toolRegistry ToolRegistry
	taskUpdater  TaskStatusUpdater
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

// SetToolExecutor injects the tool executor.
func (rt *AgentRuntime) SetToolExecutor(exec tools.ToolExecutor) {
	rt.toolExecutor = exec
}

// SetToolRegistry injects the tool registry.
func (rt *AgentRuntime) SetToolRegistry(reg ToolRegistry) {
	rt.toolRegistry = reg
}

// SetTaskStatusUpdater injects the task status updater for state transitions.
func (rt *AgentRuntime) SetTaskStatusUpdater(updater TaskStatusUpdater) {
	rt.taskUpdater = updater
}

// SetToolExecutor on Service forwards to runtime.
func (s *Service) SetToolExecutor(exec tools.ToolExecutor) {
	if s.runtime != nil {
		s.runtime.SetToolExecutor(exec)
		if s.runtime.taskUpdater == nil && s.store != nil {
			s.runtime.SetTaskStatusUpdater(s.store)
		}
	}
}

// SetToolRegistry on Service forwards to runtime.
func (s *Service) SetToolRegistry(reg ToolRegistry) {
	if s.runtime != nil {
		s.runtime.SetToolRegistry(reg)
	}
}

// Runtime returns the internal AgentRuntime.
func (s *Service) Runtime() *AgentRuntime {
	return s.runtime
}

// UpdateTaskStatus implements TaskStatusUpdater on *Store.
func (s *Store) UpdateTaskStatus(ctx context.Context, taskID string, status ExecutionStatus, errMessage string) error {
	query := `UPDATE agent_tasks SET status = ?, error_message = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, string(status), errMessage, taskID)
	return err
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

	// Populate req.Tools and req.ToolChoice when tools are available and agent has capabilities
	if rt.toolRegistry != nil && len(agent.Capabilities) > 0 {
		var availableTools []providers.Tool
		allDefs := rt.toolRegistry.ListTools()
		for _, def := range allDefs {
			if !def.Enabled {
				continue
			}
			authorized := true
			if rt.toolExecutor != nil && len(def.RequiredCapabilities) > 0 {
				if err := rt.toolExecutor.ValidateCapabilities(def.Name, agent.Capabilities); err != nil {
					authorized = false
				}
			}
			if authorized {
				availableTools = append(availableTools, providers.Tool{
					Name:        def.Name,
					Description: def.Description,
					Parameters:  def.ParametersSchema,
				})
			}
		}
		if len(availableTools) > 0 {
			req.Tools = availableTools
			req.ToolChoice = &providers.ToolChoice{Mode: providers.ToolChoiceAuto}
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

			respMsg := lastGen.Response.Message
			if len(respMsg.ToolCalls) > 0 && rt.toolExecutor != nil {
				// 1. Transition task state: running -> waiting
				if rt.taskUpdater != nil && task.ID != "" {
					_ = rt.taskUpdater.UpdateTaskStatus(ctx, task.ID, StatusWaiting, "")
				}

				// 2. Append assistant message with ToolCalls to req.Messages
				req.Messages = append(req.Messages, providers.Message{
					Role:      providers.RoleAssistant,
					Content:   respMsg.Content,
					ToolCalls: respMsg.ToolCalls,
				})

				// 3. Execute each tool call
				for _, tc := range respMsg.ToolCalls {
					execCtx := tools.ExecutionContext{
						RunID:               task.RunID,
						TaskID:              task.ID,
						AgentID:             agent.ID,
						GrantedCapabilities: agent.Capabilities,
						GrantedPermissions:  agent.Permissions,
						Actor:               agent.ID,
					}
					resTool, execErr := rt.toolExecutor.Execute(ctx, execCtx, tc.Name, json.RawMessage(tc.Arguments))
					var toolOutput string
					if execErr != nil {
						toolOutput = fmt.Sprintf("Error: %v", execErr)
					} else {
						toolOutput = resTool.Output
					}

					// 4. Append providers.RoleTool message with output
					req.Messages = append(req.Messages, providers.Message{
						Role:       providers.RoleTool,
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Content:    []providers.ContentPart{providers.TextPart(toolOutput)},
					})
				}

				// 5. Transition task state: waiting -> running
				if rt.taskUpdater != nil && task.ID != "" {
					_ = rt.taskUpdater.UpdateTaskStatus(ctx, task.ID, StatusRunning, "")
				}

				// 6. Continue to next iteration of LLM generation
				continue
			}
		}

		// For single-turn or when no tool calls: we are done
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
