package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/tools/process"
)

// --- process_list ---

type ProcessListTool struct {
	inspector process.ProcessInspector
}

func NewProcessListTool(inspector process.ProcessInspector) *ProcessListTool {
	if inspector == nil {
		inspector = process.NewLinuxProcInspector()
	}
	return &ProcessListTool{inspector: inspector}
}

func (t *ProcessListTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "process_list",
		Name:        "process_list",
		Description: "List running host processes with resource usage and protection status.",
		Category:    CategoryProcess,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"filter": {
					"type": "string",
					"description": "Optional substring filter on process name or command line"
				},
				"limit": {
					"type": "integer",
					"description": "Maximum number of processes to return (default 50, max 200)",
					"minimum": 1,
					"maximum": 200
				}
			}
		}`),
		RequiredCapabilities: []string{"tools:process"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type processListArgs struct {
	Filter string `json:"filter"`
	Limit  int    `json:"limit"`
}

func (t *ProcessListTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a processListArgs
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
	}

	out, list, err := process.List(t.inspector, a.Filter, a.Limit)
	if err != nil {
		return nil, err
	}

	return &ExecutionResult{
		ToolName: "process_list",
		Output:   out,
		Data:     list,
	}, nil
}

// --- process_kill ---

type ProcessKillTool struct {
	inspector process.ProcessInspector
}

func NewProcessKillTool(inspector process.ProcessInspector) *ProcessKillTool {
	if inspector == nil {
		inspector = process.NewLinuxProcInspector()
	}
	return &ProcessKillTool{inspector: inspector}
}

func (t *ProcessKillTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "process_kill",
		Name:        "process_kill",
		Description: "Send a termination or control signal to a host process with strict immunity checks.",
		Category:    CategoryProcess,
		RiskLevel:   RiskHigh,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"pid": {
					"type": "integer",
					"description": "Process ID to signal"
				},
				"signal": {
					"type": "string",
					"description": "Signal to send: SIGTERM (default), SIGKILL, SIGINT, SIGHUP, SIGQUIT",
					"enum": ["SIGTERM", "SIGKILL", "SIGINT", "SIGHUP", "SIGQUIT"],
					"default": "SIGTERM"
				}
			},
			"required": ["pid"]
		}`),
		RequiredCapabilities: []string{"tools:process:kill"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type processKillArgs struct {
	PID    int    `json:"pid"`
	Signal string `json:"signal"`
}

func (t *ProcessKillTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a processKillArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	out, data, err := process.Kill(t.inspector, a.PID, a.Signal)
	if err != nil {
		if errors.Is(err, process.ErrPermissionDenied) {
			return nil, fmt.Errorf("%w: %v", ErrPermissionDenied, err)
		}
		if errors.Is(err, process.ErrSchemaValidation) {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
		if errors.Is(err, process.ErrToolNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrToolNotFound, err)
		}
		return nil, err
	}

	return &ExecutionResult{
		ToolName: "process_kill",
		Output:   out,
		Data:     data,
	}, nil
}
