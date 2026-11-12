-- Phase 3 schema: persistent routing configuration and usage/rate tracking.

CREATE TABLE routing_profiles (
    id           TEXT PRIMARY KEY,
    policy       TEXT NOT NULL,          -- priority | weighted | ordered
    max_attempts INTEGER NOT NULL DEFAULT 3,
    timeout_ms   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE routing_candidates (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    profile_id     TEXT NOT NULL REFERENCES routing_profiles(id) ON DELETE CASCADE,
    provider_id    TEXT NOT NULL,
    model_id       TEXT NOT NULL,
    priority       INTEGER NOT NULL DEFAULT 0,
    weight         INTEGER NOT NULL DEFAULT 1,
    max_concurrent INTEGER NOT NULL DEFAULT 0,
    sort_order     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_routing_candidates_profile ON routing_candidates(profile_id);

CREATE TABLE model_groups (
    id         TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE model_group_members (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id   TEXT NOT NULL REFERENCES model_groups(id) ON DELETE CASCADE,
    model_id   TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE(group_id, model_id)
);
CREATE INDEX idx_model_group_members_group ON model_group_members(group_id);

CREATE TABLE breaker_configs (
    provider_id TEXT PRIMARY KEY,
    threshold   INTEGER NOT NULL DEFAULT 3,
    recovery_ms INTEGER NOT NULL DEFAULT 30000,
    max_probes  INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE usage_records (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id         TEXT NOT NULL,
    provider_id        TEXT NOT NULL,
    model_id           TEXT NOT NULL,
    prompt_tokens      INTEGER,
    completion_tokens  INTEGER,
    total_tokens       INTEGER,
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    success            INTEGER NOT NULL, -- 1 for success, 0 for failure
    error_kind         TEXT,
    stream             INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL
);
CREATE INDEX idx_usage_created_at ON usage_records(created_at);
CREATE INDEX idx_usage_provider ON usage_records(provider_id);
CREATE INDEX idx_usage_model ON usage_records(model_id);
CREATE INDEX idx_usage_success ON usage_records(success);
