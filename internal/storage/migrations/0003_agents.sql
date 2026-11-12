-- Phase 4 schema: agent definitions, team configurations, execution runs and tasks.

CREATE TABLE agent_definitions (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    role              TEXT NOT NULL DEFAULT '',
    system_prompt     TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    routing_profile   TEXT NOT NULL DEFAULT '',
    params_json       TEXT NOT NULL DEFAULT '{}',
    max_tokens        INTEGER NOT NULL DEFAULT 0,
    timeout_ms        INTEGER NOT NULL DEFAULT 0,
    max_requests      INTEGER NOT NULL DEFAULT 1,
    autonomy_level    TEXT NOT NULL DEFAULT 'autonomous',
    context_policy    TEXT NOT NULL DEFAULT 'isolated',
    capabilities_json TEXT NOT NULL DEFAULT '[]',
    permissions_json  TEXT NOT NULL DEFAULT '[]',
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
CREATE INDEX idx_agents_enabled ON agent_definitions(enabled);

CREATE TABLE team_definitions (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    strategy             TEXT NOT NULL DEFAULT 'sequential', -- sequential, parallel, dependency
    members_json         TEXT NOT NULL DEFAULT '[]',
    execution_order_json TEXT NOT NULL DEFAULT '[]',
    dependencies_json    TEXT NOT NULL DEFAULT '{}',
    context_policy       TEXT NOT NULL DEFAULT 'isolated',
    aggregation_policy   TEXT NOT NULL DEFAULT 'collect_all', -- collect_all, ordered, primary, synthesizer, merge
    failure_policy       TEXT NOT NULL DEFAULT 'fail_fast',   -- fail_fast, continue_independent, best_effort
    synthesizer_agent_id TEXT NOT NULL DEFAULT '',
    max_parallel         INTEGER NOT NULL DEFAULT 2,
    timeout_ms           INTEGER NOT NULL DEFAULT 0,
    enabled              INTEGER NOT NULL DEFAULT 1,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);
CREATE INDEX idx_teams_enabled ON team_definitions(enabled);

CREATE TABLE agent_runs (
    id                     TEXT PRIMARY KEY,
    run_type               TEXT NOT NULL, -- 'agent' or 'team'
    agent_id               TEXT NOT NULL DEFAULT '',
    team_id                TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL, -- pending, queued, running, waiting, completed, failed, cancelled, timed_out
    input_context          TEXT NOT NULL DEFAULT '',
    output_result          TEXT NOT NULL DEFAULT '',
    structured_output_json TEXT NOT NULL DEFAULT '{}',
    error_message          TEXT NOT NULL DEFAULT '',
    prompt_tokens          INTEGER NOT NULL DEFAULT 0,
    completion_tokens      INTEGER NOT NULL DEFAULT 0,
    total_tokens           INTEGER NOT NULL DEFAULT 0,
    config_snapshot_json   TEXT NOT NULL DEFAULT '{}',
    created_at             TEXT NOT NULL,
    started_at             TEXT,
    completed_at           TEXT
);
CREATE INDEX idx_agent_runs_status ON agent_runs(status);
CREATE INDEX idx_agent_runs_created_at ON agent_runs(created_at);
CREATE INDEX idx_agent_runs_agent ON agent_runs(agent_id);
CREATE INDEX idx_agent_runs_team ON agent_runs(team_id);

CREATE TABLE agent_tasks (
    id                     TEXT PRIMARY KEY,
    run_id                 TEXT NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    parent_task_id         TEXT NOT NULL DEFAULT '',
    agent_id               TEXT NOT NULL,
    name                   TEXT NOT NULL DEFAULT '',
    description            TEXT NOT NULL DEFAULT '',
    status                 TEXT NOT NULL, -- pending, queued, running, waiting, completed, failed, cancelled, timed_out
    priority               INTEGER NOT NULL DEFAULT 0,
    timeout_ms             INTEGER NOT NULL DEFAULT 0,
    retry_count            INTEGER NOT NULL DEFAULT 0,
    max_retries            INTEGER NOT NULL DEFAULT 0,
    input_data             TEXT NOT NULL DEFAULT '',
    output_data            TEXT NOT NULL DEFAULT '',
    structured_output_json TEXT NOT NULL DEFAULT '{}',
    error_message          TEXT NOT NULL DEFAULT '',
    created_at             TEXT NOT NULL,
    started_at             TEXT,
    completed_at           TEXT
);
CREATE INDEX idx_agent_tasks_run ON agent_tasks(run_id);
CREATE INDEX idx_agent_tasks_parent ON agent_tasks(parent_task_id);
CREATE INDEX idx_agent_tasks_status ON agent_tasks(status);

CREATE TABLE task_dependencies (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id            TEXT NOT NULL REFERENCES agent_tasks(id) ON DELETE CASCADE,
    depends_on_task_id TEXT NOT NULL REFERENCES agent_tasks(id) ON DELETE CASCADE,
    UNIQUE(task_id, depends_on_task_id)
);
CREATE INDEX idx_task_deps_task ON task_dependencies(task_id);
CREATE INDEX idx_task_deps_dep ON task_dependencies(depends_on_task_id);

CREATE TABLE agent_messages (
    id            TEXT PRIMARY KEY,
    run_id        TEXT NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    from_agent_id TEXT NOT NULL,
    to_agent_id   TEXT NOT NULL DEFAULT '',
    message_type  TEXT NOT NULL DEFAULT 'text', -- text, handoff, summary, decision
    content       TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at    TEXT NOT NULL
);
CREATE INDEX idx_agent_messages_run ON agent_messages(run_id);
