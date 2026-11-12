package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func dummyHandler(name, category string, enabled bool) ToolHandler {
	return WrapHandler(ToolDefinition{
		ID:          "tool-" + name,
		Name:        name,
		Description: "dummy tool " + name,
		Category:    category,
		RiskLevel:   RiskLow,
		Enabled:     enabled,
	}, func(ctx context.Context, execCtx ExecutionContext, args json.RawMessage) (*ExecutionResult, error) {
		return &ExecutionResult{
			ToolName: name,
			Output:   "executed " + name,
		}, nil
	})
}

func TestToolRegistry(t *testing.T) {
	reg := NewToolRegistry()

	t.Run("Register and Resolve", func(t *testing.T) {
		h := dummyHandler("echo_tool", CategorySystem, true)
		if err := reg.RegisterTool(h); err != nil {
			t.Fatalf("failed to register tool: %v", err)
		}

		// Duplicate registration should fail
		if err := reg.RegisterTool(h); err == nil {
			t.Fatal("expected duplicate registration error")
		}

		resolved, err := reg.Resolve("echo_tool")
		if err != nil {
			t.Fatalf("failed to resolve tool: %v", err)
		}
		if resolved.Definition().Name != "echo_tool" {
			t.Fatalf("unexpected tool name: %s", resolved.Definition().Name)
		}

		// Case-insensitive resolution
		resolvedUpper, err := reg.Resolve("ECHO_TOOL")
		if err != nil {
			t.Fatalf("failed to resolve upper-case tool name: %v", err)
		}
		if resolvedUpper.Definition().Name != "echo_tool" {
			t.Fatalf("unexpected tool name: %s", resolvedUpper.Definition().Name)
		}
	})

	t.Run("Resolve Non-Existent Tool", func(t *testing.T) {
		_, err := reg.Resolve("non_existent")
		if !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("expected ErrToolNotFound, got %v", err)
		}
	})

	t.Run("SetEnabled and Resolve Disabled Tool", func(t *testing.T) {
		h := dummyHandler("disabled_tool", CategorySystem, true)
		_ = reg.RegisterTool(h)

		if err := reg.SetEnabled("disabled_tool", false); err != nil {
			t.Fatalf("failed to disable tool: %v", err)
		}

		// GetTool still works for inspection
		got, exists := reg.GetTool("disabled_tool")
		if !exists || got.Definition().Enabled {
			t.Fatal("expected GetTool to succeed and report enabled=false")
		}

		// Resolve must return ErrToolDisabled
		_, err := reg.Resolve("disabled_tool")
		if !errors.Is(err, ErrToolDisabled) {
			t.Fatalf("expected ErrToolDisabled, got %v", err)
		}

		// Re-enable
		if err := reg.SetEnabled("disabled_tool", true); err != nil {
			t.Fatalf("failed to re-enable tool: %v", err)
		}
		_, err = reg.Resolve("disabled_tool")
		if err != nil {
			t.Fatalf("expected Resolve to succeed after re-enable, got %v", err)
		}
	})

	t.Run("ListTools and ListByCategory", func(t *testing.T) {
		r := NewToolRegistry()
		_ = r.RegisterTool(dummyHandler("beta", CategoryFilesystem, true))
		_ = r.RegisterTool(dummyHandler("alpha", CategorySystem, true))
		_ = r.RegisterTool(dummyHandler("gamma", CategorySystem, true))

		all := r.ListTools()
		if len(all) != 3 {
			t.Fatalf("expected 3 tools, got %d", len(all))
		}
		// Must be sorted by name
		if all[0].Name != "alpha" || all[1].Name != "beta" || all[2].Name != "gamma" {
			t.Fatalf("unexpected sort order: %v, %v, %v", all[0].Name, all[1].Name, all[2].Name)
		}

		sysTools := r.ListByCategory(CategorySystem)
		if len(sysTools) != 2 {
			t.Fatalf("expected 2 system tools, got %d", len(sysTools))
		}
		if sysTools[0].Name != "alpha" || sysTools[1].Name != "gamma" {
			t.Fatalf("unexpected system tools: %v, %v", sysTools[0].Name, sysTools[1].Name)
		}
	})

	t.Run("UnregisterTool", func(t *testing.T) {
		r := NewToolRegistry()
		_ = r.RegisterTool(dummyHandler("temp", CategorySystem, true))
		if r.Count() != 1 {
			t.Fatalf("expected count 1, got %d", r.Count())
		}

		if err := r.UnregisterTool("temp"); err != nil {
			t.Fatalf("failed to unregister tool: %v", err)
		}
		if r.Count() != 0 {
			t.Fatalf("expected count 0, got %d", r.Count())
		}
		if err := r.UnregisterTool("temp"); !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("expected ErrToolNotFound on double unregister, got %v", err)
		}
	})

	t.Run("Concurrent Safety", func(t *testing.T) {
		r := NewToolRegistry()
		var wg sync.WaitGroup
		workers := 8
		opsPerWorker := 50

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < opsPerWorker; j++ {
					toolName := fmt.Sprintf("tool_%d_%d", workerID, j)
					_ = r.RegisterTool(dummyHandler(toolName, CategoryCustom, true))
					_, _ = r.Resolve(toolName)
					_ = r.SetEnabled(toolName, false)
					_ = r.ListTools()
				}
			}(i)
		}
		wg.Wait()

		if r.Count() != workers*opsPerWorker {
			t.Fatalf("expected %d tools, got %d", workers*opsPerWorker, r.Count())
		}
	})
}
