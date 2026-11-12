package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/agents"
)

type agentPayload struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Role           string         `json:"role,omitempty"`
	SystemPrompt   string         `json:"system_prompt,omitempty"`
	Model          string         `json:"model,omitempty"`
	RoutingProfile string         `json:"routing_profile,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	TimeoutMS      int64          `json:"timeout_ms,omitempty"`
	MaxRequests    int            `json:"max_requests,omitempty"`
	AutonomyLevel  string         `json:"autonomy_level,omitempty"`
	ContextPolicy  string         `json:"context_policy,omitempty"`
	Capabilities   []string       `json:"capabilities,omitempty"`
	Permissions    []string       `json:"permissions,omitempty"`
	Enabled        *bool          `json:"enabled,omitempty"`
}

type teamPayload struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Description        string              `json:"description,omitempty"`
	Strategy           string              `json:"strategy,omitempty"`
	Members            []agents.TeamMember `json:"members,omitempty"`
	ExecutionOrder     []string            `json:"execution_order,omitempty"`
	Dependencies       map[string][]string `json:"dependencies,omitempty"`
	ContextPolicy      string              `json:"context_policy,omitempty"`
	AggregationPolicy  string              `json:"aggregation_policy,omitempty"`
	FailurePolicy      string              `json:"failure_policy,omitempty"`
	SynthesizerAgentID string              `json:"synthesizer_agent_id,omitempty"`
	MaxParallel        int                 `json:"max_parallel,omitempty"`
	TimeoutMS          int64               `json:"timeout_ms,omitempty"`
	Enabled            *bool               `json:"enabled,omitempty"`
}

type runRequestPayload struct {
	RunType        string                   `json:"run_type"` // "agent" or "team"
	AgentID        string                   `json:"agent_id,omitempty"`
	TeamID         string                   `json:"team_id,omitempty"`
	Input          string                   `json:"input"`
	TimeoutMS      int64                    `json:"timeout_ms,omitempty"`
	DynamicAgents  []agents.AgentDefinition `json:"dynamic_agents,omitempty"`
	ConfigSnapshot map[string]any           `json:"config_snapshot,omitempty"`
}

func (s *Server) checkAgentsConfigured(w http.ResponseWriter, r *http.Request) bool {
	if s.agents != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "agents_unavailable", "agent engine is not configured")
	return false
}

// Agent Handlers

func (s *Server) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	includeDisabled := r.URL.Query().Get("all") == "true"
	list := s.agents.Registry().ListAgents(includeDisabled)
	if list == nil {
		list = []agents.AgentDefinition{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAgentGet(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	a, err := s.agents.Registry().GetAgent("", id)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleAgentCreate(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	var p agentPayload
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if p.ID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "agent id must not be empty")
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	def := agents.AgentDefinition{
		ID:             p.ID,
		Name:           p.Name,
		Description:    p.Description,
		Role:           p.Role,
		SystemPrompt:   p.SystemPrompt,
		Model:          p.Model,
		RoutingProfile: p.RoutingProfile,
		Params:         p.Params,
		MaxTokens:      p.MaxTokens,
		Timeout:        time.Duration(p.TimeoutMS) * time.Millisecond,
		MaxRequests:    p.MaxRequests,
		AutonomyLevel:  agents.AutonomyLevel(p.AutonomyLevel),
		ContextPolicy:  agents.ContextPolicy(p.ContextPolicy),
		Capabilities:   p.Capabilities,
		Permissions:    p.Permissions,
		Enabled:        enabled,
	}
	if err := s.agents.Registry().RegisterAgent(r.Context(), def); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "agent.created", def.ID, map[string]any{"name": def.Name, "role": def.Role})
	writeJSON(w, http.StatusCreated, def)
}

func (s *Server) handleAgentUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	var p agentPayload
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	def := agents.AgentDefinition{
		ID:             id,
		Name:           p.Name,
		Description:    p.Description,
		Role:           p.Role,
		SystemPrompt:   p.SystemPrompt,
		Model:          p.Model,
		RoutingProfile: p.RoutingProfile,
		Params:         p.Params,
		MaxTokens:      p.MaxTokens,
		Timeout:        time.Duration(p.TimeoutMS) * time.Millisecond,
		MaxRequests:    p.MaxRequests,
		AutonomyLevel:  agents.AutonomyLevel(p.AutonomyLevel),
		ContextPolicy:  agents.ContextPolicy(p.ContextPolicy),
		Capabilities:   p.Capabilities,
		Permissions:    p.Permissions,
		Enabled:        enabled,
	}
	if err := s.agents.Registry().RegisterAgent(r.Context(), def); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "agent.updated", id, map[string]any{"name": def.Name})
	writeJSON(w, http.StatusOK, def)
}

func (s *Server) handleAgentPatch(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	var p struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if p.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "enabled field is required")
		return
	}
	if err := s.agents.Registry().SetAgentEnabled(r.Context(), id, *p.Enabled); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	action := "agent.disabled"
	if *p.Enabled {
		action = "agent.enabled"
	}
	s.recordAudit(r, action, id, map[string]any{"enabled": *p.Enabled})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": *p.Enabled})
}

func (s *Server) handleAgentDelete(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.agents.Registry().DeleteAgent(r.Context(), id); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "agent not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.recordAudit(r, "agent.deleted", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// Team Handlers

func (s *Server) handleTeamsList(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	includeDisabled := r.URL.Query().Get("all") == "true"
	list := s.agents.Registry().ListTeams(includeDisabled)
	if list == nil {
		list = []agents.TeamDefinition{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleTeamGet(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	t, err := s.agents.Registry().GetTeam(id)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "team not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTeamCreate(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	var p teamPayload
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if p.ID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "team id must not be empty")
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	t := agents.TeamDefinition{
		ID:                 p.ID,
		Name:               p.Name,
		Description:        p.Description,
		Strategy:           agents.ExecutionStrategy(p.Strategy),
		Members:            p.Members,
		ExecutionOrder:     p.ExecutionOrder,
		Dependencies:       p.Dependencies,
		ContextPolicy:      agents.ContextPolicy(p.ContextPolicy),
		AggregationPolicy:  agents.AggregationPolicy(p.AggregationPolicy),
		FailurePolicy:      agents.FailurePolicy(p.FailurePolicy),
		SynthesizerAgentID: p.SynthesizerAgentID,
		MaxParallel:        p.MaxParallel,
		Timeout:            time.Duration(p.TimeoutMS) * time.Millisecond,
		Enabled:            enabled,
	}
	if err := s.agents.Registry().RegisterTeam(r.Context(), t); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "team.created", t.ID, map[string]any{"name": t.Name, "strategy": t.Strategy})
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleTeamUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	var p teamPayload
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	t := agents.TeamDefinition{
		ID:                 id,
		Name:               p.Name,
		Description:        p.Description,
		Strategy:           agents.ExecutionStrategy(p.Strategy),
		Members:            p.Members,
		ExecutionOrder:     p.ExecutionOrder,
		Dependencies:       p.Dependencies,
		ContextPolicy:      agents.ContextPolicy(p.ContextPolicy),
		AggregationPolicy:  agents.AggregationPolicy(p.AggregationPolicy),
		FailurePolicy:      agents.FailurePolicy(p.FailurePolicy),
		SynthesizerAgentID: p.SynthesizerAgentID,
		MaxParallel:        p.MaxParallel,
		Timeout:            time.Duration(p.TimeoutMS) * time.Millisecond,
		Enabled:            enabled,
	}
	if err := s.agents.Registry().RegisterTeam(r.Context(), t); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "team.updated", id, map[string]any{"name": t.Name})
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTeamPatch(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	var p struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if p.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "enabled field is required")
		return
	}
	if err := s.agents.Registry().SetTeamEnabled(r.Context(), id, *p.Enabled); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "team not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	action := "team.disabled"
	if *p.Enabled {
		action = "team.enabled"
	}
	s.recordAudit(r, action, id, map[string]any{"enabled": *p.Enabled})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "enabled": *p.Enabled})
}

func (s *Server) handleTeamDelete(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.agents.Registry().DeleteTeam(r.Context(), id); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "team not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.recordAudit(r, "team.deleted", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// Run Handlers

func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	var p runRequestPayload
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if strings.TrimSpace(p.Input) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "input must not be empty")
		return
	}
	if p.RunType == "" {
		if p.TeamID != "" {
			p.RunType = "team"
		} else {
			p.RunType = "agent"
		}
	}

	actor := "anonymous"
	if princ := PrincipalFrom(r.Context()); princ != nil {
		actor = princ.Name
	}

	spec := agents.RunSpec{
		RunType:        p.RunType,
		AgentID:        p.AgentID,
		TeamID:         p.TeamID,
		Input:          p.Input,
		Timeout:        time.Duration(p.TimeoutMS) * time.Millisecond,
		DynamicAgents:  p.DynamicAgents,
		ConfigSnapshot: p.ConfigSnapshot,
	}

	run, err := s.agents.StartRun(r.Context(), spec, actor)
	if err != nil {
		if errors.Is(err, agents.ErrQueueFull) {
			writeError(w, r, http.StatusTooManyRequests, "queue_full", "execution queue is full")
			return
		}
		writeError(w, r, http.StatusBadRequest, "execution_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleRunsList(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	runs, err := s.agents.Store().ListRuns(r.Context(), limit, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if runs == nil {
		runs = []agents.RunRecord{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	run, err := s.agents.Store().GetRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "run not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	id := r.PathValue("id")
	actor := "anonymous"
	if princ := PrincipalFrom(r.Context()); princ != nil {
		actor = princ.Name
	}
	if err := s.agents.CancelRun(r.Context(), id, actor); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "run not found")
			return
		}
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true, "run_id": id})
}

// Task Handlers

func (s *Server) handleTasksList(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	runID := r.PathValue("id")
	tasks, err := s.agents.Store().ListTasksByRun(r.Context(), runID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if tasks == nil {
		tasks = []agents.TaskRecord{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	taskID := r.PathValue("taskId")
	task, err := s.agents.Store().GetTask(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "task not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if !s.checkAgentsConfigured(w, r) {
		return
	}
	taskID := r.PathValue("taskId")
	actor := "anonymous"
	if princ := PrincipalFrom(r.Context()); princ != nil {
		actor = princ.Name
	}
	if err := s.agents.CancelTask(r.Context(), taskID, actor); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "task not found")
			return
		}
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true, "task_id": taskID})
}
