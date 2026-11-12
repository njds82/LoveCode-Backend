package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Store manages SQLite persistence for tool definitions, MCP configurations, and device nodes.
type Store struct {
	db *sql.DB
}

// NewStore constructs a Store backed by the provided SQLite database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// SaveToolDefinition inserts or updates a tool definition in SQLite.
func (s *Store) SaveToolDefinition(ctx context.Context, def ToolDefinition) error {
	if def.ID == "" {
		return errors.New("tool definition ID cannot be empty")
	}
	if def.Name == "" {
		return errors.New("tool definition Name cannot be empty")
	}
	if def.CreatedAt.IsZero() {
		def.CreatedAt = time.Now().UTC()
	}
	if def.UpdatedAt.IsZero() {
		def.UpdatedAt = time.Now().UTC()
	}

	capsJSON, err := json.Marshal(def.RequiredCapabilities)
	if err != nil {
		capsJSON = []byte("[]")
	}
	permsJSON, err := json.Marshal(def.RequiredPermissions)
	if err != nil {
		permsJSON = []byte("[]")
	}
	schemaJSON := def.ParametersSchema
	if len(schemaJSON) == 0 {
		schemaJSON = []byte("{}")
	}

	enabledInt := 0
	if def.Enabled {
		enabledInt = 1
	}

	query := `
		INSERT INTO tool_definitions (
			id, name, description, category, risk_level,
			parameters_schema, required_capabilities, required_permissions,
			enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			category = excluded.category,
			risk_level = excluded.risk_level,
			parameters_schema = excluded.parameters_schema,
			required_capabilities = excluded.required_capabilities,
			required_permissions = excluded.required_permissions,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at
	`
	_, err = s.db.ExecContext(ctx, query,
		def.ID, def.Name, def.Description, def.Category, string(def.RiskLevel),
		string(schemaJSON), string(capsJSON), string(permsJSON),
		enabledInt, def.CreatedAt.Format(time.RFC3339Nano), def.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetToolDefinition retrieves a tool definition by ID or name.
func (s *Store) GetToolDefinition(ctx context.Context, idOrName string) (*ToolDefinition, error) {
	query := `
		SELECT id, name, description, category, risk_level,
		       parameters_schema, required_capabilities, required_permissions,
		       enabled, created_at, updated_at
		FROM tool_definitions
		WHERE id = ? OR name = ?
	`
	row := s.db.QueryRowContext(ctx, query, idOrName, idOrName)

	var def ToolDefinition
	var riskStr, schemaStr, capsStr, permsStr, createdStr, updatedStr string
	var enabledInt int

	err := row.Scan(
		&def.ID, &def.Name, &def.Description, &def.Category, &riskStr,
		&schemaStr, &capsStr, &permsStr, &enabledInt, &createdStr, &updatedStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrToolNotFound
		}
		return nil, err
	}

	def.RiskLevel = RiskLevel(riskStr)
	def.ParametersSchema = json.RawMessage(schemaStr)
	_ = json.Unmarshal([]byte(capsStr), &def.RequiredCapabilities)
	_ = json.Unmarshal([]byte(permsStr), &def.RequiredPermissions)
	def.Enabled = (enabledInt == 1)
	def.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	def.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)

	return &def, nil
}

// ListToolDefinitions returns all stored tool definitions.
func (s *Store) ListToolDefinitions(ctx context.Context, includeDisabled bool) ([]ToolDefinition, error) {
	query := `
		SELECT id, name, description, category, risk_level,
		       parameters_schema, required_capabilities, required_permissions,
		       enabled, created_at, updated_at
		FROM tool_definitions
	`
	if !includeDisabled {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY name ASC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ToolDefinition
	for rows.Next() {
		var def ToolDefinition
		var riskStr, schemaStr, capsStr, permsStr, createdStr, updatedStr string
		var enabledInt int

		err := rows.Scan(
			&def.ID, &def.Name, &def.Description, &def.Category, &riskStr,
			&schemaStr, &capsStr, &permsStr, &enabledInt, &createdStr, &updatedStr,
		)
		if err != nil {
			return nil, err
		}

		def.RiskLevel = RiskLevel(riskStr)
		def.ParametersSchema = json.RawMessage(schemaStr)
		_ = json.Unmarshal([]byte(capsStr), &def.RequiredCapabilities)
		_ = json.Unmarshal([]byte(permsStr), &def.RequiredPermissions)
		def.Enabled = (enabledInt == 1)
		def.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		def.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)

		list = append(list, def)
	}

	return list, rows.Err()
}

// SetToolEnabled updates the enabled flag for a tool definition.
func (s *Store) SetToolEnabled(ctx context.Context, idOrName string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	query := `
		UPDATE tool_definitions
		SET enabled = ?, updated_at = ?
		WHERE id = ? OR name = ?
	`
	res, err := s.db.ExecContext(ctx, query, enabledInt, time.Now().UTC().Format(time.RFC3339Nano), idOrName, idOrName)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrToolNotFound
	}
	return nil
}

