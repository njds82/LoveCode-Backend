package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lovecode/lovecode/internal/agents"
	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/storage"
)

type agentsAPIFixture struct {
	srv     *Server
	service *agents.Service
	admin   string
	write   string
	read    string
}

func setupAgentsAPIFixture(t *testing.T) *agentsAPIFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "agents_api_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	ts := security.NewTokenStore(db.DB)
	adminToken, _, _ := ts.Create(ctx, "admin-user", []string{security.ScopeAdmin}, 0)
	writeToken, _, _ := ts.Create(ctx, "write-user", []string{"write"}, 0)
	readToken, _, _ := ts.Create(ctx, "read-user", []string{"read"}, 0)

	auditLog := audit.New(db.DB)
	modelRegistry := models.NewRegistry()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := providers.NewManager(providers.NewRegistry(), modelRegistry, logger)

	bTrue := true
	pCfg := config.ProviderConfig{
		ID:       "mock",
		Type:     "mock",
		Settings: map[string]any{"reply": "Hello from mock agent execution!"},
		Models: []config.ModelConfig{
			{
				ID:              "mock:chat",
				ProviderModelID: "mock-1",
				Streaming:       &bTrue,
			},
		},
	}
	if err := manager.Load(ctx, []config.ProviderConfig{pCfg}); err != nil {
		t.Fatalf("manager.Load: %v", err)
	}

	router := routing.NewWithOptions(manager, routing.RouterOptions{DB: db.DB})
	_ = router.SetProfile(routing.Profile{
		ID:          "default",
		Policy:      routing.Priority,
		MaxAttempts: 2,
		Candidates:  []routing.Candidate{{ProviderID: "mock", ModelID: "mock:chat", Priority: 10, Weight: 1}},
	})

	cfg := config.Defaults()
	agentStore := agents.NewStore(db.DB)
	agentService := agents.NewService(cfg.Agents, agentStore, router, auditLog, logger)
	if err := agentService.Start(ctx); err != nil {
		t.Fatalf("agentService.Start: %v", err)
	}
	t.Cleanup(func() { _ = agentService.Stop(ctx) })

	srv := New(Deps{
		Config:    cfg.Server,
		Log:       logger,
		Tokens:    ts,
		Audit:     auditLog,
		Providers: manager,
		Router:    router,
		Agents:    agentService,
		Version:   "test",
	})

	return &agentsAPIFixture{
		srv:     srv,
		service: agentService,
		admin:   adminToken,
		write:   writeToken,
		read:    readToken,
	}
}

func (f *agentsAPIFixture) request(method, path, token string, body any) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rdr = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "127.0.0.1:4567"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAgentsAPIEndpoints(t *testing.T) {
	f := setupAgentsAPIFixture(t)

	// 1. Unauthenticated request to /v1/agents returns 401
	rec := f.request("GET", "/v1/agents", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 unauthorized, got %d", rec.Code)
	}

	// 2. Insufficient scope for POST /v1/agents (read token should get 403)
	agentPayload := map[string]any{
		"id":            "test-agent",
		"name":          "Researcher",
		"role":          "Search",
		"model":         "mock:chat",
		"system_prompt": "You search data.",
	}
	rec = f.request("POST", "/v1/agents", f.read, agentPayload)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 forbidden for read token on POST /v1/agents, got %d", rec.Code)
	}

	// 3. Admin token creates agent successfully (201)
	rec = f.request("POST", "/v1/agents", f.admin, agentPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 created, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 4. GET /v1/agents with read scope (200)
	rec = f.request("GET", "/v1/agents", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var agentList []agents.AgentDefinition
	_ = json.Unmarshal(rec.Body.Bytes(), &agentList)
	if len(agentList) != 1 || agentList[0].ID != "test-agent" {
		t.Errorf("unexpected agent list: %+v", agentList)
	}

	// 5. GET /v1/agents/{id}
	rec = f.request("GET", "/v1/agents/test-agent", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 6. PATCH /v1/agents/{id} to disable
	rec = f.request("PATCH", "/v1/agents/test-agent", f.admin, map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on patch, got %d", rec.Code)
	}

	// Verify disabled agent not in default list
	rec = f.request("GET", "/v1/agents", f.read, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &agentList)
	if len(agentList) != 0 {
		t.Errorf("expected 0 enabled agents, got %d", len(agentList))
	}

	// Re-enable agent
	f.request("PATCH", "/v1/agents/test-agent", f.admin, map[string]any{"enabled": true})

	// 7. Teams API: POST /v1/teams
	teamPayload := map[string]any{
		"id":       "team-1",
		"name":     "Research Team",
		"strategy": "sequential",
		"members": []map[string]any{
			{"agent_id": "test-agent", "required": true},
		},
	}
	rec = f.request("POST", "/v1/teams", f.admin, teamPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for team creation, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// GET /v1/teams
	rec = f.request("GET", "/v1/teams", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for teams list, got %d", rec.Code)
	}

	// GET /v1/teams/{id}
	rec = f.request("GET", "/v1/teams/team-1", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for team get, got %d", rec.Code)
	}

	// 8. Start Agent Run: POST /v1/agent-runs (write scope)
	runPayload := map[string]any{
		"run_type": "agent",
		"agent_id": "test-agent",
		"input":    "Perform research on LoveCode architecture.",
	}
	rec = f.request("POST", "/v1/agent-runs", f.write, runPayload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 created for agent run, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var run agents.RunRecord
	_ = json.Unmarshal(rec.Body.Bytes(), &run)
	if run.ID == "" || run.Status != agents.StatusCompleted {
		t.Errorf("unexpected run record: %+v", run)
	}

	// 9. GET /v1/agent-runs
	rec = f.request("GET", "/v1/agent-runs", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for list runs, got %d", rec.Code)
	}

	// 10. GET /v1/agent-runs/{id}
	rec = f.request("GET", "/v1/agent-runs/"+run.ID, f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for get run, got %d", rec.Code)
	}

	// 11. Tasks API: GET /v1/agent-runs/{id}/tasks
	rec = f.request("GET", "/v1/agent-runs/"+run.ID+"/tasks", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for get tasks, got %d", rec.Code)
	}
	var tasks []agents.TaskRecord
	_ = json.Unmarshal(rec.Body.Bytes(), &tasks)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	// GET /v1/agent-runs/{id}/tasks/{taskId}
	rec = f.request("GET", "/v1/agent-runs/"+run.ID+"/tasks/"+tasks[0].ID, f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for task detail, got %d", rec.Code)
	}

	// 12. Delete Agent & Team
	rec = f.request("DELETE", "/v1/teams/team-1", f.admin, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on team delete, got %d", rec.Code)
	}

	rec = f.request("DELETE", "/v1/agents/test-agent", f.admin, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 on agent delete, got %d", rec.Code)
	}

	// Verify that run and task historical records still exist after agent deletion!
	rec = f.request("GET", "/v1/agent-runs/"+run.ID, f.read, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("historical run record was lost after agent deletion! got %d", rec.Code)
	}
}
