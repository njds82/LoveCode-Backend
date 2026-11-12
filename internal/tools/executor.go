package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/security"
)

// UnifiedToolExecutor coordinates tool resolution, deny-by-default security checks,
// bounded concurrency, timeouts, output truncation, secret redaction, and audit logging.
type UnifiedToolExecutor struct {
	registry         *ToolRegistry
	audit            AuditRecorder
	redactor         *security.Redactor
	semaphore        chan struct{}
	defaultTimeout   time.Duration
	defaultMaxOutput int64
}

// NewUnifiedToolExecutor constructs an executor with a 16-worker concurrency pool.
func NewUnifiedToolExecutor(registry *ToolRegistry, auditLog AuditRecorder) *UnifiedToolExecutor {
	return &UnifiedToolExecutor{
		registry:         registry,
		audit:            auditLog,
		redactor:         security.NewRedactor(),
		semaphore:        make(chan struct{}, 16),
		defaultTimeout:   60 * time.Second,
		defaultMaxOutput: 1024 * 1024, // 1MB default
	}
}

// SetLimits allows customizing default timeout and maximum output bytes.
func (e *UnifiedToolExecutor) SetLimits(defaultTimeout time.Duration, defaultMaxOutput int64) {
	if defaultTimeout > 0 {
		e.defaultTimeout = defaultTimeout
	}
	if defaultMaxOutput > 0 {
		e.defaultMaxOutput = defaultMaxOutput
	}
}

// ValidateCapabilities validates that the granted capabilities satisfy the tool's requirements.
func (e *UnifiedToolExecutor) ValidateCapabilities(toolName string, grantedCaps []string) error {
	h, ok := e.registry.GetTool(toolName)
	if !ok {
		return ErrToolNotFound
	}
	def := h.Definition()
	if len(def.RequiredCapabilities) == 0 {
		return nil
	}

	if len(grantedCaps) == 0 {
		return fmt.Errorf("%w: tool %q requires %v", ErrCapabilityDenied, toolName, def.RequiredCapabilities)
	}

	for _, req := range def.RequiredCapabilities {
		if !matchScoped(req, grantedCaps) {
			return fmt.Errorf("%w: missing capability %q for tool %q", ErrCapabilityDenied, req, toolName)
		}
	}
	return nil
}

// ValidatePermissions validates that the granted permissions satisfy the tool's requirements.
func (e *UnifiedToolExecutor) ValidatePermissions(toolName string, grantedPerms []string) error {
	h, ok := e.registry.GetTool(toolName)
	if !ok {
		return ErrToolNotFound
	}
	def := h.Definition()
	if len(def.RequiredPermissions) == 0 {
		return nil
	}

	if len(grantedPerms) == 0 {
		return fmt.Errorf("%w: tool %q requires %v", ErrPermissionDenied, toolName, def.RequiredPermissions)
	}

	for _, req := range def.RequiredPermissions {
		if !matchScoped(req, grantedPerms) {
			return fmt.Errorf("%w: missing permission %q for tool %q", ErrPermissionDenied, req, toolName)
		}
	}
	return nil
}

