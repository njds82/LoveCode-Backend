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
	"github.com/lovecode/lovecode/internal/security"
	"github.com/lovecode/lovecode/internal/storage"
	"github.com/lovecode/lovecode/internal/tools"
)

type toolsFixture struct {
	srv      *Server
	tokens   *security.TokenStore
	auditLog *audit.Log
	toolsSvc *tools.Service
	admin    string
	reader   string
	writer   string
}

func setupToolsFixture(t *testing.T) *toolsFixture {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "tools_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	ts := security.NewTokenStore(db.DB)
	admin, _, _ := ts.Create(ctx, "admin-user", []string{security.ScopeAdmin}, 0)
	reader, _, _ := ts.Create(ctx, "reader-user", []string{"read"}, 0)
	writer, _, _ := ts.Create(ctx, "writer-user", []string{"read", "write"}, 0)

	auditLog := audit.New(db.DB)
	toolsStore := tools.NewStore(db.DB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolsSvc := tools.NewService(toolsStore, auditLog, logger)

	// Register sample tools
	_ = toolsSvc.Registry().RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:                   "fs_read",
		Name:                 "fs_read",
		Description:          "Read file content",
		Category:             tools.CategoryFilesystem,
		RiskLevel:            tools.RiskMedium,
		RequiredCapabilities: []string{"tools:fs:read"},
		RequiredPermissions:  []string{"read"},
		ParametersSchema: json.RawMessage(`{
			"type": "object",
			"required": ["path"],
			"properties": {
				"path": { "type": "string" }
			}
		}`),
		Enabled: true,
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &p)
		if p.Path == "/secret" {
			return &tools.ExecutionResult{
				Output: "credentials: sk-1234567890abcdef123456",
			}, nil
		}
		if p.Path == "/big" {
			return &tools.ExecutionResult{
				Output: strings.Repeat("X", 200),
			}, nil
		}
		return &tools.ExecutionResult{
			Output: "content of " + p.Path,
		}, nil
	}))

	_ = toolsSvc.Registry().RegisterTool(tools.WrapHandler(tools.ToolDefinition{
		ID:          "sys_info",
		Name:        "sys_info",
		Description: "System information",
		Category:    tools.CategorySystem,
		RiskLevel:   tools.RiskLow,
		Enabled:     false, // disabled
	}, func(ctx context.Context, execCtx tools.ExecutionContext, args json.RawMessage) (*tools.ExecutionResult, error) {
		return &tools.ExecutionResult{Output: "linux amd64"}, nil
	}))

	cfg := config.Defaults().Server
	s := New(Deps{
		Config: cfg,
		Log:    logger,
		Tokens: ts,
		Audit:  auditLog,
		Tools:  toolsSvc,
	})

	return &toolsFixture{
		srv:      s,
		tokens:   ts,
		auditLog: auditLog,
		toolsSvc: toolsSvc,
		admin:    admin,
		reader:   reader,
		writer:   writer,
	}
}

func (f *toolsFixture) request(method, path, token string, body any) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.RemoteAddr = "127.0.0.1:9999"
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

