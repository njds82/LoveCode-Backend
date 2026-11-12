package agents

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ExecutionStatus string

const (
	StatusPending   ExecutionStatus = "pending"
	StatusQueued    ExecutionStatus = "queued"
	StatusRunning   ExecutionStatus = "running"
	StatusWaiting   ExecutionStatus = "waiting"
	StatusCompleted ExecutionStatus = "completed"
	StatusFailed    ExecutionStatus = "failed"
	StatusCancelled ExecutionStatus = "cancelled"
	StatusTimedOut  ExecutionStatus = "timed_out"
)

func (s ExecutionStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

func ValidateTransition(from, to ExecutionStatus) error {
	if from == to {
		return nil
	}
	if from.IsTerminal() {
		return fmt.Errorf("cannot transition from terminal state %q to %q", from, to)
	}
	switch from {
	case StatusPending:
		if to == StatusQueued || to == StatusRunning || to == StatusCancelled || to == StatusFailed {
			return nil
		}
	case StatusQueued:
		if to == StatusRunning || to == StatusCancelled || to == StatusTimedOut || to == StatusFailed {
			return nil
		}
	case StatusRunning:
		if to == StatusWaiting || to == StatusCompleted || to == StatusFailed || to == StatusCancelled || to == StatusTimedOut {
			return nil
		}
	case StatusWaiting:
		if to == StatusRunning || to == StatusCompleted || to == StatusFailed || to == StatusCancelled || to == StatusTimedOut {
			return nil
		}
	}
	return fmt.Errorf("invalid execution state transition from %q to %q", from, to)
}

type ContextPolicy string

const (
	ContextIsolated   ContextPolicy = "isolated"
	ContextShared     ContextPolicy = "shared"
	ContextHandoff    ContextPolicy = "handoff"
	ContextSummarized ContextPolicy = "summarized"
)

func (c ContextPolicy) Valid() bool {
	switch c {
	case ContextIsolated, ContextShared, ContextHandoff, ContextSummarized:
		return true
	default:
		return false
	}
}

type AutonomyLevel string

const (
	AutonomyReadOnly   AutonomyLevel = "readonly"
	AutonomyGuided     AutonomyLevel = "guided"
	AutonomyAutonomous AutonomyLevel = "autonomous"
)

func (a AutonomyLevel) Valid() bool {
	switch a {
	case AutonomyReadOnly, AutonomyGuided, AutonomyAutonomous:
		return true
	default:
		return false
	}
}

type ExecutionStrategy string

const (
	StrategySequential ExecutionStrategy = "sequential"
	StrategyParallel   ExecutionStrategy = "parallel"
	StrategyDependency ExecutionStrategy = "dependency"
)

func (s ExecutionStrategy) Valid() bool {
	switch s {
	case StrategySequential, StrategyParallel, StrategyDependency:
		return true
	default:
		return false
	}
}

type AggregationPolicy string

const (
	AggregationCollectAll  AggregationPolicy = "collect_all"
	AggregationOrdered     AggregationPolicy = "ordered"
	AggregationPrimary     AggregationPolicy = "primary"
	AggregationSynthesizer AggregationPolicy = "synthesizer"
	AggregationMerge       AggregationPolicy = "merge"
)

func (a AggregationPolicy) Valid() bool {
	switch a {
	case AggregationCollectAll, AggregationOrdered, AggregationPrimary, AggregationSynthesizer, AggregationMerge:
		return true
	default:
		return false
	}
}

type FailurePolicy string

const (
	FailureFailFast            FailurePolicy = "fail_fast"
	FailureContinueIndependent FailurePolicy = "continue_independent"
	FailureBestEffort          FailurePolicy = "best_effort"
)

func (f FailurePolicy) Valid() bool {
	switch f {
	case FailureFailFast, FailureContinueIndependent, FailureBestEffort:
		return true
	default:
		return false
	}
}

type MessageType string

const (
	MessageText     MessageType = "text"
	MessageHandoff  MessageType = "handoff"
	MessageSummary  MessageType = "summary"
	MessageDecision MessageType = "decision"
)

type AgentDefinition struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Role           string         `json:"role,omitempty"`
	SystemPrompt   string         `json:"system_prompt,omitempty"`
	Model          string         `json:"model,omitempty"`
	RoutingProfile string         `json:"routing_profile,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	Timeout        time.Duration  `json:"timeout,omitempty"`
	MaxRequests    int            `json:"max_requests,omitempty"`
	AutonomyLevel  AutonomyLevel  `json:"autonomy_level,omitempty"`
	ContextPolicy  ContextPolicy  `json:"context_policy,omitempty"`
	Capabilities   []string       `json:"capabilities,omitempty"`
	Permissions    []string       `json:"permissions,omitempty"`
	Enabled        bool           `json:"enabled"`
	IsDynamic      bool           `json:"is_dynamic,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (a AgentDefinition) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("agent id must not be empty")
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("agent name must not be empty")
	}
	if a.AutonomyLevel != "" && !a.AutonomyLevel.Valid() {
		return fmt.Errorf("invalid autonomy level %q", a.AutonomyLevel)
	}
	if a.ContextPolicy != "" && !a.ContextPolicy.Valid() {
		return fmt.Errorf("invalid context policy %q", a.ContextPolicy)
	}
	if a.MaxRequests < 0 {
		return errors.New("max_requests must be non-negative")
	}
	if a.MaxTokens < 0 {
		return errors.New("max_tokens must be non-negative")
	}
	if a.Timeout < 0 {
		return errors.New("timeout must be non-negative")
	}
	return nil
}