// Execute performs end-to-end execution of a tool with full guardrails.
func (e *UnifiedToolExecutor) Execute(ctx context.Context, execCtx ExecutionContext, toolName string, arguments json.RawMessage) (*ExecutionResult, error) {
	// 1. Resolve tool (checks existence and enabled status)
	handler, err := e.registry.Resolve(toolName)
	if err != nil {
		e.recordAudit(ctx, execCtx, "tool.failed", toolName, map[string]any{
			"error": err.Error(),
		})
		return nil, err
	}
	def := handler.Definition()

	// 2. Deny-by-default capability check
	if err := e.ValidateCapabilities(toolName, execCtx.GrantedCapabilities); err != nil {
		e.recordAudit(ctx, execCtx, "tool.denied", toolName, map[string]any{
			"reason": "capability_denied",
			"error":  err.Error(),
		})
		return nil, err
	}

	// 3. Deny-by-default permission check
	if err := e.ValidatePermissions(toolName, execCtx.GrantedPermissions); err != nil {
		e.recordAudit(ctx, execCtx, "tool.denied", toolName, map[string]any{
			"reason": "permission_denied",
			"error":  err.Error(),
		})
		return nil, err
	}

	// 4. Schema validation
	if err := ValidateJSONSchema(def.ParametersSchema, arguments); err != nil {
		e.recordAudit(ctx, execCtx, "tool.failed", toolName, map[string]any{
			"reason": "schema_validation_failed",
			"error":  err.Error(),
		})
		return nil, err
	}

	// 5. Bounded concurrency (semaphore acquire)
	select {
	case e.semaphore <- struct{}{}:
		defer func() { <-e.semaphore }()
	case <-ctx.Done():
		e.recordAudit(ctx, execCtx, "tool.failed", toolName, map[string]any{
			"reason": "cancelled_before_execution",
			"error":  ctx.Err().Error(),
		})
		return nil, ctx.Err()
	}

	// 6. Timeout configuration
	timeout := execCtx.Timeout
	if timeout <= 0 {
		timeout = e.defaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 7. Output limit configuration
	maxOutput := execCtx.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = e.defaultMaxOutput
	}

	// 8. Execute tool handler
	start := time.Now()
	res, execErr := handler.Execute(callCtx, execCtx, arguments)
	duration := time.Since(start)

	if execErr != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) || errors.Is(execErr, context.DeadlineExceeded) {
			e.recordAudit(ctx, execCtx, "tool.failed", toolName, map[string]any{
				"error":       "execution timed out",
				"duration_ms": duration.Milliseconds(),
			})
			return nil, ErrExecutionTimeout
		}
		if errors.Is(callCtx.Err(), context.Canceled) || errors.Is(execErr, context.Canceled) {
			e.recordAudit(ctx, execCtx, "tool.failed", toolName, map[string]any{
				"error":       "execution cancelled",
				"duration_ms": duration.Milliseconds(),
			})
			return nil, ErrExecutionCancelled
		}
		e.recordAudit(ctx, execCtx, "tool.failed", toolName, map[string]any{
			"error":       execErr.Error(),
			"duration_ms": duration.Milliseconds(),
		})
		return nil, execErr
	}

	if res == nil {
		res = &ExecutionResult{ToolName: toolName}
	}
	res.ToolName = toolName
	res.Duration = duration

	// 9. Enforce output truncation
	if maxOutput > 0 && int64(len(res.Output)) > maxOutput {
		res.Output = res.Output[:maxOutput]
		res.Truncated = true
	}

	// 10. Automatic secret redaction
	res.Output = e.redactor.RedactString(res.Output)
	if res.Data != nil {
		res.Data = e.redactor.RedactValue(res.Data)
	}

	// 11. Tamper-evident audit logging (operational metadata ONLY)
	e.recordAudit(ctx, execCtx, "tool.executed", toolName, map[string]any{
		"duration_ms": duration.Milliseconds(),
		"truncated":   res.Truncated,
		"output_len":  len(res.Output),
	})

	return res, nil
}

func (e *UnifiedToolExecutor) recordAudit(ctx context.Context, execCtx ExecutionContext, action, toolName string, detail map[string]any) {
	if e.audit == nil {
		return
	}
	actor := execCtx.Actor
	if actor == "" {
		actor = execCtx.AgentID
	}
	if actor == "" {
		actor = "system"
	}
	corrID := execCtx.RunID
	if corrID == "" {
		corrID = execCtx.TaskID
	}
	if detail == nil {
		detail = make(map[string]any)
	}
	if execCtx.RunID != "" {
		detail["run_id"] = execCtx.RunID
	}
	if execCtx.TaskID != "" {
		detail["task_id"] = execCtx.TaskID
	}
	if execCtx.AgentID != "" {
		detail["agent_id"] = execCtx.AgentID
	}

	_ = e.audit.Append(ctx, audit.Event{
		Actor:         actor,
		Action:        action,
		Target:        toolName,
		CorrelationID: corrID,
		Detail:        detail,
	})
}

func matchScoped(required string, granted []string) bool {
	reqNorm := strings.ToLower(required)
	for _, g := range granted {
		gNorm := strings.ToLower(g)
		if gNorm == "*" || gNorm == "admin" {
			return true
		}
		if gNorm == reqNorm {
			return true
		}
		if strings.HasSuffix(gNorm, ":*") {
			prefix := strings.TrimSuffix(gNorm, "*")
			if strings.HasPrefix(reqNorm, prefix) {
				return true
			}
		}
		if strings.HasSuffix(gNorm, "*") {
			prefix := strings.TrimSuffix(gNorm, "*")
			if strings.HasPrefix(reqNorm, prefix) {
				return true
			}
		}
	}
	return false
}
