package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
)

type completionRequest struct {
	Model       string                `json:"model"`
	Provider    string                `json:"provider,omitempty"`
	Profile     string                `json:"routing_profile,omitempty"`
	Group       string                `json:"model_group,omitempty"`
	Messages    []json.RawMessage     `json:"messages"`
	Temperature *float64              `json:"temperature,omitempty"`
	TopP        *float64              `json:"top_p,omitempty"`
	MaxTokens   *int                  `json:"max_tokens,omitempty"`
	Stream      bool                  `json:"stream,omitempty"`
	Tools       []providers.Tool      `json:"tools,omitempty"`
	ToolChoice  *providers.ToolChoice `json:"tool_choice,omitempty"`
	Stop        []string              `json:"stop,omitempty"`
	Seed        *int64                `json:"seed,omitempty"`
}
type openAIMessage struct {
	Role       string               `json:"role"`
	Content    json.RawMessage      `json:"content"`
	Name       string               `json:"name,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	ToolCalls  []providers.ToolCall `json:"tool_calls,omitempty"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if s.router == nil {
		writeError(w, r, http.StatusServiceUnavailable, "routing_unavailable", "generation routing is not configured")
		return
	}
	var body completionRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if len(body.Messages) == 0 || len(body.Messages) > 2048 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "messages must contain between 1 and 2048 entries")
		return
	}
	msgs := make([]providers.Message, 0, len(body.Messages))
	for i, raw := range body.Messages {
		var m openAIMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", fmt.Sprintf("messages[%d] is invalid", i))
			return
		}
		role := providers.Role(m.Role)
		if !role.Valid() {
			writeError(w, r, http.StatusBadRequest, "invalid_request", fmt.Sprintf("messages[%d].role is invalid", i))
			return
		}
		msg := providers.Message{Role: role, Name: m.Name, ToolCallID: m.ToolCallID, ToolCalls: m.ToolCalls}
		if len(m.Content) > 0 && string(m.Content) != "null" {
			var txt string
			if json.Unmarshal(m.Content, &txt) == nil {
				msg.Content = []providers.ContentPart{providers.TextPart(txt)}
			} else {
				if err := json.Unmarshal(m.Content, &msg.Content); err != nil {
					writeError(w, r, http.StatusBadRequest, "invalid_request", fmt.Sprintf("messages[%d].content is invalid", i))
					return
				}
			}
		}
		msgs = append(msgs, msg)
	}
	model := body.Model
	if model == "" {
		model = "auto"
	}
	req := providers.ChatRequest{Model: model, Messages: msgs, Temperature: body.Temperature, TopP: body.TopP, MaxTokens: body.MaxTokens, Stream: body.Stream, Tools: body.Tools, ToolChoice: body.ToolChoice, Stop: body.Stop, Seed: body.Seed}
	if err := req.Validate(); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.Stream {
		result, err := s.router.Stream(r.Context(), req, body.Profile, body.Group, body.Provider)
		if err != nil {
			s.recordAudit(r, "generation.failed", "", map[string]any{"stream": true, "error": safeErrorKind(err)})
			s.writeGenerationError(w, r, err)
			return
		}
		defer result.Stream.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, r, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported by this server")
			return
		}
		w.WriteHeader(http.StatusOK)
		send := func(ev providers.StreamEvent) bool {
			payload := map[string]any{"id": RequestID(r.Context()), "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": result.ModelID, "choices": []any{map[string]any{"index": 0, "delta": streamDelta(ev), "finish_reason": streamFinish(ev)}}}
			b, e := json.Marshal(payload)
			if e != nil {
				return false
			}
			if _, e = fmt.Fprintf(w, "data: %s\n\n", b); e != nil {
				return false
			}
			flusher.Flush()
			return true
		}
		if result.First.Type != providers.StreamDone && !send(result.First) {
			return
		}
		for {
			ev, e := result.Stream.Recv(r.Context())
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "stream_error", "message": "provider stream failed after output began"}})
				_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", b)
				flusher.Flush()
				break
			}
			if !send(ev) {
				return
			}
			if ev.Type == providers.StreamDone {
				break
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		s.recordAudit(r, "generation.completed", result.ModelID, map[string]any{"provider_id": result.ProviderID, "attempts": result.Attempts, "stream": true})
		return
	}
	result, err := s.router.Generate(r.Context(), req, body.Profile, body.Group, body.Provider)
	if err != nil {
		s.recordAudit(r, "generation.failed", "", map[string]any{"stream": false, "error": safeErrorKind(err)})
		s.writeGenerationError(w, r, err)
		return
	}
	s.recordAudit(r, "generation.completed", result.ModelID, map[string]any{"provider_id": result.ProviderID, "attempts": result.Attempts, "stream": false})
	response := map[string]any{"id": RequestID(r.Context()), "object": "chat.completion", "created": time.Now().Unix(), "model": result.ModelID, "choices": []any{map[string]any{"index": 0, "message": result.Response.Message, "finish_reason": result.Response.FinishReason}}}
	if result.Response.Usage != nil {
		response["usage"] = result.Response.Usage
	}
	writeJSON(w, http.StatusOK, response)
}
func streamDelta(ev providers.StreamEvent) any {
	if ev.Type == providers.StreamTextDelta {
		return map[string]string{"content": ev.Text}
	}
	if ev.Type == providers.StreamToolCallDelta {
		return map[string]any{"tool_calls": ev.ToolCall}
	}
	return map[string]any{}
}
func streamFinish(ev providers.StreamEvent) any {
	if ev.Type == providers.StreamDone {
		return ev.FinishReason
	}
	return nil
}
func safeErrorKind(err error) string {
	var pe *providers.Error
	if errors.As(err, &pe) {
		return string(pe.Kind)
	}
	return "internal"
}
func (s *Server) writeGenerationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, models.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "model_not_found", "requested model was not found")
		return
	}
	var pe *providers.Error
	if errors.As(err, &pe) {
		status := providerErrorStatus(pe.Kind)
		writeError(w, r, status, string(pe.Kind), pe.Message)
		return
	}
	if strings.Contains(err.Error(), "no eligible") {
		writeError(w, r, http.StatusServiceUnavailable, "no_eligible_candidates", "no eligible provider/model candidates")
		return
	}
	writeError(w, r, http.StatusBadGateway, "generation_failed", "generation request failed")
}
func (s *Server) handleRoutingProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": s.router.Profiles()})
}
func (s *Server) handleRoutingProfilePut(w http.ResponseWriter, r *http.Request) {
	var p routing.Profile
	if err := decodeStrictJSON(r, &p); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	p.ID = r.PathValue("id")
	if err := s.router.SetProfile(p); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "routing.profile.updated", p.ID, map[string]any{"policy": p.Policy, "candidates": len(p.Candidates)})
	writeJSON(w, http.StatusOK, p)
}
func (s *Server) handleRoutingGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"groups": s.router.Groups()})
}
func (s *Server) handleRoutingGroupPut(w http.ResponseWriter, r *http.Request) {
	var g routing.Group
	if err := decodeStrictJSON(r, &g); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	g.ID = r.PathValue("id")
	if err := s.router.SetGroup(g); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "routing.group.updated", g.ID, map[string]any{"models": len(g.Models)})
	writeJSON(w, http.StatusOK, g)
}
func (s *Server) handleRoutingStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"breakers": s.router.Breakers(),
		"profiles": len(s.router.Profiles()),
		"groups":   len(s.router.Groups()),
	}
	if rl := s.router.RateLimiter(); rl != nil {
		resp["rate_limits"] = rl.Status()
	}
	if hm := s.router.HealthMonitor(); hm != nil {
		resp["health"] = hm.Status()
	}
	writeJSON(w, http.StatusOK, resp)
}

