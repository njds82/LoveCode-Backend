package agents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// SaveAgent creates or overwrites an agent definition.
func (s *Store) SaveAgent(ctx context.Context, a AgentDefinition) error {
	paramsJSON, err := json.Marshal(a.Params)
	if err != nil {
		paramsJSON = []byte("{}")
	}
	capsJSON, err := json.Marshal(a.Capabilities)
	if err != nil {
		capsJSON = []byte("[]")
	}
	permsJSON, err := json.Marshal(a.Permissions)
	if err != nil {
		permsJSON = []byte("[]")
	}

	enabledInt := 0
	if a.Enabled {
		enabledInt = 1
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	a.UpdatedAt = time.Now().UTC()

	query := `
		INSERT INTO agent_definitions (
			id, name, description, role, system_prompt, model, routing_profile,
			params_json, max_tokens, timeout_ms, max_requests, autonomy_level,
			context_policy, capabilities_json, permissions_json, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			role = excluded.role,
			system_prompt = excluded.system_prompt,
			model = excluded.model,
			routing_profile = excluded.routing_profile,
			params_json = excluded.params_json,
			max_tokens = excluded.max_tokens,
			timeout_ms = excluded.timeout_ms,
			max_requests = excluded.max_requests,
			autonomy_level = excluded.autonomy_level,
			context_policy = excluded.context_policy,
			capabilities_json = excluded.capabilities_json,
			permissions_json = excluded.permissions_json,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at
	`
	_, err = s.db.ExecContext(ctx, query,
		a.ID, a.Name, a.Description, a.Role, a.SystemPrompt, a.Model, a.RoutingProfile,
		string(paramsJSON), a.MaxTokens, a.Timeout.Milliseconds(), a.MaxRequests,
		string(a.AutonomyLevel), string(a.ContextPolicy), string(capsJSON), string(permsJSON),
		enabledInt, a.CreatedAt.Format(time.RFC3339), now,
	)
	return err
}

func (s *Store) GetAgent(ctx context.Context, id string) (*AgentDefinition, error) {
	query := `
		SELECT id, name, description, role, system_prompt, model, routing_profile,
		       params_json, max_tokens, timeout_ms, max_requests, autonomy_level,
		       context_policy, capabilities_json, permissions_json, enabled, created_at, updated_at
		FROM agent_definitions WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var a AgentDefinition
	var paramsRaw, capsRaw, permsRaw, createdStr, updatedStr string
	var timeoutMS int64
	var enabledInt int

	err := row.Scan(
		&a.ID, &a.Name, &a.Description, &a.Role, &a.SystemPrompt, &a.Model, &a.RoutingProfile,
		&paramsRaw, &a.MaxTokens, &timeoutMS, &a.MaxRequests, &a.AutonomyLevel,
		&a.ContextPolicy, &capsRaw, &permsRaw, &enabledInt, &createdStr, &updatedStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	a.Timeout = time.Duration(timeoutMS) * time.Millisecond
	a.Enabled = enabledInt == 1
	_ = json.Unmarshal([]byte(paramsRaw), &a.Params)
	_ = json.Unmarshal([]byte(capsRaw), &a.Capabilities)
	_ = json.Unmarshal([]byte(permsRaw), &a.Permissions)
	a.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &a, nil
}

func (s *Store) ListAgents(ctx context.Context, includeDisabled bool) ([]AgentDefinition, error) {
	query := `
		SELECT id, name, description, role, system_prompt, model, routing_profile,
		       params_json, max_tokens, timeout_ms, max_requests, autonomy_level,
		       context_policy, capabilities_json, permissions_json, enabled, created_at, updated_at
		FROM agent_definitions
	`
	if !includeDisabled {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id ASC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []AgentDefinition
	for rows.Next() {
		var a AgentDefinition
		var paramsRaw, capsRaw, permsRaw, createdStr, updatedStr string
		var timeoutMS int64
		var enabledInt int

		if err := rows.Scan(
			&a.ID, &a.Name, &a.Description, &a.Role, &a.SystemPrompt, &a.Model, &a.RoutingProfile,
			&paramsRaw, &a.MaxTokens, &timeoutMS, &a.MaxRequests, &a.AutonomyLevel,
			&a.ContextPolicy, &capsRaw, &permsRaw, &enabledInt, &createdStr, &updatedStr,
		); err != nil {
			return nil, err
		}
		a.Timeout = time.Duration(timeoutMS) * time.Millisecond
		a.Enabled = enabledInt == 1
		_ = json.Unmarshal([]byte(paramsRaw), &a.Params)
		_ = json.Unmarshal([]byte(capsRaw), &a.Capabilities)
		_ = json.Unmarshal([]byte(permsRaw), &a.Permissions)
		a.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		a.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		list = append(list, a)
	}
	return list, rows.Err()
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM agent_definitions WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveTeam creates or overwrites a team definition.
func (s *Store) SaveTeam(ctx context.Context, t TeamDefinition) error {
	membersJSON, err := json.Marshal(t.Members)
	if err != nil {
		membersJSON = []byte("[]")
	}
	orderJSON, err := json.Marshal(t.ExecutionOrder)
	if err != nil {
		orderJSON = []byte("[]")
	}
	depsJSON, err := json.Marshal(t.Dependencies)
	if err != nil {
		depsJSON = []byte("{}")
	}

	enabledInt := 0
	if t.Enabled {
		enabledInt = 1
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.UpdatedAt = time.Now().UTC()

	query := `
		INSERT INTO team_definitions (
			id, name, description, strategy, members_json, execution_order_json,
			dependencies_json, context_policy, aggregation_policy, failure_policy,
			synthesizer_agent_id, max_parallel, timeout_ms, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			strategy = excluded.strategy,
			members_json = excluded.members_json,
			execution_order_json = excluded.execution_order_json,
			dependencies_json = excluded.dependencies_json,
			context_policy = excluded.context_policy,
			aggregation_policy = excluded.aggregation_policy,
			failure_policy = excluded.failure_policy,
			synthesizer_agent_id = excluded.synthesizer_agent_id,
			max_parallel = excluded.max_parallel,
			timeout_ms = excluded.timeout_ms,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at
	`
	_, err = s.db.ExecContext(ctx, query,
		t.ID, t.Name, t.Description, string(t.Strategy), string(membersJSON), string(orderJSON),
		string(depsJSON), string(t.ContextPolicy), string(t.AggregationPolicy), string(t.FailurePolicy),
		t.SynthesizerAgentID, t.MaxParallel, t.Timeout.Milliseconds(), enabledInt,
		t.CreatedAt.Format(time.RFC3339), now,
	)
	return err
}

func (s *Store) GetTeam(ctx context.Context, id string) (*TeamDefinition, error) {
	query := `
		SELECT id, name, description, strategy, members_json, execution_order_json,
		       dependencies_json, context_policy, aggregation_policy, failure_policy,
		       synthesizer_agent_id, max_parallel, timeout_ms, enabled, created_at, updated_at
		FROM team_definitions WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var t TeamDefinition
	var membersRaw, orderRaw, depsRaw, createdStr, updatedStr string
	var timeoutMS int64
	var enabledInt int

	err := row.Scan(
		&t.ID, &t.Name, &t.Description, &t.Strategy, &membersRaw, &orderRaw,
		&depsRaw, &t.ContextPolicy, &t.AggregationPolicy, &t.FailurePolicy,
		&t.SynthesizerAgentID, &t.MaxParallel, &timeoutMS, &enabledInt, &createdStr, &updatedStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	t.Timeout = time.Duration(timeoutMS) * time.Millisecond
	t.Enabled = enabledInt == 1
	_ = json.Unmarshal([]byte(membersRaw), &t.Members)
	_ = json.Unmarshal([]byte(orderRaw), &t.ExecutionOrder)
	_ = json.Unmarshal([]byte(depsRaw), &t.Dependencies)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return &t, nil
}

func (s *Store) ListTeams(ctx context.Context, includeDisabled bool) ([]TeamDefinition, error) {
	query := `
		SELECT id, name, description, strategy, members_json, execution_order_json,
		       dependencies_json, context_policy, aggregation_policy, failure_policy,
		       synthesizer_agent_id, max_parallel, timeout_ms, enabled, created_at, updated_at
		FROM team_definitions
	`
	if !includeDisabled {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY id ASC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TeamDefinition
	for rows.Next() {
		var t TeamDefinition
		var membersRaw, orderRaw, depsRaw, createdStr, updatedStr string
		var timeoutMS int64
		var enabledInt int

		if err := rows.Scan(
			&t.ID, &t.Name, &t.Description, &t.Strategy, &membersRaw, &orderRaw,
			&depsRaw, &t.ContextPolicy, &t.AggregationPolicy, &t.FailurePolicy,
			&t.SynthesizerAgentID, &t.MaxParallel, &timeoutMS, &enabledInt, &createdStr, &updatedStr,
		); err != nil {
			return nil, err
		}
		t.Timeout = time.Duration(timeoutMS) * time.Millisecond
		t.Enabled = enabledInt == 1
		_ = json.Unmarshal([]byte(membersRaw), &t.Members)
		_ = json.Unmarshal([]byte(orderRaw), &t.ExecutionOrder)
		_ = json.Unmarshal([]byte(depsRaw), &t.Dependencies)
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
		list = append(list, t)
	}
	return list, rows.Err()
}

func (s *Store) DeleteTeam(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM team_definitions WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Runs and Tasks

func (s *Store) CreateRun(ctx context.Context, r RunRecord) error {
	structJSON, err := json.Marshal(r.StructuredOutput)
	if err != nil {
		structJSON = []byte("{}")
	}
	cfgJSON, err := json.Marshal(r.ConfigSnapshot)
	if err != nil {
		cfgJSON = []byte("{}")
	}

	var startedStr, completedStr *string
	if r.StartedAt != nil {
		val := r.StartedAt.UTC().Format(time.RFC3339)
		startedStr = &val
	}
	if r.CompletedAt != nil {
		val := r.CompletedAt.UTC().Format(time.RFC3339)
		completedStr = &val
	}

	query := `
		INSERT INTO agent_runs (
			id, run_type, agent_id, team_id, status, input_context, output_result,
			structured_output_json, error_message, prompt_tokens, completion_tokens,
			total_tokens, config_snapshot_json, created_at, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.ExecContext(ctx, query,
		r.ID, r.RunType, r.AgentID, r.TeamID, string(r.Status), r.InputContext, r.OutputResult,
		string(structJSON), r.ErrorMessage, r.PromptTokens, r.CompletionTokens, r.TotalTokens,
		string(cfgJSON), r.CreatedAt.UTC().Format(time.RFC3339), startedStr, completedStr,
	)
	return err
}

func (s *Store) UpdateRun(ctx context.Context, r RunRecord) error {
	structJSON, err := json.Marshal(r.StructuredOutput)
	if err != nil {
		structJSON = []byte("{}")
	}
	var startedStr, completedStr *string
	if r.StartedAt != nil {
		val := r.StartedAt.UTC().Format(time.RFC3339)
		startedStr = &val
	}
	if r.CompletedAt != nil {
		val := r.CompletedAt.UTC().Format(time.RFC3339)
		completedStr = &val
	}

	query := `
		UPDATE agent_runs SET
			status = ?,
			output_result = ?,
			structured_output_json = ?,
			error_message = ?,
			prompt_tokens = ?,
			completion_tokens = ?,
			total_tokens = ?,
			started_at = ?,
			completed_at = ?
		WHERE id = ?
	`
	_, err = s.db.ExecContext(ctx, query,
		string(r.Status), r.OutputResult, string(structJSON), r.ErrorMessage,
		r.PromptTokens, r.CompletionTokens, r.TotalTokens, startedStr, completedStr, r.ID,
	)
	return err
}

func (s *Store) GetRun(ctx context.Context, id string) (*RunRecord, error) {
	query := `
		SELECT id, run_type, agent_id, team_id, status, input_context, output_result,
		       structured_output_json, error_message, prompt_tokens, completion_tokens,
		       total_tokens, config_snapshot_json, created_at, started_at, completed_at
		FROM agent_runs WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var r RunRecord
	var structRaw, cfgRaw, createdStr string
	var startedStr, completedStr sql.NullString

	err := row.Scan(
		&r.ID, &r.RunType, &r.AgentID, &r.TeamID, &r.Status, &r.InputContext, &r.OutputResult,
		&structRaw, &r.ErrorMessage, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
		&cfgRaw, &createdStr, &startedStr, &completedStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(structRaw), &r.StructuredOutput)
	_ = json.Unmarshal([]byte(cfgRaw), &r.ConfigSnapshot)
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if startedStr.Valid {
		t, _ := time.Parse(time.RFC3339, startedStr.String)
		r.StartedAt = &t
	}
	if completedStr.Valid {
		t, _ := time.Parse(time.RFC3339, completedStr.String)
		r.CompletedAt = &t
	}
	return &r, nil
}

func (s *Store) ListRuns(ctx context.Context, limit int, offset int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT id, run_type, agent_id, team_id, status, input_context, output_result,
		       structured_output_json, error_message, prompt_tokens, completion_tokens,
		       total_tokens, config_snapshot_json, created_at, started_at, completed_at
		FROM agent_runs ORDER BY created_at DESC LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []RunRecord
	for rows.Next() {
		var r RunRecord
		var structRaw, cfgRaw, createdStr string
		var startedStr, completedStr sql.NullString

		if err := rows.Scan(
			&r.ID, &r.RunType, &r.AgentID, &r.TeamID, &r.Status, &r.InputContext, &r.OutputResult,
			&structRaw, &r.ErrorMessage, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&cfgRaw, &createdStr, &startedStr, &completedStr,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(structRaw), &r.StructuredOutput)
		_ = json.Unmarshal([]byte(cfgRaw), &r.ConfigSnapshot)
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		if startedStr.Valid {
			t, _ := time.Parse(time.RFC3339, startedStr.String)
			r.StartedAt = &t
		}
		if completedStr.Valid {
			t, _ := time.Parse(time.RFC3339, completedStr.String)
			r.CompletedAt = &t
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

func (s *Store) CreateTask(ctx context.Context, t TaskRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	structJSON, err := json.Marshal(t.StructuredOutput)
	if err != nil {
		structJSON = []byte("{}")
	}

	var startedStr, completedStr *string
	if t.StartedAt != nil {
		val := t.StartedAt.UTC().Format(time.RFC3339)
		startedStr = &val
	}
	if t.CompletedAt != nil {
		val := t.CompletedAt.UTC().Format(time.RFC3339)
		completedStr = &val
	}

	query := `
		INSERT INTO agent_tasks (
			id, run_id, parent_task_id, agent_id, name, description, status,
			priority, timeout_ms, retry_count, max_retries, input_data, output_data,
			structured_output_json, error_message, created_at, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = tx.ExecContext(ctx, query,
		t.ID, t.RunID, t.ParentTaskID, t.AgentID, t.Name, t.Description, string(t.Status),
		t.Priority, t.Timeout.Milliseconds(), t.RetryCount, t.MaxRetries, t.InputData, t.OutputData,
		string(structJSON), t.ErrorMessage, t.CreatedAt.UTC().Format(time.RFC3339), startedStr, completedStr,
	)
	if err != nil {
		return err
	}

	for _, dep := range t.Dependencies {
		_, err = tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO task_dependencies (task_id, depends_on_task_id) VALUES (?, ?)
		`, t.ID, dep)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateTask(ctx context.Context, t TaskRecord) error {
	structJSON, err := json.Marshal(t.StructuredOutput)
	if err != nil {
		structJSON = []byte("{}")
	}
	var startedStr, completedStr *string
	if t.StartedAt != nil {
		val := t.StartedAt.UTC().Format(time.RFC3339)
		startedStr = &val
	}
	if t.CompletedAt != nil {
		val := t.CompletedAt.UTC().Format(time.RFC3339)
		completedStr = &val
	}

	query := `
		UPDATE agent_tasks SET
			status = ?,
			retry_count = ?,
			output_data = ?,
			structured_output_json = ?,
			error_message = ?,
			started_at = ?,
			completed_at = ?
		WHERE id = ?
	`
	_, err = s.db.ExecContext(ctx, query,
		string(t.Status), t.RetryCount, t.OutputData, string(structJSON),
		t.ErrorMessage, startedStr, completedStr, t.ID,
	)
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*TaskRecord, error) {
	query := `
		SELECT id, run_id, parent_task_id, agent_id, name, description, status,
		       priority, timeout_ms, retry_count, max_retries, input_data, output_data,
		       structured_output_json, error_message, created_at, started_at, completed_at
		FROM agent_tasks WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var t TaskRecord
	var structRaw, createdStr string
	var timeoutMS int64
	var startedStr, completedStr sql.NullString

	err := row.Scan(
		&t.ID, &t.RunID, &t.ParentTaskID, &t.AgentID, &t.Name, &t.Description, &t.Status,
		&t.Priority, &timeoutMS, &t.RetryCount, &t.MaxRetries, &t.InputData, &t.OutputData,
		&structRaw, &t.ErrorMessage, &createdStr, &startedStr, &completedStr,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	t.Timeout = time.Duration(timeoutMS) * time.Millisecond
	_ = json.Unmarshal([]byte(structRaw), &t.StructuredOutput)
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	if startedStr.Valid {
		ti, _ := time.Parse(time.RFC3339, startedStr.String)
		t.StartedAt = &ti
	}
	if completedStr.Valid {
		ti, _ := time.Parse(time.RFC3339, completedStr.String)
		t.CompletedAt = &ti
	}

	// load deps
	depRows, err := s.db.QueryContext(ctx, "SELECT depends_on_task_id FROM task_dependencies WHERE task_id = ?", id)
	if err == nil {
		defer depRows.Close()
		for depRows.Next() {
			var d string
			if err := depRows.Scan(&d); err == nil {
				t.Dependencies = append(t.Dependencies, d)
			}
		}
	}

	return &t, nil
}

func (s *Store) ListTasksByRun(ctx context.Context, runID string) ([]TaskRecord, error) {
	query := `
		SELECT id, run_id, parent_task_id, agent_id, name, description, status,
		       priority, timeout_ms, retry_count, max_retries, input_data, output_data,
		       structured_output_json, error_message, created_at, started_at, completed_at
		FROM agent_tasks WHERE run_id = ? ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []TaskRecord
	for rows.Next() {
		var t TaskRecord
		var structRaw, createdStr string
		var timeoutMS int64
		var startedStr, completedStr sql.NullString

		if err := rows.Scan(
			&t.ID, &t.RunID, &t.ParentTaskID, &t.AgentID, &t.Name, &t.Description, &t.Status,
			&t.Priority, &timeoutMS, &t.RetryCount, &t.MaxRetries, &t.InputData, &t.OutputData,
			&structRaw, &t.ErrorMessage, &createdStr, &startedStr, &completedStr,
		); err != nil {
			return nil, err
		}
		t.Timeout = time.Duration(timeoutMS) * time.Millisecond
		_ = json.Unmarshal([]byte(structRaw), &t.StructuredOutput)
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		if startedStr.Valid {
			ti, _ := time.Parse(time.RFC3339, startedStr.String)
			t.StartedAt = &ti
		}
		if completedStr.Valid {
			ti, _ := time.Parse(time.RFC3339, completedStr.String)
			t.CompletedAt = &ti
		}
		list = append(list, t)
	}

	for i := range list {
		depRows, err := s.db.QueryContext(ctx, "SELECT depends_on_task_id FROM task_dependencies WHERE task_id = ?", list[i].ID)
		if err == nil {
			for depRows.Next() {
				var d string
				if err := depRows.Scan(&d); err == nil {
					list[i].Dependencies = append(list[i].Dependencies, d)
				}
			}
			depRows.Close()
		}
	}

	return list, rows.Err()
}

// Messages

func (s *Store) SaveMessage(ctx context.Context, m AgentMessage) error {
	metaJSON, err := json.Marshal(m.Metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}
	query := `
		INSERT INTO agent_messages (id, run_id, from_agent_id, to_agent_id, message_type, content, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.ExecContext(ctx, query,
		m.ID, m.RunID, m.FromAgentID, m.ToAgentID, string(m.Type), m.Content,
		string(metaJSON), m.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) ListMessagesByRun(ctx context.Context, runID string) ([]AgentMessage, error) {
	query := `
		SELECT id, run_id, from_agent_id, to_agent_id, message_type, content, metadata_json, created_at
		FROM agent_messages WHERE run_id = ? ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []AgentMessage
	for rows.Next() {
		var m AgentMessage
		var metaRaw, createdStr string
		if err := rows.Scan(
			&m.ID, &m.RunID, &m.FromAgentID, &m.ToAgentID, &m.Type, &m.Content, &metaRaw, &createdStr,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metaRaw), &m.Metadata)
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
		list = append(list, m)
	}
	return list, rows.Err()
}

// RecoverInterrupted marks any non-terminal runs and tasks as failed following a crash or restart.
func (s *Store) RecoverInterrupted(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = ?, error_message = 'interrupted by system restart', completed_at = ?
		WHERE status IN (?, ?, ?, ?)
	`, string(StatusFailed), now, string(StatusPending), string(StatusQueued), string(StatusRunning), string(StatusWaiting))
	if err != nil {
		return 0, err
	}
	runsAffected, _ := res.RowsAffected()

	_, err = s.db.ExecContext(ctx, `
		UPDATE agent_tasks
		SET status = ?, error_message = 'interrupted by system restart', completed_at = ?
		WHERE status IN (?, ?, ?, ?)
	`, string(StatusFailed), now, string(StatusPending), string(StatusQueued), string(StatusRunning), string(StatusWaiting))
	if err != nil {
		return runsAffected, err
	}

	return runsAffected, nil
}
