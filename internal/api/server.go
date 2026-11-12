// Package api is the HTTP gateway. Phase 1 exposes health, status and audit
// endpoints under /v1. Later phases add providers, chat, agents, etc.
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/lovecode/lovecode/internal/agents"
	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/core"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/security"
)

type Deps struct {
	Config    config.Server
	Log       *slog.Logger
	Tokens    *security.TokenStore
	Audit     *audit.Log
	Providers *providers.Manager
	Router    *routing.Router
	Agents    *agents.Service
	Health    func(ctx context.Context) map[string]core.Health
	Version   string
}

type Server struct {
	log       *slog.Logger
	tokens    *security.TokenStore
	audit     *audit.Log
	providers *providers.Manager
	router    *routing.Router
	agents    *agents.Service
	health    func(ctx context.Context) map[string]core.Health
	version   string
	started   time.Time
	cfg       config.Server
	localMode bool
	maxBody   int64
	limiter   *limiter

	handler http.Handler
	httpSrv *http.Server
	ln      net.Listener
}

func New(d Deps) *Server {
	s := &Server{
		log: d.Log, tokens: d.Tokens, audit: d.Audit, providers: d.Providers, health: d.Health, version: d.Version,
		router: d.Router, agents: d.Agents, cfg: d.Config, localMode: d.Config.Mode == config.ModeLocal, maxBody: d.Config.MaxBodyBytes,
		limiter: newLimiter(d.Config.RateLimitPerSec, d.Config.RateLimitBurst),
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/health", http.HandlerFunc(s.handleHealth)) // public, minimal
	mux.Handle("GET /v1/status", s.protect("read", s.handleStatus))
	mux.Handle("GET /v1/audit", s.protect(security.ScopeAdmin, s.handleAuditList))
	mux.Handle("GET /v1/audit/verify", s.protect(security.ScopeAdmin, s.handleAuditVerify))
	mux.Handle("GET /v1/providers", s.protect("read", s.handleProviderList))
	mux.Handle("GET /v1/providers/{id}", s.protect("read", s.handleProviderGet))
	mux.Handle("PATCH /v1/providers/{id}", s.protect(security.ScopeAdmin, s.handleProviderPatch))
	mux.Handle("POST /v1/providers/{id}/test", s.protect(security.ScopeAdmin, s.handleProviderTest))
	mux.Handle("POST /v1/providers/{id}/models/refresh", s.protect(security.ScopeAdmin, s.handleProviderRefresh))
	mux.Handle("GET /v1/models", s.protect("read", s.handleModelList))
	mux.Handle("GET /v1/models/{id...}", s.protect("read", s.handleModelGet))
	mux.Handle("POST /v1/chat/completions", s.protect("write", s.handleChatCompletions))
	mux.Handle("GET /v1/routing/profiles", s.protect("read", s.handleRoutingProfiles))
	mux.Handle("PUT /v1/routing/profiles/{id}", s.protect(security.ScopeAdmin, s.handleRoutingProfilePut))
	mux.Handle("GET /v1/routing/groups", s.protect("read", s.handleRoutingGroups))
	mux.Handle("PUT /v1/routing/groups/{id}", s.protect(security.ScopeAdmin, s.handleRoutingGroupPut))
	mux.Handle("GET /v1/routing/status", s.protect("read", s.handleRoutingStatus))
	mux.Handle("PUT /v1/routing/breakers/{id}", s.protect(security.ScopeAdmin, s.handleBreakerPut))
	mux.Handle("GET /v1/routing/usage", s.protect("read", s.handleUsageGet))
	mux.Handle("GET /v1/routing/usage/summary", s.protect("read", s.handleUsageSummaryGet))
	mux.Handle("GET /v1/routing/ratelimits", s.protect("read", s.handleRateLimitsGet))
	mux.Handle("GET /v1/routing/health", s.protect("read", s.handleRoutingHealthGet))
	mux.Handle("PATCH /v1/models/{id...}", s.protect(security.ScopeAdmin, s.handleModelPatch))

	// Agents
	mux.Handle("GET /v1/agents", s.protect("read", s.handleAgentsList))
	mux.Handle("POST /v1/agents", s.protect(security.ScopeAdmin, s.handleAgentCreate))
	mux.Handle("GET /v1/agents/{id}", s.protect("read", s.handleAgentGet))
	mux.Handle("PUT /v1/agents/{id}", s.protect(security.ScopeAdmin, s.handleAgentUpdate))
	mux.Handle("PATCH /v1/agents/{id}", s.protect(security.ScopeAdmin, s.handleAgentPatch))
	mux.Handle("DELETE /v1/agents/{id}", s.protect(security.ScopeAdmin, s.handleAgentDelete))

	// Teams
	mux.Handle("GET /v1/teams", s.protect("read", s.handleTeamsList))
	mux.Handle("POST /v1/teams", s.protect(security.ScopeAdmin, s.handleTeamCreate))
	mux.Handle("GET /v1/teams/{id}", s.protect("read", s.handleTeamGet))
	mux.Handle("PUT /v1/teams/{id}", s.protect(security.ScopeAdmin, s.handleTeamUpdate))
	mux.Handle("PATCH /v1/teams/{id}", s.protect(security.ScopeAdmin, s.handleTeamPatch))
	mux.Handle("DELETE /v1/teams/{id}", s.protect(security.ScopeAdmin, s.handleTeamDelete))

	// Runs
	mux.Handle("POST /v1/agent-runs", s.protect("write", s.handleRunCreate))
	mux.Handle("GET /v1/agent-runs", s.protect("read", s.handleRunsList))
	mux.Handle("GET /v1/agent-runs/{id}", s.protect("read", s.handleRunGet))
	mux.Handle("POST /v1/agent-runs/{id}/cancel", s.protect("write", s.handleRunCancel))
	mux.Handle("DELETE /v1/agent-runs/{id}", s.protect("write", s.handleRunCancel))

	// Tasks
	mux.Handle("GET /v1/agent-runs/{id}/tasks", s.protect("read", s.handleTasksList))
	mux.Handle("GET /v1/agent-runs/{id}/tasks/{taskId}", s.protect("read", s.handleTaskGet))
	mux.Handle("POST /v1/agent-runs/{id}/tasks/{taskId}/cancel", s.protect("write", s.handleTaskCancel))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "no such endpoint")
	})
	// Order (outermost first): requestID, recover, accessLog, localOnly, rateLimit, bodyLimit.
	s.handler = s.requestID(s.recoverer(s.accessLog(s.localOnly(s.rateLimit(s.bodyLimit(mux))))))
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) Name() string { return "api" }

