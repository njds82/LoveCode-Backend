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
	"strings"
	"testing"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/core"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/storage"
)

type apiTestFixture struct {
	srv     *Server
	router  *routing.Router
	db      *storage.DB
	manager *providers.Manager
	admin   string
	write   string
	read    string
}

func setupAPIFixture(t *testing.T) *apiTestFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "api_test.db"))
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

	// Load deterministic mock provider
	bTrue := true
	pCfg := config.ProviderConfig{
		ID:       "mock",
		Type:     "mock",
		Settings: map[string]any{"reply": "Hello from mock generation!"},
		Models: []config.ModelConfig{
			{
				ID:              "mock:chat",
				ProviderModelID: "mock-1",
				Streaming:       &bTrue,
				ToolCalls:       &bTrue,
			},
		},
	}
	if err := manager.Load(ctx, []config.ProviderConfig{pCfg}); err != nil {
		t.Fatalf("manager.Load: %v", err)
	}

	router := routing.NewWithOptions(manager, routing.RouterOptions{
		DB: db.DB,
	})

	// Configure default profile
	_ = router.SetProfile(routing.Profile{
		ID:          "default",
		Policy:      routing.Priority,
		MaxAttempts: 2,
		Candidates:  []routing.Candidate{{ProviderID: "mock", ModelID: "mock:chat", Priority: 10, Weight: 1}},
	})

	cfg := config.Defaults().Server
	srv := New(Deps{
		Config:    cfg,
		Log:       logger,
		Tokens:    ts,
		Audit:     auditLog,
		Providers: manager,
		Router:    router,
		Version:   "test",
		Health: func(context.Context) map[string]core.Health {
			return map[string]core.Health{"storage": {Status: core.StatusOK}}
		},
	})

	return &apiTestFixture{
		srv:     srv,
		router:  router,
		db:      db,
		manager: manager,
		admin:   adminToken,
		write:   writeToken,
		read:    readToken,
	}
}

func (f *apiTestFixture) request(method, path, token string, body any) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
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

func TestChatCompletionsNonStreaming(t *testing.T) {
	f := setupAPIFixture(t)

	// Valid chat completion
	reqBody := map[string]any{
		"model": "mock:chat",
		"messages": []map[string]any{
			{"role": "user", "content": "Hello!"},
		},
	}
	rec := f.request("POST", "/v1/chat/completions", f.write, reqBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	choices := res["choices"].([]any)
	if len(choices) == 0 {
		t.Fatal("empty choices")
	}
	firstChoice := choices[0].(map[string]any)
	msg := firstChoice["message"].(map[string]any)
	content := msg["content"].([]any)
	firstPart := content[0].(map[string]any)
	if firstPart["text"] != "Hello from mock generation!" {
		t.Fatalf("unexpected content text: %v", firstPart["text"])
	}
}

func TestChatCompletionsStreaming(t *testing.T) {
	f := setupAPIFixture(t)

	reqBody := map[string]any{
		"model":  "mock:chat",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "Stream to me"},
		},
	}
	rec := f.request("POST", "/v1/chat/completions", f.write, reqBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	bodyStr := rec.Body.String()
	if !strings.Contains(bodyStr, "data: ") || !strings.Contains(bodyStr, "data: [DONE]") {
		t.Fatalf("stream body missing chunks or DONE event: %s", bodyStr)
	}
}

func TestRoutingManagementEndpoints(t *testing.T) {
	f := setupAPIFixture(t)

	// GET profiles with read token
	rec := f.request("GET", "/v1/routing/profiles", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for profiles list, got %d", rec.Code)
	}

	// PUT profile with write token should fail (requires admin)
	profUpdate := map[string]any{
		"policy":       "priority",
		"max_attempts": 3,
		"candidates": []map[string]any{
			{"provider_id": "mock", "model_id": "mock:chat", "priority": 1, "weight": 1},
		},
	}
	rec = f.request("PUT", "/v1/routing/profiles/custom", f.write, profUpdate)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-admin put profile, got %d", rec.Code)
	}

	// PUT profile with admin token
	rec = f.request("PUT", "/v1/routing/profiles/custom", f.admin, profUpdate)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin put profile, got %d: %s", rec.Code, rec.Body.String())
	}

	// PUT group
	groupUpdate := map[string]any{
		"models": []string{"mock:chat"},
	}
	rec = f.request("PUT", "/v1/routing/groups/group1", f.admin, groupUpdate)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for put group, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET routing status
	rec = f.request("GET", "/v1/routing/status", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for routing status, got %d", rec.Code)
	}
	var st map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["profiles"].(float64) < 2 {
		t.Fatalf("expected at least 2 profiles, got %v", st["profiles"])
	}

	// PUT breaker config
	bUpdate := map[string]any{
		"threshold":  4,
		"recovery":   20000000000, // 20s in ns
		"max_probes": 2,
	}
	rec = f.request("PUT", "/v1/routing/breakers/mock", f.admin, bUpdate)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for put breaker, got %d: %s", rec.Code, rec.Body.String())
	}

	// GET usage & summary
	rec = f.request("GET", "/v1/routing/usage", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for usage query, got %d", rec.Code)
	}
	rec = f.request("GET", "/v1/routing/usage/summary", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for usage summary, got %d", rec.Code)
	}

	// GET rate limits & health
	rec = f.request("GET", "/v1/routing/ratelimits", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for ratelimits, got %d", rec.Code)
	}
	rec = f.request("GET", "/v1/routing/health", f.read, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for routing health, got %d", rec.Code)
	}
}
