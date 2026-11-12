package routing

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store handles persistent storage for routing profiles, model groups, circuit breaker settings, and usage records.
type Store struct {
	db *sql.DB
}

// NewStore creates a Store backed by SQLite.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// SaveProfile persists or updates a Profile and its Candidates in a transaction.
func (s *Store) SaveProfile(ctx context.Context, p Profile) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	timeoutMs := p.Timeout.Milliseconds()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO routing_profiles (id, policy, max_attempts, timeout_ms, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			policy = excluded.policy,
			max_attempts = excluded.max_attempts,
			timeout_ms = excluded.timeout_ms,
			updated_at = excluded.updated_at
	`, p.ID, string(p.Policy), p.MaxAttempts, timeoutMs, now, now)
	if err != nil {
		return fmt.Errorf("upsert profile: %w", err)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM routing_candidates WHERE profile_id = ?`, p.ID)
	if err != nil {
		return fmt.Errorf("delete old candidates: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO routing_candidates (profile_id, provider_id, model_id, priority, weight, max_concurrent, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert candidate: %w", err)
	}
	defer stmt.Close()

	for i, c := range p.Candidates {
		_, err = stmt.ExecContext(ctx, p.ID, c.ProviderID, c.ModelID, c.Priority, c.Weight, c.MaxConcurrent, i)
		if err != nil {
			return fmt.Errorf("insert candidate %d: %w", i, err)
		}
	}

	return tx.Commit()
}

// LoadProfiles loads all saved Profiles and their candidates.
func (s *Store) LoadProfiles(ctx context.Context) ([]Profile, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, policy, max_attempts, timeout_ms FROM routing_profiles ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query profiles: %w", err)
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		var p Profile
		var pol string
		var timeoutMs int64
		if err := rows.Scan(&p.ID, &pol, &p.MaxAttempts, &timeoutMs); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		p.Policy = Policy(pol)
		p.Timeout = time.Duration(timeoutMs) * time.Millisecond
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range profiles {
		candRows, err := s.db.QueryContext(ctx, `
			SELECT provider_id, model_id, priority, weight, max_concurrent
			FROM routing_candidates
			WHERE profile_id = ?
			ORDER BY sort_order ASC
		`, profiles[i].ID)
		if err != nil {
			return nil, fmt.Errorf("query candidates for profile %s: %w", profiles[i].ID, err)
		}
		for candRows.Next() {
			var c Candidate
			if err := candRows.Scan(&c.ProviderID, &c.ModelID, &c.Priority, &c.Weight, &c.MaxConcurrent); err != nil {
				candRows.Close()
				return nil, fmt.Errorf("scan candidate: %w", err)
			}
			profiles[i].Candidates = append(profiles[i].Candidates, c)
		}
		candRows.Close()
		if err := candRows.Err(); err != nil {
			return nil, err
		}
	}

	return profiles, nil
}

// SaveGroup persists or updates a Group and its member model IDs in a transaction.
func (s *Store) SaveGroup(ctx context.Context, g Group) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO model_groups (id, created_at, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at
	`, g.ID, now, now)
	if err != nil {
		return fmt.Errorf("upsert group: %w", err)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM model_group_members WHERE group_id = ?`, g.ID)
	if err != nil {
		return fmt.Errorf("delete old group members: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO model_group_members (group_id, model_id, sort_order)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert group member: %w", err)
	}
	defer stmt.Close()

	for i, mID := range g.Models {
		_, err = stmt.ExecContext(ctx, g.ID, mID, i)
		if err != nil {
			return fmt.Errorf("insert group member %s: %w", mID, err)
		}
	}

	return tx.Commit()
}

// LoadGroups loads all saved Model Groups.
func (s *Store) LoadGroups(ctx context.Context) ([]Group, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM model_groups ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query groups: %w", err)
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range groups {
		mRows, err := s.db.QueryContext(ctx, `
			SELECT model_id FROM model_group_members WHERE group_id = ? ORDER BY sort_order ASC
		`, groups[i].ID)
		if err != nil {
			return nil, fmt.Errorf("query group members: %w", err)
		}
		for mRows.Next() {
			var mID string
			if err := mRows.Scan(&mID); err != nil {
				mRows.Close()
				return nil, fmt.Errorf("scan group member: %w", err)
			}
			groups[i].Models = append(groups[i].Models, mID)
		}
		mRows.Close()
		if err := mRows.Err(); err != nil {
			return nil, err
		}
	}

	return groups, nil
}