type TeamMember struct {
	AgentID      string   `json:"agent_id"`
	Role         string   `json:"role,omitempty"`
	Order        int      `json:"order,omitempty"`
	Required     bool     `json:"required,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type TeamDefinition struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Description        string              `json:"description,omitempty"`
	Strategy           ExecutionStrategy   `json:"strategy"`
	Members            []TeamMember        `json:"members"`
	ExecutionOrder     []string            `json:"execution_order,omitempty"`
	Dependencies       map[string][]string `json:"dependencies,omitempty"`
	ContextPolicy      ContextPolicy       `json:"context_policy,omitempty"`
	AggregationPolicy  AggregationPolicy   `json:"aggregation_policy,omitempty"`
	FailurePolicy      FailurePolicy       `json:"failure_policy,omitempty"`
	SynthesizerAgentID string              `json:"synthesizer_agent_id,omitempty"`
	MaxParallel        int                 `json:"max_parallel,omitempty"`
	Timeout            time.Duration       `json:"timeout,omitempty"`
	Enabled            bool                `json:"enabled"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

func (t TeamDefinition) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("team id must not be empty")
	}
	if strings.TrimSpace(t.Name) == "" {
		return errors.New("team name must not be empty")
	}
	if t.Strategy != "" && !t.Strategy.Valid() {
		return fmt.Errorf("invalid team strategy %q", t.Strategy)
	}
	if t.ContextPolicy != "" && !t.ContextPolicy.Valid() {
		return fmt.Errorf("invalid context policy %q", t.ContextPolicy)
	}
	if t.AggregationPolicy != "" && !t.AggregationPolicy.Valid() {
		return fmt.Errorf("invalid aggregation policy %q", t.AggregationPolicy)
	}
	if t.FailurePolicy != "" && !t.FailurePolicy.Valid() {
		return fmt.Errorf("invalid failure policy %q", t.FailurePolicy)
	}
	if t.MaxParallel < 0 {
		return errors.New("max_parallel must be non-negative")
	}
	if t.Timeout < 0 {
		return errors.New("timeout must be non-negative")
	}
	if len(t.Members) == 0 {
		return errors.New("team must have at least one member")
	}
	memberIDs := make(map[string]bool, len(t.Members))
	for _, m := range t.Members {
		if strings.TrimSpace(m.AgentID) == "" {
			return errors.New("team member agent_id must not be empty")
		}
		memberIDs[m.AgentID] = true
	}
	return nil
}

type RunSpec struct {
	RunType        string            `json:"run_type"` // "agent" or "team"
	AgentID        string            `json:"agent_id,omitempty"`
	TeamID         string            `json:"team_id,omitempty"`
	DynamicAgents  []AgentDefinition `json:"dynamic_agents,omitempty"`
	Input          string            `json:"input"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	ConfigSnapshot map[string]any    `json:"config_snapshot,omitempty"`
}

type RunRecord struct {
	ID               string          `json:"id"`
	RunType          string          `json:"run_type"`
	AgentID          string          `json:"agent_id,omitempty"`
	TeamID           string          `json:"team_id,omitempty"`
	Status           ExecutionStatus `json:"status"`
	InputContext     string          `json:"input_context"`
	OutputResult     string          `json:"output_result"`
	StructuredOutput map[string]any  `json:"structured_output,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      int             `json:"total_tokens"`
	ConfigSnapshot   map[string]any  `json:"config_snapshot,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
}

type TaskRecord struct {
	ID               string          `json:"id"`
	RunID            string          `json:"run_id"`
	ParentTaskID     string          `json:"parent_task_id,omitempty"`
	AgentID          string          `json:"agent_id"`
	Name             string          `json:"name,omitempty"`
	Description      string          `json:"description,omitempty"`
	Status           ExecutionStatus `json:"status"`
	Priority         int             `json:"priority"`
	Timeout          time.Duration   `json:"timeout,omitempty"`
	RetryCount       int             `json:"retry_count"`
	MaxRetries       int             `json:"max_retries"`
	InputData        string          `json:"input_data,omitempty"`
	OutputData       string          `json:"output_data,omitempty"`
	StructuredOutput map[string]any  `json:"structured_output,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
	Dependencies     []string        `json:"dependencies,omitempty"`
}

type AgentMessage struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	FromAgentID string         `json:"from_agent_id"`
	ToAgentID   string         `json:"to_agent_id,omitempty"`
	Type        MessageType    `json:"type"`
	Content     string         `json:"content"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}
