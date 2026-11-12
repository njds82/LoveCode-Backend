package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/core"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/storage"
)

type fixture struct {
	srv      *Server
	tokens   *security.TokenStore
	admin    string
	manager  *providers.Manager
	models   *models.Registry
	auditLog *audit.Log
}

func setup(t *testing.T, mutate func(*config.Server)) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults().Server
	if mutate != nil {
		mutate(&cfg)
	}
	ts := security.NewTokenStore(db.DB)
	admin, _, err := ts.Create(ctx, "owner", []string{security.ScopeAdmin}, 0)
	if err != nil {
		t.Fatal(err)
	}
	auditLog := audit.New(db.DB)
	modelRegistry := models.NewRegistry()
	manager := providers.NewManager(providers.NewRegistry(), modelRegistry, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := New(Deps{
		Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Tokens: ts, Audit: auditLog, Providers: manager, Version: "test",
		Health: func(context.Context) map[string]core.Health {
			return map[string]core.Health{"storage": {Status: core.StatusOK}}
		},
	})
	return &fixture{srv: s, tokens: ts, admin: admin, manager: manager, models: modelRegistry, auditLog: auditLog}
}

func (f *fixture) do(method, path, token, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

const loop = "127.0.0.1:5555"

func TestHealthPublicAndRequestID(t *testing.T) {
	f := setup(t, nil)
	rec := f.do("GET", "/v1/health", "", loop)
	if rec.Code != 200 || rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("health: %d %v", rec.Code, rec.Header())
	}
}

func TestStatusRequiresAuth(t *testing.T) {
	f := setup(t, nil)
	if rec := f.do("GET", "/v1/status", "", loop); rec.Code != 401 {
		t.Fatalf("want 401 got %d", rec.Code)
	}
	if rec := f.do("GET", "/v1/status", "lc_wrong", loop); rec.Code != 401 {
		t.Fatalf("want 401 got %d", rec.Code)
	}
	rec := f.do("GET", "/v1/status", f.admin, loop)
	if rec.Code != 200 {
		t.Fatalf("want 200 got %d: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" || body["version"] != "test" {
		t.Fatalf("bad body %v", body)
	}
}

func TestScopeEnforced(t *testing.T) {
	f := setup(t, nil)
	readTok, _, _ := f.tokens.Create(context.Background(), "ro", []string{"read"}, 0)
	if rec := f.do("GET", "/v1/status", readTok, loop); rec.Code != 200 {
		t.Fatalf("read scope should reach status, got %d", rec.Code)
	}
	if rec := f.do("GET", "/v1/audit", readTok, loop); rec.Code != 403 {
		t.Fatalf("read scope must not reach audit, got %d", rec.Code)
	}
}

func TestLocalModeRejectsNonLoopback(t *testing.T) {
	f := setup(t, nil)
	if rec := f.do("GET", "/v1/health", "", "192.168.1.50:4000"); rec.Code != 403 {
		t.Fatalf("want 403 got %d", rec.Code)
	}
	g := setup(t, func(c *config.Server) { c.Mode = config.ModeLAN; c.Listen = "0.0.0.0:7878" })
	if rec := g.do("GET", "/v1/health", "", "192.168.1.50:4000"); rec.Code != 200 {
		t.Fatalf("lan mode should allow, got %d", rec.Code)
	}
}

func TestRateLimit(t *testing.T) {
	f := setup(t, func(c *config.Server) { c.RateLimitBurst = 2; c.RateLimitPerSec = 0.001 })
	codes := []int{}
	for i := 0; i < 3; i++ {
		codes = append(codes, f.do("GET", "/v1/health", "", loop).Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("got %v", codes)
	}
}

func TestAuditEndpointsAndFailureRecorded(t *testing.T) {
	f := setup(t, nil)
	f.do("GET", "/v1/status", "lc_nope", loop) // records auth.failure
	rec := f.do("GET", "/v1/audit?action=auth.failure", f.admin, loop)
	if rec.Code != 200 {
		t.Fatalf("audit list: %d %s", rec.Code, rec.Body)
	}
	var body struct{ Entries []audit.Entry }
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Entries) != 1 {
		t.Fatalf("want 1 auth.failure entry, got %d", len(body.Entries))
	}
	rec = f.do("GET", "/v1/audit/verify", f.admin, loop)
	var v audit.VerifyResult
	json.Unmarshal(rec.Body.Bytes(), &v)
	if rec.Code != 200 || !v.OK {
		t.Fatalf("verify: %d %+v", rec.Code, v)
	}
}

func TestUnknownRouteNormalizedError(t *testing.T) {
	f := setup(t, nil)
	rec := f.do("GET", "/v1/nope", "", loop)
	var e errorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 404 || e.Error.Code != "not_found" || e.Error.RequestID == "" {
		t.Fatalf("got %d %+v", rec.Code, e)
	}
	_ = http.StatusOK
}
