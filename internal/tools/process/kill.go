package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/lovecode/lovecode/internal/tools"
)

type KillTool struct {
	inspector ProcessInspector
}

func NewKillTool(inspector ProcessInspector) *KillTool {
	if inspector == nil {
		inspector = NewLinuxProcInspector()
	}
	return &KillTool{inspector: inspector}
}

func (t *KillTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		ID:          "process_kill",
		Name:        "process_kill",
		Description: "Send a termination or control signal to a host process with strict immunity checks.",
		Category:    tools.CategoryProcess,
		RiskLevel:   tools.RiskHigh,
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
		RequiredCapabilities: []string{"process:kill"},
		RequiredPermissions:  []string{"execute"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type killArgs struct {
	PID    int    `json:"pid"`
	Signal string `json:"signal"`
}

type KillResultData struct {
	PID    int    `json:"pid"`
	Signal string `json:"signal"`
	Status string `json:"status"`
}

func (t *KillTool) Execute(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
	var a killArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("%w: %v", tools.ErrSchemaValidation, err)
	}

	sig, err := ParseSignal(a.Signal)
	if err != nil {
		return nil, err
	}

	// Validate target PID and immunity
	pInfo, err := ValidateKillTarget(a.PID, t.inspector)
	if err != nil {
		return nil, err
	}

	// Dispatch signal to process
	if err := syscall.Kill(a.PID, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil, fmt.Errorf("%w: process with PID %d not found", tools.ErrToolNotFound, a.PID)
		}
		if errors.Is(err, syscall.EPERM) {
			return nil, fmt.Errorf("%w: operation not permitted for PID %d", tools.ErrPermissionDenied, a.PID)
		}
		return nil, fmt.Errorf("failed to signal process %d: %w", a.PID, err)
	}

	procName := "process"
	if pInfo != nil && pInfo.Name != "" {
		procName = pInfo.Name
	}

	output := fmt.Sprintf("sent signal %s to %s (PID %d)", sig.String(), procName, a.PID)
	return &tools.ExecutionResult{
		ToolName: "process_kill",
		Output:   output,
		Data: KillResultData{
			PID:    a.PID,
			Signal: sig.String(),
			Status: "signaled",
		},
	}, nil
}