func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	s.ln = ln
	s.started = time.Now()
	s.httpSrv = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: s.cfg.ReadHeaderTimeout.Std(),
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	useTLS := s.cfg.TLSCert != ""
	if useTLS {
		s.httpSrv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	go func() {
		var err error
		if useTLS {
			err = s.httpSrv.ServeTLS(ln, s.cfg.TLSCert, s.cfg.TLSKey)
		} else {
			err = s.httpSrv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("http server stopped unexpectedly", "err", err)
		}
	}()
	s.log.Info("api listening", "addr", ln.Addr().String(), "mode", s.cfg.Mode, "tls", useTLS)
	return nil
}

func (s *Server) Stop(ctx context.Context) error { return s.httpSrv.Shutdown(ctx) }

// ---- responses ----

type errorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message, b.Error.RequestID = code, msg, RequestID(r.Context())
	writeJSON(w, status, b)
}

// ---- handlers ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	mods := s.health(r.Context())
	overall := core.StatusOK
	for _, h := range mods {
		if h.Status != core.StatusOK {
			overall = core.StatusDegraded
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         overall,
		"version":        s.version,
		"mode":           s.cfg.Mode,
		"uptime_seconds": int64(time.Since(s.started).Seconds()),
		"modules":        mods,
	})
}

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	es, err := s.audit.List(r.Context(), limit, r.URL.Query().Get("action"))
	if err != nil {
		s.log.Error("audit list", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "could not read audit log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": es})
}

func (s *Server) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	res, err := s.audit.Verify(r.Context())
	if err != nil {
		s.log.Error("audit verify", "err", err)
		writeError(w, r, http.StatusInternalServerError, "internal", "could not verify audit log")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