// DeleteToolDefinition removes a tool definition from SQLite.
func (s *Store) DeleteToolDefinition(ctx context.Context, idOrName string) error {
	query := `DELETE FROM tool_definitions WHERE id = ? OR name = ?`
	res, err := s.db.ExecContext(ctx, query, idOrName, idOrName)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrToolNotFound
	}
	return nil
}

// MCPServer persistence

// SaveMCPServer creates or updates an MCP server configuration.
func (s *Store) SaveMCPServer(ctx context.Context, mcp MCPServerRecord) error {
	if mcp.ID == "" {
		return errors.New("MCP server ID cannot be empty")
	}
	if mcp.CreatedAt.IsZero() {
		mcp.CreatedAt = time.Now().UTC()
	}
	if mcp.UpdatedAt.IsZero() {
		mcp.UpdatedAt = time.Now().UTC()
	}

	argsJSON, err := json.Marshal(mcp.Args)
	if err != nil {
		argsJSON = []byte("[]")
	}
	envJSON, err := json.Marshal(mcp.Env)
	if err != nil {
		envJSON = []byte("{}")
	}

	enabledInt := 0
	if mcp.Enabled {
		enabledInt = 1
	}

	query := `
		INSERT INTO mcp_servers (
			id, name, transport_type, uri, command,
			args_json, env_json, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			transport_type = excluded.transport_type,
			uri = excluded.uri,
			command = excluded.command,
			args_json = excluded.args_json,
			env_json = excluded.env_json,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at
	`
	_, err = s.db.ExecContext(ctx, query,
		mcp.ID, mcp.Name, mcp.TransportType, mcp.URI, mcp.Command,
		string(argsJSON), string(envJSON), enabledInt,
		mcp.CreatedAt.Format(time.RFC3339Nano), mcp.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetMCPServer fetches an MCP server by ID.
func (s *Store) GetMCPServer(ctx context.Context, id string) (*MCPServerRecord, error) {
	query := `
		SELECT id, name, transport_type, uri, command,
		       args_json, env_json, enabled, created_at, updated_at
		FROM mcp_servers
		WHERE id = ?
	`
	var mcp MCPServerRecord
	var argsStr, envStr, createdStr, updatedStr string
	var enabledInt int

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&mcp.ID, &mcp.Name, &mcp.TransportType, &mcp.URI, &mcp.Command,
		&argsStr, &envStr, &enabledInt, &createdStr, &updatedStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("mcp server not found")
		}
		return nil, err
	}

	_ = json.Unmarshal([]byte(argsStr), &mcp.Args)
	_ = json.Unmarshal([]byte(envStr), &mcp.Env)
	mcp.Enabled = (enabledInt == 1)
	mcp.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	mcp.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)

	return &mcp, nil
}

// ListMCPServers returns all MCP server configs.
func (s *Store) ListMCPServers(ctx context.Context, includeDisabled bool) ([]MCPServerRecord, error) {
	query := `
		SELECT id, name, transport_type, uri, command,
		       args_json, env_json, enabled, created_at, updated_at
		FROM mcp_servers
	`
	if !includeDisabled {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY name ASC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []MCPServerRecord
	for rows.Next() {
		var mcp MCPServerRecord
		var argsStr, envStr, createdStr, updatedStr string
		var enabledInt int

		err := rows.Scan(
			&mcp.ID, &mcp.Name, &mcp.TransportType, &mcp.URI, &mcp.Command,
			&argsStr, &envStr, &enabledInt, &createdStr, &updatedStr,
		)
		if err != nil {
			return nil, err
		}

		_ = json.Unmarshal([]byte(argsStr), &mcp.Args)
		_ = json.Unmarshal([]byte(envStr), &mcp.Env)
		mcp.Enabled = (enabledInt == 1)
		mcp.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		mcp.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)

		list = append(list, mcp)
	}
	return list, rows.Err()
}

// DeleteMCPServer deletes an MCP server configuration.
func (s *Store) DeleteMCPServer(ctx context.Context, id string) error {
	query := `DELETE FROM mcp_servers WHERE id = ?`
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("mcp server not found")
	}
	return nil
}

// Device Node persistence

