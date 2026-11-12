package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/tools"
)

func (s *Server) checkToolsConfigured(w http.ResponseWriter, r *http.Request) bool {
	if s.tools == nil {
		writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "tools service not configured")
		return false
	}
	return true
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request) {
	if !s.checkToolsConfigured(w, r) {
		return
	}

	allStr := r.URL.Query().Get("all")
	category := r.URL.Query().Get("category")
	includeDisabled := (allStr == "true" || allStr == "1")

	allTools := s.tools.Registry().ListTools()
	var filtered []tools.ToolDefinition

	for _, t := range allTools {
		if !includeDisabled && !t.Enabled {
			continue
		}
		if category != "" && !strings.EqualFold(t.Category, category) {
			continue
		}
		filtered = append(filtered, t)
	}

	if filtered == nil {
		filtered = []tools.ToolDefinition{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"tools": filtered})
}

func (s *Server) handleToolGet(w http.ResponseWriter, r *http.Request) {
	if !s.checkToolsConfigured(w, r) {
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "bad_request", "tool id required")
		return
	}

	h, ok := s.tools.Registry().GetTool(id)
	if !ok {
		writeError(w, r, http.StatusNotFound, "tool_not_found", "tool not found")
		return
	}

	writeJSON(w, http.StatusOK, h.Definition())
}

func (s *Server) handleToolPatch(w http.ResponseWriter, r *http.Request) {
	if !s.checkToolsConfigured(w, r) {
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, r, http.StatusBadRequest, "bad_request", "tool id required")
		return
	}

	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request", "invalid JSON payload")
		return
	}
	if body.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "bad_request", "missing required 'enabled' boolean")
		return
	}

	if err := s.tools.Registry().SetEnabled(id, *body.Enabled); err != nil {
		if errors.Is(err, tools.ErrToolNotFound) {
			writeError(w, r, http.StatusNotFound, "tool_not_found", "tool not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	if s.tools.Store() != nil {
		_ = s.tools.Store().SetToolEnabled(r.Context(), id, *body.Enabled)
	}

	h, _ := s.tools.Registry().GetTool(id)
	writeJSON(w, http.StatusOK, h.Definition())
}

type toolExecuteRequest struct {
	Tool           string          `json:"tool"`
	ToolName       string          `json:"tool_name"`
	Arguments      json.RawMessage `json:"arguments"`
	Capabilities   []string        `json:"capabilities"`
	Permissions    []string        `json:"permissions"`
	MaxOutputBytes int64           `json:"max_output_bytes"`
	TimeoutMS      int64           `json:"timeout_ms"`
	RunID          string          `json:"run_id"`
	TaskID         string          `json:"task_id"`
}

func (s *Server) handleToolExecute(w http.ResponseWriter, r *http.Request) {
	if !s.checkToolsConfigured(w, r) {
		return
	}

	var req toolExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	toolName := req.Tool
	if toolName == "" {
		toolName = req.ToolName
	}
	if toolName == "" {
		writeError(w, r, http.StatusBadRequest, "bad_request", "missing 'tool' name")
		return
	}

	principal := PrincipalFrom(r.Context())
	grantedCaps := req.Capabilities
	grantedPerms := req.Permissions

	// Admin auto-grant if capabilities or permissions are omitted
	if principal != nil && principal.Allows(security.ScopeAdmin) {
		if len(grantedCaps) == 0 {
			grantedCaps = []string{"*"}
		}
		if len(grantedPerms) == 0 {
			grantedPerms = []string{"admin"}
		}
	} else if principal != nil && len(grantedPerms) == 0 {
		grantedPerms = principal.Scopes
	}

	actor := "api"
	if principal != nil && principal.TokenID != "" {
		actor = principal.TokenID
	}

	var timeout time.Duration
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}

	execCtx := tools.ExecutionContext{
		RunID:               req.RunID,
		TaskID:              req.TaskID,
		GrantedCapabilities: grantedCaps,
		GrantedPermissions:  grantedPerms,
		MaxOutputBytes:      req.MaxOutputBytes,
		Timeout:             timeout,
		Actor:               actor,
	}

	res, err := s.tools.Executor().Execute(r.Context(), execCtx, toolName, req.Arguments)
	if err != nil {
		if errors.Is(err, tools.ErrToolNotFound) {
			writeError(w, r, http.StatusNotFound, "tool_not_found", err.Error())
			return
		}
		if errors.Is(err, tools.ErrToolDisabled) {
			writeError(w, r, http.StatusForbidden, "tool_disabled", err.Error())
			return
		}
		if errors.Is(err, tools.ErrCapabilityDenied) {
			writeError(w, r, http.StatusForbidden, "capability_denied", err.Error())
			return
		}
		if errors.Is(err, tools.ErrPermissionDenied) {
			writeError(w, r, http.StatusForbidden, "permission_denied", err.Error())
			return
		}
		if errors.Is(err, tools.ErrSchemaValidation) {
			writeError(w, r, http.StatusBadRequest, "schema_validation_failed", err.Error())
			return
		}
		if errors.Is(err, tools.ErrExecutionTimeout) {
			writeError(w, r, http.StatusGatewayTimeout, "execution_timeout", err.Error())
			return
		}
		writeError(w, r, http.StatusInternalServerError, "execution_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tool_name":   res.ToolName,
		"output":      res.Output,
		"data":        res.Data,
		"truncated":   res.Truncated,
		"duration_ms": res.Duration.Milliseconds(),
	})
}
