package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_tools.db")
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}

	return NewStore(db.DB)
}

func TestStoreToolDefinitions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tool := ToolDefinition{
		ID:                   "tool-1",
		Name:                 "test_tool",
		Description:          "A tool for testing",
		Category:             CategorySystem,
		RiskLevel:            RiskLow,
		ParametersSchema:     json.RawMessage(`{"type":"object"}`),
		RequiredCapabilities: []string{"tools:system:read"},
		RequiredPermissions:  []string{"read"},
		Enabled:              true,
	}

	if err := store.SaveToolDefinition(ctx, tool); err != nil {
		t.Fatalf("failed to save tool definition: %v", err)
	}

	// Fetch by ID
	byID, err := store.GetToolDefinition(ctx, "tool-1")
	if err != nil {
		t.Fatalf("failed to get tool by ID: %v", err)
	}
	if byID.Name != "test_tool" || len(byID.RequiredCapabilities) != 1 {
		t.Fatalf("unexpected fetched tool: %+v", byID)
	}

	// Fetch by Name
	byName, err := store.GetToolDefinition(ctx, "test_tool")
	if err != nil {
		t.Fatalf("failed to get tool by Name: %v", err)
	}
	if byName.ID != "tool-1" {
		t.Fatalf("unexpected fetched tool ID: %s", byName.ID)
	}

	// Disable tool
	if err := store.SetToolEnabled(ctx, "tool-1", false); err != nil {
		t.Fatalf("failed to set tool disabled: %v", err)
	}
	disabled, err := store.GetToolDefinition(ctx, "tool-1")
	if err != nil || disabled.Enabled {
		t.Fatalf("expected tool to be disabled, got: %+v", disabled)
	}

	// List without disabled
	enabledList, err := store.ListToolDefinitions(ctx, false)
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}
	if len(enabledList) != 0 {
		t.Fatalf("expected 0 enabled tools, got %d", len(enabledList))
	}

	// List with disabled
	allList, err := store.ListToolDefinitions(ctx, true)
	if err != nil {
		t.Fatalf("failed to list all tools: %v", err)
	}
	if len(allList) != 1 {
		t.Fatalf("expected 1 tool in all list, got %d", len(allList))
	}

	// Delete
	if err := store.DeleteToolDefinition(ctx, "tool-1"); err != nil {
		t.Fatalf("failed to delete tool: %v", err)
	}
	_, err = store.GetToolDefinition(ctx, "tool-1")
	if err != ErrToolNotFound {
		t.Fatalf("expected ErrToolNotFound after delete, got %v", err)
	}
}

func TestStoreMCPServers(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	mcp := MCPServerRecord{
		ID:            "mcp-1",
		Name:          "filesystem-server",
		TransportType: "stdio",
		Command:       "npx",
		Args:          []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Env:           map[string]string{"NODE_ENV": "production"},
		Enabled:       true,
	}

	if err := store.SaveMCPServer(ctx, mcp); err != nil {
		t.Fatalf("failed to save MCP server: %v", err)
	}

	got, err := store.GetMCPServer(ctx, "mcp-1")
	if err != nil {
		t.Fatalf("failed to get MCP server: %v", err)
	}
	if got.Name != "filesystem-server" || len(got.Args) != 3 || got.Env["NODE_ENV"] != "production" {
		t.Fatalf("unexpected MCP server record: %+v", got)
	}

	list, err := store.ListMCPServers(ctx, true)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 MCP server in list, got %d (err: %v)", len(list), err)
	}

	if err := store.DeleteMCPServer(ctx, "mcp-1"); err != nil {
		t.Fatalf("failed to delete MCP server: %v", err)
	}
}

func TestStoreDeviceNodes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	dev := DeviceNodeRecord{
		ID:            "dev-1",
		Name:          "macbook-node",
		Status:        "pending",
		PairingCode:   "PAIR-1234",
		PublicKey:     "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA",
		AuthTokenHash: "hash-token-123",
		Capabilities:  []string{"device:screen:capture"},
		LastSeen:      &now,
	}

	if err := store.SaveDeviceNode(ctx, dev); err != nil {
		t.Fatalf("failed to save device node: %v", err)
	}

	byCode, err := store.GetDeviceNodeByPairingCode(ctx, "PAIR-1234")
	if err != nil {
		t.Fatalf("failed to get device node by pairing code: %v", err)
	}
	if byCode.ID != "dev-1" || len(byCode.Capabilities) != 1 {
		t.Fatalf("unexpected device node by code: %+v", byCode)
	}

	if err := store.UpdateDeviceStatus(ctx, "dev-1", "paired"); err != nil {
		t.Fatalf("failed to update device status: %v", err)
	}

	updated, err := store.GetDeviceNode(ctx, "dev-1")
	if err != nil || updated.Status != "paired" {
		t.Fatalf("expected status 'paired', got %+v (err: %v)", updated, err)
	}

	if err := store.DeleteDeviceNode(ctx, "dev-1"); err != nil {
		t.Fatalf("failed to delete device node: %v", err)
	}
}
