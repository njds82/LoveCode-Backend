package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type mutableHandler struct {
	inner           ToolHandler
	overrideEnabled *bool
}

func (m *mutableHandler) Definition() ToolDefinition {
	def := m.inner.Definition()
	if m.overrideEnabled != nil {
		def.Enabled = *m.overrideEnabled
	}
	return def
}

func (m *mutableHandler) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	return m.inner.Execute(ctx, execCtx, args)
}

// ToolRegistry provides thread-safe registration, resolution, and categorization of tools.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*mutableHandler
}

// NewToolRegistry constructs a new in-memory ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*mutableHandler),
	}
}

// RegisterTool adds a tool handler to the registry.
func (r *ToolRegistry) RegisterTool(handler ToolHandler) error {
	if handler == nil {
		return errors.New("tool handler cannot be nil")
	}
	def := handler.Definition()
	if def.Name == "" {
		return errors.New("tool name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	norm := strings.ToLower(def.Name)
	if _, exists := r.tools[norm]; exists {
		return fmt.Errorf("tool %q already registered", def.Name)
	}

	r.tools[norm] = &mutableHandler{
		inner: handler,
	}
	return nil
}

// UnregisterTool removes a tool from the registry.
func (r *ToolRegistry) UnregisterTool(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	norm := strings.ToLower(name)
	if _, exists := r.tools[norm]; !exists {
		return ErrToolNotFound
	}
	delete(r.tools, norm)
	return nil
}

// GetTool retrieves a tool by name without checking its enabled status.
func (r *ToolRegistry) GetTool(name string) (ToolHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.tools[strings.ToLower(name)]
	if !ok {
		return nil, false
	}
	return h, true
}

// Resolve retrieves a tool by name and verifies that it is enabled.
func (r *ToolRegistry) Resolve(name string) (ToolHandler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.tools[strings.ToLower(name)]
	if !ok {
		return nil, ErrToolNotFound
	}
	if !h.Definition().Enabled {
		return nil, ErrToolDisabled
	}
	return h, nil
}

// ListTools returns all registered tool definitions, sorted by name.
func (r *ToolRegistry) ListTools() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]ToolDefinition, 0, len(r.tools))
	for _, h := range r.tools {
		list = append(list, h.Definition())
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// ListByCategory returns tool definitions for a specific category, sorted by name.
func (r *ToolRegistry) ListByCategory(category string) []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var list []ToolDefinition
	for _, h := range r.tools {
		def := h.Definition()
		if strings.EqualFold(def.Category, category) {
			list = append(list, def)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// SetEnabled updates the enabled state of a registered tool.
func (r *ToolRegistry) SetEnabled(name string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	h, ok := r.tools[strings.ToLower(name)]
	if !ok {
		return ErrToolNotFound
	}
	h.overrideEnabled = &enabled
	return nil
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// LoadFromStore synchronizes tool overrides from persistent storage.
func (r *ToolRegistry) LoadFromStore(ctx context.Context, store *Store) error {
	if store == nil {
		return nil
	}
	defs, err := store.ListToolDefinitions(ctx, true)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, d := range defs {
		norm := strings.ToLower(d.Name)
		if h, exists := r.tools[norm]; exists {
			enabled := d.Enabled
			h.overrideEnabled = &enabled
		}
	}
	return nil
}

type funcHandler struct {
	def ToolDefinition
	fn  func(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error)
}

func (f *funcHandler) Definition() ToolDefinition {
	return f.def
}

func (f *funcHandler) Execute(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
	return f.fn(ctx, execCtx, args)
}

// WrapHandler creates a ToolHandler from a definition and execution function.
func WrapHandler(def ToolDefinition, fn func(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error)) ToolHandler {
	if def.CreatedAt.IsZero() {
		def.CreatedAt = time.Now().UTC()
	}
	if def.UpdatedAt.IsZero() {
		def.UpdatedAt = time.Now().UTC()
	}
	return &funcHandler{
		def: def,
		fn:  fn,
	}
}