// BreakerConfig holds configurable circuit breaker thresholds per provider or default.
type BreakerConfig struct {
	ProviderID string        `json:"provider_id"`
	Threshold  int           `json:"threshold"`
	Recovery   time.Duration `json:"recovery"`
	MaxProbes  int           `json:"max_probes"`
}

// SaveBreakerConfig stores breaker configuration.
func (s *Store) SaveBreakerConfig(ctx context.Context, cfg BreakerConfig) error {
	if s == nil || s.db == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	recMs := cfg.Recovery.Milliseconds()
	if cfg.Threshold <= 0 {
		cfg.Threshold = 3
	}
	if recMs <= 0 {
		recMs = 30000
	}
	if cfg.MaxProbes <= 0 {
		cfg.MaxProbes = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO breaker_configs (provider_id, threshold, recovery_ms, max_probes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id) DO UPDATE SET
			threshold = excluded.threshold,
			recovery_ms = excluded.recovery_ms,
			max_probes = excluded.max_probes,
			updated_at = excluded.updated_at
	`, cfg.ProviderID, cfg.Threshold, recMs, cfg.MaxProbes, now, now)
	return err
}

// LoadBreakerConfigs loads all stored breaker configurations.
func (s *Store) LoadBreakerConfigs(ctx context.Context) (map[string]BreakerConfig, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT provider_id, threshold, recovery_ms, max_probes FROM breaker_configs`)
	if err != nil {
		return nil, fmt.Errorf("query breaker configs: %w", err)
	}
	defer rows.Close()

	out := make(map[string]BreakerConfig)
	for rows.Next() {
		var cfg BreakerConfig
		var recMs int64
		if err := rows.Scan(&cfg.ProviderID, &cfg.Threshold, &recMs, &cfg.MaxProbes); err != nil {
			return nil, fmt.Errorf("scan breaker config: %w", err)
		}
		cfg.Recovery = time.Duration(recMs) * time.Millisecond
		out[cfg.ProviderID] = cfg
	}
	return out, rows.Err()
}

// UsageRecord stores normalized generation usage.
type UsageRecord struct {
	ID               int64         `json:"id"`
	RequestID        string        `json:"request_id"`
	ProviderID       string        `json:"provider_id"`
	ModelID          string        `json:"model_id"`
	PromptTokens     *int          `json:"prompt_tokens,omitempty"`
	CompletionTokens *int          `json:"completion_tokens,omitempty"`
	TotalTokens      *int          `json:"total_tokens,omitempty"`
	Duration         time.Duration `json:"duration"`
	Success          bool          `json:"success"`
	ErrorKind        string        `json:"error_kind,omitempty"`
	Stream           bool          `json:"stream"`
	CreatedAt        time.Time     `json:"created_at"`
}

// RecordUsage inserts a usage record into SQLite.
func (s *Store) RecordUsage(ctx context.Context, u UsageRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	ts := u.CreatedAt.Format(time.RFC3339)
	successInt := 0
	if u.Success {
		successInt = 1
	}
	streamInt := 0
	if u.Stream {
		streamInt = 1
	}
	durMs := u.Duration.Milliseconds()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_records (
			request_id, provider_id, model_id, prompt_tokens, completion_tokens,
			total_tokens, duration_ms, success, error_kind, stream, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, u.RequestID, u.ProviderID, u.ModelID, u.PromptTokens, u.CompletionTokens,
		u.TotalTokens, durMs, successInt, u.ErrorKind, streamInt, ts)
	return err
}

// UsageFilter defines criteria for querying usage statistics.
type UsageFilter struct {
	ProviderID string
	ModelID    string
	Success    *bool
	Since      time.Time
	Until      time.Time
	Limit      int
	Offset     int
}