// SaveDeviceNode inserts or updates a DeviceNodeRecord.
func (s *Store) SaveDeviceNode(ctx context.Context, dev DeviceNodeRecord) error {
	if dev.ID == "" {
		return errors.New("device node ID cannot be empty")
	}
	if dev.CreatedAt.IsZero() {
		dev.CreatedAt = time.Now().UTC()
	}
	if dev.UpdatedAt.IsZero() {
		dev.UpdatedAt = time.Now().UTC()
	}

	capsJSON, err := json.Marshal(dev.Capabilities)
	if err != nil {
		capsJSON = []byte("[]")
	}

	var lastSeenStr *string
	if dev.LastSeen != nil {
		v := dev.LastSeen.UTC().Format(time.RFC3339Nano)
		lastSeenStr = &v
	}

	query := `
		INSERT INTO device_nodes (
			id, name, status, pairing_code, public_key,
			auth_token_hash, capabilities_json, last_seen,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			status = excluded.status,
			pairing_code = excluded.pairing_code,
			public_key = excluded.public_key,
			auth_token_hash = excluded.auth_token_hash,
			capabilities_json = excluded.capabilities_json,
			last_seen = excluded.last_seen,
			updated_at = excluded.updated_at
	`
	_, err = s.db.ExecContext(ctx, query,
		dev.ID, dev.Name, dev.Status, dev.PairingCode, dev.PublicKey,
		dev.AuthTokenHash, string(capsJSON), lastSeenStr,
		dev.CreatedAt.Format(time.RFC3339Nano), dev.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

// GetDeviceNode fetches a device node by ID.
func (s *Store) GetDeviceNode(ctx context.Context, id string) (*DeviceNodeRecord, error) {
	query := `
		SELECT id, name, status, pairing_code, public_key,
		       auth_token_hash, capabilities_json, last_seen,
		       created_at, updated_at
		FROM device_nodes
		WHERE id = ?
	`
	var dev DeviceNodeRecord
	var capsStr, createdStr, updatedStr string
	var lastSeenStr *string

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&dev.ID, &dev.Name, &dev.Status, &dev.PairingCode, &dev.PublicKey,
		&dev.AuthTokenHash, &capsStr, &lastSeenStr,
		&createdStr, &updatedStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("device node not found")
		}
		return nil, err
	}

	_ = json.Unmarshal([]byte(capsStr), &dev.Capabilities)
	dev.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	dev.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
	if lastSeenStr != nil {
		t, _ := time.Parse(time.RFC3339Nano, *lastSeenStr)
		dev.LastSeen = &t
	}

	return &dev, nil
}

// GetDeviceNodeByPairingCode retrieves a device node by its ephemeral pairing code.
func (s *Store) GetDeviceNodeByPairingCode(ctx context.Context, code string) (*DeviceNodeRecord, error) {
	query := `
		SELECT id, name, status, pairing_code, public_key,
		       auth_token_hash, capabilities_json, last_seen,
		       created_at, updated_at
		FROM device_nodes
		WHERE pairing_code = ?
	`
	var dev DeviceNodeRecord
	var capsStr, createdStr, updatedStr string
	var lastSeenStr *string

	err := s.db.QueryRowContext(ctx, query, code).Scan(
		&dev.ID, &dev.Name, &dev.Status, &dev.PairingCode, &dev.PublicKey,
		&dev.AuthTokenHash, &capsStr, &lastSeenStr,
		&createdStr, &updatedStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("device node not found")
		}
		return nil, err
	}

	_ = json.Unmarshal([]byte(capsStr), &dev.Capabilities)
	dev.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
	dev.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
	if lastSeenStr != nil {
		t, _ := time.Parse(time.RFC3339Nano, *lastSeenStr)
		dev.LastSeen = &t
	}

	return &dev, nil
}

// ListDeviceNodes returns all registered device nodes.
func (s *Store) ListDeviceNodes(ctx context.Context) ([]DeviceNodeRecord, error) {
	query := `
		SELECT id, name, status, pairing_code, public_key,
		       auth_token_hash, capabilities_json, last_seen,
		       created_at, updated_at
		FROM device_nodes
		ORDER BY created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []DeviceNodeRecord
	for rows.Next() {
		var dev DeviceNodeRecord
		var capsStr, createdStr, updatedStr string
		var lastSeenStr *string

		err := rows.Scan(
			&dev.ID, &dev.Name, &dev.Status, &dev.PairingCode, &dev.PublicKey,
			&dev.AuthTokenHash, &capsStr, &lastSeenStr,
			&createdStr, &updatedStr,
		)
		if err != nil {
			return nil, err
		}

		_ = json.Unmarshal([]byte(capsStr), &dev.Capabilities)
		dev.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdStr)
		dev.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedStr)
		if lastSeenStr != nil {
			t, _ := time.Parse(time.RFC3339Nano, *lastSeenStr)
			dev.LastSeen = &t
		}

		list = append(list, dev)
	}
	return list, rows.Err()
}

// UpdateDeviceStatus updates the status and last seen timestamp of a device node.
func (s *Store) UpdateDeviceStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := `
		UPDATE device_nodes
		SET status = ?, last_seen = ?, updated_at = ?
		WHERE id = ?
	`
	res, err := s.db.ExecContext(ctx, query, status, now, now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("device node not found")
	}
	return nil
}

// DeleteDeviceNode removes a device node.
func (s *Store) DeleteDeviceNode(ctx context.Context, id string) error {
	query := `DELETE FROM device_nodes WHERE id = ?`
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("device node not found")
	}
	return nil
}
