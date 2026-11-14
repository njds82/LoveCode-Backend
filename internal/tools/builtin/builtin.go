package builtin

import (
	"github.com/lovecode/lovecode/internal/tools"
	"github.com/lovecode/lovecode/internal/tools/filesystem"
	"github.com/lovecode/lovecode/internal/tools/git"
	"github.com/lovecode/lovecode/internal/tools/process"
	"github.com/lovecode/lovecode/internal/tools/sysinfo"
	"github.com/lovecode/lovecode/internal/tools/terminal"
)

// RegisterAll registers all built-in host system tools into the registry.
func RegisterAll(reg *tools.ToolRegistry) error {
	if reg == nil {
		return nil
	}

	// 1. Filesystem Tools (Feature 8)
	fsCfg := filesystem.DefaultConfig()
	_ = reg.RegisterTool(&tools.FSReadTool{AllowedRoots: fsCfg.AllowedRoots})
	_ = reg.RegisterTool(&tools.FSWriteTool{AllowedRoots: fsCfg.AllowedRoots})
	_ = reg.RegisterTool(&tools.FSListTool{AllowedRoots: fsCfg.AllowedRoots})
	_ = reg.RegisterTool(&tools.FSStatTool{AllowedRoots: fsCfg.AllowedRoots})
	_ = reg.RegisterTool(&tools.FSDeleteTool{AllowedRoots: fsCfg.AllowedRoots})
	_ = reg.RegisterTool(&tools.FSMkdirTool{AllowedRoots: fsCfg.AllowedRoots})
	_ = reg.RegisterTool(&tools.FSSearchTool{AllowedRoots: fsCfg.AllowedRoots})

	// 2. Structured Terminal Tool (Feature 9)
	termCfg := terminal.DefaultConfig()
	termCfg.Enabled = true // Enabled for service runtime; overrideable via Store / PATCH API
	_ = reg.RegisterTool(tools.NewTerminalTool(termCfg))

	// 3. Scoped Git Tools (Feature 10)
	gitCfg := git.DefaultConfig()
	_ = reg.RegisterTool(tools.NewGitStatusTool(gitCfg))
	_ = reg.RegisterTool(tools.NewGitDiffTool(gitCfg))
	_ = reg.RegisterTool(tools.NewGitLogTool(gitCfg))
	_ = reg.RegisterTool(tools.NewGitBranchTool(gitCfg))
	_ = reg.RegisterTool(tools.NewGitCommitTool(gitCfg))

	// 4. Read-Only System Info Tool (Feature 11)
	_ = reg.RegisterTool(sysinfo.NewTool())

	// 5. Controlled Process Tools (Feature 12)
	procInspector := process.NewLinuxProcInspector()
	_ = reg.RegisterTool(process.NewListTool(procInspector))
	_ = reg.RegisterTool(process.NewKillTool(procInspector))

	// 6. Restricted Network Tool (Feature 13)
	_ = reg.RegisterTool(tools.NewHTTPRequestTool())

	return nil
}
