package process

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/tools"
)

type ListTool struct {
	inspector ProcessInspector
}

func NewListTool(inspector ProcessInspector) *ListTool {
	if inspector == nil {
		inspector = NewLinuxProcInspector()
	}
	return &ListTool{inspector: inspector}
}

func (t *ListTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		ID:          "process_list",
		Name:        "process_list",
		Description: "List running host processes with resource usage and protection status.",
		Category:    tools.CategoryProcess,
		RiskLevel:   tools.RiskLow,
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
		RequiredCapabilities: []string{"process:list"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
}

type listArgs struct {
	Filter string `json:"filter"`
	Limit  int    `json:"limit"`
}

func (t *ListTool) Execute(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
	var a listArgs
	if len(args) > 0 && string(args) != "{}" {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", tools.ErrSchemaValidation, err)
		}
	}

	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	all, err := t.inspector.ListProcesses()
	if err != nil {
		return nil, fmt.Errorf("list processes failed: %w", err)
	}

	var filtered []ProcessInfo
	filterLower := strings.ToLower(strings.TrimSpace(a.Filter))

	for _, p := range all {
		if filterLower != "" {
			nameLower := strings.ToLower(p.Name)
			cmdLower := strings.ToLower(p.CommandLine)
			if !strings.Contains(nameLower, filterLower) && !strings.Contains(cmdLower, filterLower) {
				continue
			}
		}
		filtered = append(filtered, p)
		if len(filtered) >= limit {
			break
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-7s %-7s %-6s %-10s %-10s %s\n", "PID", "PPID", "STATE", "RSS (MB)", "PROTECTED", "COMMAND"))
	for _, p := range filtered {
		rssMB := float64(p.RSSBytes) / (1024 * 1024)
		protStr := "no"
		if p.IsProtected {
			protStr = "YES"
		}
		cmdTrunc := p.CommandLine
		if len(cmdTrunc) > 80 {
			cmdTrunc = cmdTrunc[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-7d %-7d %-6s %-10.2f %-10s %s\n", p.PID, p.PPID, p.State, rssMB, protStr, cmdTrunc))
	}

	return &tools.ExecutionResult{
		ToolName: "process_list",
		Output:   sb.String(),
		Data:     filtered,
	}, nil
}
