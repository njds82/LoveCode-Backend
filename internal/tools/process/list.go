package process

import (
	"fmt"
	"strings"

	"github.com/lovecode/lovecode/internal/security"
)

// List filters and formats processes from inspector.
func List(inspector ProcessInspector, filter string, limit int) (string, []ProcessInfo, error) {
	if inspector == nil {
		inspector = NewLinuxProcInspector()
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	all, err := inspector.ListProcesses()
	if err != nil {
		return "", nil, fmt.Errorf("list processes failed: %w", err)
	}

	var filtered []ProcessInfo
	filterLower := strings.ToLower(strings.TrimSpace(filter))

	for _, p := range all {
		// Ensure secret redaction in command line
		p.CommandLine = security.RedactString(p.CommandLine)

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
			protStr = "yes"
		}
		cmd := p.CommandLine
		if len(cmd) > 80 {
			cmd = cmd[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("%-7d %-7d %-6s %-10.1f %-10s %s\n", p.PID, p.PPID, p.State, rssMB, protStr, cmd))
	}

	return sb.String(), filtered, nil
}
