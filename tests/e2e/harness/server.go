// Package harness provides shared test infrastructure for LoveCode E2E tests.
package harness

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/agents"
	"github.com/lovecode/lovecode/internal/api"
	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/core"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
	"github.com/lovecode/lovecode/internal/routing"
	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/storage"
)

// Server represents a running LoveCode test server instance.
type Server struct {
	URL        string
	DB         *storage.DB
	DBPath     string
	AdminToken string
	WriteToken string
	ReadToken  string
	TokenStore *security.TokenStore
	AuditLog   *audit.Log
	Agents     *agents.Service
	Router     *routing.Router
	Providers  *providers.Manager
	Client     *Client

	httpSrv *httptest.Server
	ln      net.Listener
	mu      sync.Mutex
	closed  bool
}

// ServerOptions allows customizing server instantiation.
type ServerOptions struct {
	AllowedRoots []string
	LocalMode    bool
}

// NewServer starts an isolated, in-process LoveCode server for testing.
// It creates a temp SQLite DB, runs all available migrations, generates
// scoped tokens, sets up an ephemeral HTTP listener, and registers cleanup.
func NewServer(t testing.TB, opts ...ServerOptions) *Server {
	t.Helper()

	// If LOVECODE_API_URL is set, connect directly to that external server
	if extURL := os.Getenv("LOVECODE_API_URL"); extURL != "" {
		adminTok := os.Getenv("LOVECODE_API_TOKEN")
		s := &Server{
			URL:        extURL,
			AdminToken: adminTok,
			WriteToken: adminTok,
			ReadToken:  adminTok,
		}
		s.Client = NewClient(extURL).WithToken(adminTok)
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "e2e_test.db")

	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("harness.NewServer: storage.Open failed: %v", err)
	}

	// Run all available migrations
	if _, err := db.Migrate(ctx); err != nil {
		db.Close()
		t.Fatalf("harness.NewServer: db.Migrate failed: %v", err)
	}

	tokenStore := security.NewTokenStore(db.DB)
	adminToken, _, err := tokenStore.Create(ctx, "e2e-admin", []string{security.ScopeAdmin}, 0)
	if err != nil {
		db.Close()
		t.Fatalf("harness.NewServer: create admin token failed: %v", err)
	}

	writeToken, _, err := tokenStore.Create(ctx, "e2e-writer", []string{"write"}, 0)
	if err != nil {
		db.Close()
		t.Fatalf("harness.NewServer: create write token failed: %v", err)
	}

	readToken, _, err := tokenStore.Create(ctx, "e2e-reader", []string{"read"}, 0)
	if err != nil {
		db.Close()
		t.Fatalf("harness.NewServer: create read token failed: %v", err)
	}

	auditLog := audit.New(db.DB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	modelReg := models.NewRegistry()
	providerReg := providers.NewRegistry()
	pm := providers.NewManager(providerReg, modelReg, logger)

	// Register mock provider for deterministic offline testing
	mockCfg := config.ProviderConfig{
		ID:       "mock-default",
		Type:     "mock",
		Endpoint: "http://mock.internal",
		Timeout:  config.Duration(5 * time.Second),
	}
	_ = pm.Load(ctx, []config.ProviderConfig{mockCfg})

	router := routing.NewWithOptions(pm, routing.RouterOptions{
		DB:             db.DB,
		HealthInterval: 60 * time.Second,
	})

	agentStore := agents.NewStore(db.DB)
	agentService := agents.NewService(config.AgentConfig{
		MaxConcurrentRuns:   8,
		MaxConcurrentAgents: 16,
		MaxParallelPerRun:   4,
		MaxDelegationDepth:  5,
	}, agentStore, router, auditLog, logger)

	rt := core.New(logger)
	rt.Register(agentService)

	var opt ServerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	serverCfg := config.Server{
		Listen:          "127.0.0.1:0",
		Mode:            config.ModeLocal,
		RateLimitPerSec: 10000,
		RateLimitBurst:  20000,
		MaxBodyBytes:    10 * 1024 * 1024,
		ShutdownTimeout: config.Duration(5 * time.Second),
	}
	if !opt.LocalMode {
		serverCfg.Mode = config.ModeLocal
	}

	apiServer := api.New(api.Deps{
		Config:    serverCfg,
		Log:       logger,
		Tokens:    tokenStore,
		Audit:     auditLog,
		Providers: pm,
		Router:    router,
		Agents:    agentService,
		Health:    rt.Health,
		Version:   "0.1.0-e2e",
	})

	// Start httptest.Server wrapping the API handler
	ts := httptest.NewServer(apiServer.Handler())

	s := &Server{
		URL:        ts.URL,
		DB:         db,
		DBPath:     dbPath,
		AdminToken: adminToken,
		WriteToken: writeToken,
		ReadToken:  readToken,
		TokenStore: tokenStore,
		AuditLog:   auditLog,
		Agents:     agentService,
		Router:     router,
		Providers:  pm,
		httpSrv:    ts,
	}
	s.Client = NewClient(ts.URL).WithToken(adminToken)

	t.Cleanup(func() {
		s.Close()
	})

	return s
}

// Close terminates the server and cleans up database connections.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true

	if s.httpSrv != nil {
		s.httpSrv.Close()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.Agents != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.Agents.Stop(ctx)
		cancel()
	}
	if s.DB != nil {
		_ = s.DB.Close()
	}
}

// NewClient returns a new Client pointing to this server.
func (s *Server) NewClient() *Client {
	return NewClient(s.URL)
}

// AdminClient returns a new Client configured with the Admin bearer token.
func (s *Server) AdminClient() *Client {
	return NewClient(s.URL).WithToken(s.AdminToken)
}

// WriteClient returns a new Client configured with the Write bearer token.
func (s *Server) WriteClient() *Client {
	return NewClient(s.URL).WithToken(s.WriteToken)
}

// ReadClient returns a new Client configured with the Read bearer token.
func (s *Server) ReadClient() *Client {
	return NewClient(s.URL).WithToken(s.ReadToken)
}

// UnauthClient returns a new Client with no bearer token.
func (s *Server) UnauthClient() *Client {
	return NewClient(s.URL)
}
