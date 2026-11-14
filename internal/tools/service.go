package tools

import (
	"context"
	"log/slog"

	"github.com/lovecode/lovecode/internal/core"
	"github.com/lovecode/lovecode/internal/tools/git"
	"github.com/lovecode/lovecode/internal/tools/terminal"
)

// Service integrates tools registry, executor, and persistence into the LoveCode modular runtime.
type Service struct {
	store    *Store
	registry *ToolRegistry
	executor *UnifiedToolExecutor
	log      *slog.Logger
}

// NewService constructs a Tools Service instance.
func NewService(store *Store, auditLog AuditRecorder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	reg := NewToolRegistry()
	exec := NewUnifiedToolExecutor(reg, auditLog)

	s := &Service{
		store:    store,
		registry: reg,
		executor: exec,
		log:      log,
	}

	s.registerBuiltinTools()

	return s
}

// registerBuiltinTools registers all 15 host system tools across the 6 Milestone 2 feature domains.
func (s *Service) registerBuiltinTools() {
	// 1. Filesystem Tools (Feature 8)
	_ = s.registry.RegisterTool(&FSReadTool{})
	_ = s.registry.RegisterTool(&FSWriteTool{})
	_ = s.registry.RegisterTool(&FSListTool{})
	_ = s.registry.RegisterTool(&FSStatTool{})
	_ = s.registry.RegisterTool(&FSDeleteTool{})

	// 2. Structured Terminal Tool (Feature 9)
	termCfg := terminal.DefaultConfig()
	termCfg.Enabled = true // Enabled for service runtime; configurable/overridable via store
	_ = s.registry.RegisterTool(NewTerminalTool(termCfg))

	// 3. Scoped Git Tools (Feature 10)
	gitCfg := git.DefaultConfig()
	_ = s.registry.RegisterTool(NewGitStatusTool(gitCfg))
	_ = s.registry.RegisterTool(NewGitDiffTool(gitCfg))
	_ = s.registry.RegisterTool(NewGitLogTool(gitCfg))
	_ = s.registry.RegisterTool(NewGitBranchTool(gitCfg))
	_ = s.registry.RegisterTool(NewGitCommitTool(gitCfg))

	// 4. Read-Only System Info Tool (Feature 11)
	_ = s.registry.RegisterTool(NewSysinfoTool())

	// 5. Controlled Process Tools (Feature 12)
	_ = s.registry.RegisterTool(NewProcessListTool(nil))
	_ = s.registry.RegisterTool(NewProcessKillTool(nil))

	// 6. Restricted Network Tool (Feature 13)
	_ = s.registry.RegisterTool(NewHTTPRequestTool())
}

// Name identifies the module in core.Runtime.
func (s *Service) Name() string {
	return "tools"
}

// Start loads persisted tool configurations on startup.
func (s *Service) Start(ctx context.Context) error {
	s.log.Info("starting tools service")
	if s.store != nil {
		if err := s.registry.LoadFromStore(ctx, s.store); err != nil {
			s.log.Warn("could not load tool overrides from store", "err", err)
		}
	}
	s.log.Info("tools service ready", "registered_tools", s.registry.Count())
	return nil
}

// Stop cleanly releases resources on shutdown.
func (s *Service) Stop(ctx context.Context) error {
	s.log.Info("stopping tools service")
	return nil
}

// Health reports tools service health.
func (s *Service) Health(ctx context.Context) core.Health {
	return core.Health{
		Status: core.StatusOK,
		Detail: "tools engine operational",
	}
}

// Registry returns the centralized ToolRegistry.
func (s *Service) Registry() *ToolRegistry {
	return s.registry
}

// Executor returns the UnifiedToolExecutor.
func (s *Service) Executor() ToolExecutor {
	return s.executor
}

// Store returns the underlying persistence Store.
func (s *Service) Store() *Store {
	return s.store
}