func TestToolsAPIListAndGet(t *testing.T) {
	f := setupToolsFixture(t)

	t.Run("Unauthorized Without Token", func(t *testing.T) {
		rec := f.request("GET", "/v1/tools", "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("List Enabled Tools Only By Default", func(t *testing.T) {
		rec := f.request("GET", "/v1/tools", f.reader, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var toolsList []tools.ToolDefinition
		if err := json.Unmarshal(rec.Body.Bytes(), &toolsList); err != nil {
			t.Fatal(err)
		}
		if len(toolsList) != 1 || toolsList[0].Name != "fs_read" {
			t.Fatalf("expected only enabled tool fs_read, got: %+v", toolsList)
		}
	})

	t.Run("List All Tools With all=true", func(t *testing.T) {
		rec := f.request("GET", "/v1/tools?all=true", f.reader, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var toolsList []tools.ToolDefinition
		_ = json.Unmarshal(rec.Body.Bytes(), &toolsList)
		if len(toolsList) != 2 {
			t.Fatalf("expected 2 tools, got %d", len(toolsList))
		}
	})

	t.Run("Filter By Category", func(t *testing.T) {
		rec := f.request("GET", "/v1/tools?category=filesystem", f.reader, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var toolsList []tools.ToolDefinition
		_ = json.Unmarshal(rec.Body.Bytes(), &toolsList)
		if len(toolsList) != 1 || toolsList[0].Name != "fs_read" {
			t.Fatalf("expected 1 filesystem tool, got %d", len(toolsList))
		}
	})

	t.Run("Get Tool Details", func(t *testing.T) {
		rec := f.request("GET", "/v1/tools/fs_read", f.reader, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var def tools.ToolDefinition
		_ = json.Unmarshal(rec.Body.Bytes(), &def)
		if def.Name != "fs_read" || def.Category != tools.CategoryFilesystem {
			t.Fatalf("unexpected tool details: %+v", def)
		}
	})

	t.Run("Get Non-Existent Tool", func(t *testing.T) {
		rec := f.request("GET", "/v1/tools/missing", f.reader, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})
}

func TestToolsAPIPatch(t *testing.T) {
	f := setupToolsFixture(t)

	t.Run("Patch Tool Requires Admin", func(t *testing.T) {
		rec := f.request("PATCH", "/v1/tools/fs_read", f.writer, map[string]any{"enabled": false})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for non-admin, got %d", rec.Code)
		}
	})

	t.Run("Patch Tool Successfully With Admin", func(t *testing.T) {
		rec := f.request("PATCH", "/v1/tools/fs_read", f.admin, map[string]any{"enabled": false})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var def tools.ToolDefinition
		_ = json.Unmarshal(rec.Body.Bytes(), &def)
		if def.Enabled {
			t.Fatal("expected enabled=false")
		}

		// Verify disabled status in registry
		h, _ := f.toolsSvc.Registry().GetTool("fs_read")
		if h.Definition().Enabled {
			t.Fatal("expected registry tool to be disabled")
		}
	})
}

func TestToolsAPIExecute(t *testing.T) {
	f := setupToolsFixture(t)

	t.Run("Admin Auto-Grant Execution", func(t *testing.T) {
		rec := f.request("POST", "/v1/tools/execute", f.admin, map[string]any{
			"tool": "fs_read",
			"arguments": map[string]any{
				"path": "/etc/hosts",
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var res struct {
			ToolName string `json:"tool_name"`
			Output   string `json:"output"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res.ToolName != "fs_read" || res.Output != "content of /etc/hosts" {
			t.Fatalf("unexpected execution output: %+v", res)
		}
	})

	t.Run("Capability Denied For Non-Admin Missing Capability", func(t *testing.T) {
		rec := f.request("POST", "/v1/tools/execute", f.writer, map[string]any{
			"tool": "fs_read",
			"arguments": map[string]any{
				"path": "/etc/hosts",
			},
			"capabilities": []string{"tools:other:capability"},
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for missing capability, got %d", rec.Code)
		}
	})

	t.Run("Execution With Granted Capability", func(t *testing.T) {
		rec := f.request("POST", "/v1/tools/execute", f.writer, map[string]any{
			"tool": "fs_read",
			"arguments": map[string]any{
				"path": "/data/test.txt",
			},
			"capabilities": []string{"tools:fs:read"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Schema Validation Failure", func(t *testing.T) {
		rec := f.request("POST", "/v1/tools/execute", f.admin, map[string]any{
			"tool":      "fs_read",
			"arguments": map[string]any{}, // missing "path"
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for schema error, got %d", rec.Code)
		}
	})

	t.Run("Output Truncation", func(t *testing.T) {
		rec := f.request("POST", "/v1/tools/execute", f.admin, map[string]any{
			"tool": "fs_read",
			"arguments": map[string]any{
				"path": "/big",
			},
			"max_output_bytes": 50,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var res struct {
			Truncated bool   `json:"truncated"`
			Output    string `json:"output"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if !res.Truncated || len(res.Output) != 50 {
			t.Fatalf("expected truncated output of length 50, got %+v", res)
		}
	})

	t.Run("Secret Redaction In Execution Output", func(t *testing.T) {
		rec := f.request("POST", "/v1/tools/execute", f.admin, map[string]any{
			"tool": "fs_read",
			"arguments": map[string]any{
				"path": "/secret",
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var res struct {
			Output string `json:"output"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if strings.Contains(res.Output, "sk-123456") {
			t.Fatalf("secret leaked in API output: %s", res.Output)
		}
		if !strings.Contains(res.Output, "[REDACTED]") {
			t.Fatalf("expected [REDACTED] in output, got: %s", res.Output)
		}
	})
}
