// Package tools defines the tool execution architecture, registry, and security enforcement.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lovecode/lovecode/internal/audit"
)

// RiskLevel classifies the potential operational and security impact of a tool.
type RiskLevel string

const (
	RiskNone     RiskLevel = "none"
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Standard tool categories
const (
	CategorySystem     = "system"
	CategoryFilesystem = "filesystem"
	CategoryTerminal   = "terminal"
	CategoryGit        = "git"
	CategoryProcess    = "process"
	CategoryNetwork    = "network"
	CategoryMCP        = "mcp"
	CategoryDevice     = "device"
	CategoryCustom     = "custom"
)

// ToolDefinition describes metadata and constraints for a tool.
type ToolDefinition struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Category             string          `json:"category"`
	RiskLevel            RiskLevel       `json:"risk_level"`
	ParametersSchema     json.RawMessage `json:"parameters_schema"`
	RequiredCapabilities []string        `json:"required_capabilities"`
	RequiredPermissions  []string        `json:"required_permissions"`
	Enabled              bool            `json:"enabled"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

// ExecutionContext carries security context and caller identity into a tool invocation.
type ExecutionContext struct {
	RunID               string
	TaskID              string
	AgentID             string
	TeamID              string
	GrantedCapabilities []string
	GrantedPermissions  []string
	MaxOutputBytes      int64
	Timeout             time.Duration
	Actor               string
}

// ExecutionResult encapsulates the outcome of a tool execution.
type ExecutionResult struct {
	ToolName  string        `json:"tool_name"`
	Output    string        `json:"output"`
	Data      any           `json:"data,omitempty"`
	Truncated bool          `json:"truncated"`
	Duration  time.Duration `json:"duration"`
}

// ToolHandler defines the interface implemented by all tools.
type ToolHandler interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error)
}

// ToolExecutor executes registered tools under strict security policy.
type ToolExecutor interface {
	Execute(ctx context.Context, execCtx ExecutionContext, toolName string, arguments json.RawMessage) (*ExecutionResult, error)
	ValidateCapabilities(toolName string, grantedCaps []string) error
	ValidatePermissions(toolName string, grantedPerms []string) error
}

// AuditRecorder defines the audit logging contract (satisfied by *audit.Log).
type AuditRecorder interface {
	Append(ctx context.Context, e audit.Event) error
}

// Sentinel errors for tool operations
var (
	ErrToolNotFound       = errors.New("tool not found")
	ErrToolDisabled       = errors.New("tool is disabled")
	ErrCapabilityDenied   = errors.New("capability denied")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrSchemaValidation   = errors.New("schema validation failed")
	ErrExecutionTimeout   = errors.New("tool execution timed out")
	ErrExecutionCancelled = errors.New("tool execution cancelled")
	ErrConcurrencyLimit   = errors.New("tool execution concurrency limit reached")
)

// MCPServerRecord models persisted MCP server configuration.
type MCPServerRecord struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	TransportType string            `json:"transport_type"` // stdio, http, sse
	URI           string            `json:"uri"`
	Command       string            `json:"command"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	Enabled       bool              `json:"enabled"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// DeviceNodeRecord models persisted Device Node registration.
type DeviceNodeRecord struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"` // pending, paired, revoked, offline
	PairingCode   string     `json:"pairing_code"`
	PublicKey     string     `json:"public_key"`
	AuthTokenHash string     `json:"auth_token_hash"`
	Capabilities  []string   `json:"capabilities"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}
