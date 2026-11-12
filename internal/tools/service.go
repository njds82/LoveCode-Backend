package tools

import (
	"context"
	"log/slog"

	"github.com/lovecode/lovecode/internal/core"
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

	return &Service{
		store:    store,
		registry: reg,
		executor: exec,
		log:      log,
	}
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
