package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/tools/sysinfo"
)

// SysinfoTool exposes read-only host telemetry without credential leaks.
type SysinfoTool struct {
	collector *sysinfo.Collector
}

func NewSysinfoTool() *SysinfoTool {
	return &SysinfoTool{collector: sysinfo.NewCollector()}
}

func (t *SysinfoTool) Definition() ToolDefinition {
	return ToolDefinition{
		ID:          "sysinfo",
		Name:        "sysinfo",
		Description: "Inspect host operating system, hardware resources, memory, and disk usage.",
		Category:    CategorySystem,
		RiskLevel:   RiskLow,
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"mount_path": {
					"type": "string",
					"description": "Path to mount point for disk usage inspection (default '/')",
					"default": "/"
				}
			}
		}`),
		RequiredCapabilities: []string{"tools:sysinfo"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type sysinfoArgs struct {
	MountPath string `json:"mount_path"`
}

func (t *SysinfoTool) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	var a sysinfoArgs
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
		}
	}

	mount := a.MountPath
	if mount == "" {
		mount = "/"
	}

	out, info, err := t.collector.Collect(mount)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}

	return &ExecutionResult{
		ToolName: "sysinfo",
		Output:   out,
		Data:     info,
	}, nil
}