type breakerUpdateRequest struct {
	Threshold int           `json:"threshold"`
	Recovery  time.Duration `json:"recovery"`
	MaxProbes int           `json:"max_probes"`
}

func (s *Server) handleBreakerPut(w http.ResponseWriter, r *http.Request) {
	if s.router == nil {
		writeError(w, r, http.StatusServiceUnavailable, "routing_unavailable", "router not configured")
		return
	}
	providerID := r.PathValue("id")
	var body breakerUpdateRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if err := s.router.ConfigureBreaker(providerID, body.Threshold, body.Recovery, body.MaxProbes); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.recordAudit(r, "routing.breaker.updated", providerID, map[string]any{
		"threshold":  body.Threshold,
		"recovery":   body.Recovery.String(),
		"max_probes": body.MaxProbes,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_id": providerID,
		"threshold":   body.Threshold,
		"recovery":    body.Recovery,
		"max_probes":  body.MaxProbes,
	})
}

func (s *Server) handleUsageGet(w http.ResponseWriter, r *http.Request) {
	if s.router == nil || s.router.Store() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"records": []any{}})
		return
	}
	q := r.URL.Query()
	f := routing.UsageFilter{
		ProviderID: q.Get("provider"),
		ModelID:    q.Get("model"),
	}
	if sVal := q.Get("success"); sVal != "" {
		b := sVal == "true" || sVal == "1"
		f.Success = &b
	}
	if sinceVal := q.Get("since"); sinceVal != "" {
		if t, err := time.Parse(time.RFC3339, sinceVal); err == nil {
			f.Since = t
		}
	}
	if untilVal := q.Get("until"); untilVal != "" {
		if t, err := time.Parse(time.RFC3339, untilVal); err == nil {
			f.Until = t
		}
	}
	if limVal := q.Get("limit"); limVal != "" {
		if n, err := strconv.Atoi(limVal); err == nil && n > 0 {
			f.Limit = n
		}
	}
	records, err := s.router.Store().QueryUsage(r.Context(), f)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	if records == nil {
		records = []routing.UsageRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

func (s *Server) handleUsageSummaryGet(w http.ResponseWriter, r *http.Request) {
	if s.router == nil || s.router.Store() == nil {
		writeJSON(w, http.StatusOK, routing.UsageSummary{})
		return
	}
	q := r.URL.Query()
	f := routing.UsageFilter{
		ProviderID: q.Get("provider"),
		ModelID:    q.Get("model"),
	}
	if sVal := q.Get("success"); sVal != "" {
		b := sVal == "true" || sVal == "1"
		f.Success = &b
	}
	if sinceVal := q.Get("since"); sinceVal != "" {
		if t, err := time.Parse(time.RFC3339, sinceVal); err == nil {
			f.Since = t
		}
	}
	if untilVal := q.Get("until"); untilVal != "" {
		if t, err := time.Parse(time.RFC3339, untilVal); err == nil {
			f.Until = t
		}
	}
	sum, err := s.router.Store().SummaryUsage(r.Context(), f)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleRateLimitsGet(w http.ResponseWriter, r *http.Request) {
	if s.router == nil || s.router.RateLimiter() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"rate_limits": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rate_limits": s.router.RateLimiter().Status()})
}

func (s *Server) handleRoutingHealthGet(w http.ResponseWriter, r *http.Request) {
	if s.router == nil || s.router.HealthMonitor() == nil {
		writeJSON(w, http.StatusOK, map[string]any{"health": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"health": s.router.HealthMonitor().Status()})
}
