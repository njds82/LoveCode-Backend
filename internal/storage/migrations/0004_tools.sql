-- Phase 5 schema: tool definitions, MCP server configurations, and device nodes.

CREATE TABLE tool_definitions (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL UNIQUE,
    description           TEXT NOT NULL DEFAULT '',
    category              TEXT NOT NULL DEFAULT 'system',
    risk_level            TEXT NOT NULL DEFAULT 'low',
    parameters_schema     TEXT NOT NULL DEFAULT '{}',
    required_capabilities TEXT NOT NULL DEFAULT '[]',
    required_permissions  TEXT NOT NULL DEFAULT '[]',
    enabled               INTEGER NOT NULL DEFAULT 1,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);
CREATE INDEX idx_tools_name ON tool_definitions(name);
CREATE INDEX idx_tools_category ON tool_definitions(category);
CREATE INDEX idx_tools_risk_level ON tool_definitions(risk_level);
CREATE INDEX idx_tools_enabled ON tool_definitions(enabled);

CREATE TABLE mcp_servers (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    transport_type TEXT NOT NULL DEFAULT 'stdio', -- stdio, http, sse
    uri            TEXT NOT NULL DEFAULT '',
    command        TEXT NOT NULL DEFAULT '',
    args_json      TEXT NOT NULL DEFAULT '[]',
    env_json       TEXT NOT NULL DEFAULT '{}',
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_mcp_servers_enabled ON mcp_servers(enabled);
CREATE INDEX idx_mcp_servers_transport ON mcp_servers(transport_type);

CREATE TABLE device_nodes (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'pending', -- pending, paired, revoked, offline
    pairing_code      TEXT NOT NULL DEFAULT '',
    public_key        TEXT NOT NULL DEFAULT '',
    auth_token_hash   TEXT NOT NULL DEFAULT '',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    last_seen         TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
CREATE INDEX idx_device_nodes_status ON device_nodes(status);
CREATE INDEX idx_device_nodes_pairing ON device_nodes(pairing_code);
CREATE INDEX idx_device_nodes_token ON device_nodes(auth_token_hash);
