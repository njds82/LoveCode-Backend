package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/tools/terminal"
)

// TerminalTool exposes structured command execution with allowlist and process group isolation.
type TerminalTool struct {
	runner *terminal.Runner
	cfg    terminal.Config
}

func NewTerminalTool(cfg terminal.Config) *TerminalTool {
	return &TerminalTool{
		runner: terminal.NewRunner(cfg),
		cfg:    cfg,
	}
}

func (t *TerminalTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "terminal_exec",
		Name:        "terminal_exec",
		Description: "Execute an allowlisted system command in an isolated process group.",
		Category:    CategoryTerminal,
		RiskLevel:   RiskHigh,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"executable": {"type": "string", "description": "Binary name to execute (e.g. git, echo, ls)"},
				"args": {"type": "array", "items": {"type": "string"}, "description": "Command arguments"},
				"working_dir": {"type": "string", "description": "Working directory path"},
				"env": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Environment variables"}
			},
			"required": ["executable"]
		}`),
		RequiredCapabilities: []string{"tools:terminal"},
		RequiredPermissions:  []string{"write"},
		Enabled:              t.cfg.Enabled,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type terminalExecArgs struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	WorkingDir string            `json:"working_dir"`
	Env        map[string]string `json:"env"`
}

func (t *TerminalTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a terminalExecArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	combined, data, err := t.runner.Run(ctx, a.Executable, a.Args, a.WorkingDir, a.Env)
	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		ToolName: "terminal_exec",
		Output:   combined,
		Data:     data,
	}, nil
}