// UsageSummary holds aggregated usage figures.
type UsageSummary struct {
	TotalRequests     int64 `json:"total_requests"`
	Successful        int64 `json:"successful"`
	Failed            int64 `json:"failed"`
	PromptTokens      int64 `json:"prompt_tokens"`
	CompletionTokens  int64 `json:"completion_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	UnknownUsageCount int64 `json:"unknown_usage_count"`
}

// QueryUsage returns usage records matching the filter.
func (s *Store) QueryUsage(ctx context.Context, f UsageFilter) ([]UsageRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	query := `SELECT id, request_id, provider_id, model_id, prompt_tokens, completion_tokens, total_tokens, duration_ms, success, error_kind, stream, created_at FROM usage_records WHERE 1=1`
	var args []any

	if f.ProviderID != "" {
		query += ` AND provider_id = ?`
		args = append(args, f.ProviderID)
	}
	if f.ModelID != "" {
		query += ` AND model_id = ?`
		args = append(args, f.ModelID)
	}
	if f.Success != nil {
		sInt := 0
		if *f.Success {
			sInt = 1
		}
		query += ` AND success = ?`
		args = append(args, sInt)
	}
	if !f.Since.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, f.Since.Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		query += ` AND created_at <= ?`
		args = append(args, f.Until.Format(time.RFC3339))
	}
	query += ` ORDER BY id DESC`
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query += ` LIMIT ?`
	args = append(args, limit)
	if f.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, f.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage: %w", err)
	}
	defer rows.Close()

	var records []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var successInt, streamInt int
		var durMs int64
		var ts string
		var pt, ct, tt sql.NullInt64
		var errKind sql.NullString
		if err := rows.Scan(&r.ID, &r.RequestID, &r.ProviderID, &r.ModelID, &pt, &ct, &tt, &durMs, &successInt, &errKind, &streamInt, &ts); err != nil {
			return nil, fmt.Errorf("scan usage: %w", err)
		}
		if pt.Valid {
			val := int(pt.Int64)
			r.PromptTokens = &val
		}
		if ct.Valid {
			val := int(ct.Int64)
			r.CompletionTokens = &val
		}
		if tt.Valid {
			val := int(tt.Int64)
			r.TotalTokens = &val
		}
		if errKind.Valid {
			r.ErrorKind = errKind.String
		}
		r.Success = successInt == 1
		r.Stream = streamInt == 1
		r.Duration = time.Duration(durMs) * time.Millisecond
		r.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		records = append(records, r)
	}
	return records, rows.Err()
}

// SummaryUsage aggregates metrics across records matching the filter.
func (s *Store) SummaryUsage(ctx context.Context, f UsageFilter) (UsageSummary, error) {
	var sum UsageSummary
	if s == nil || s.db == nil {
		return sum, nil
	}
	query := `
		SELECT
			COUNT(*) as total_reqs,
			COALESCE(SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(COALESCE(prompt_tokens, 0)), 0),
			COALESCE(SUM(COALESCE(completion_tokens, 0)), 0),
			COALESCE(SUM(COALESCE(total_tokens, 0)), 0),
			COALESCE(SUM(CASE WHEN total_tokens IS NULL THEN 1 ELSE 0 END), 0)
		FROM usage_records
		WHERE 1=1
	`
	var args []any
	if f.ProviderID != "" {
		query += ` AND provider_id = ?`
		args = append(args, f.ProviderID)
	}
	if f.ModelID != "" {
		query += ` AND model_id = ?`
		args = append(args, f.ModelID)
	}
	if f.Success != nil {
		sInt := 0
		if *f.Success {
			sInt = 1
		}
		query += ` AND success = ?`
		args = append(args, sInt)
	}
	if !f.Since.IsZero() {
		query += ` AND created_at >= ?`
		args = append(args, f.Since.Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		query += ` AND created_at <= ?`
		args = append(args, f.Until.Format(time.RFC3339))
	}

	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&sum.TotalRequests,
		&sum.Successful,
		&sum.Failed,
		&sum.PromptTokens,
		&sum.CompletionTokens,
		&sum.TotalTokens,
		&sum.UnknownUsageCount,
	)
	if err != nil {
		return sum, fmt.Errorf("summary usage: %w", err)
	}
	return sum, nil
}
