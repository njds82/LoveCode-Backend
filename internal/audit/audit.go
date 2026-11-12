// Package audit implements a hash-chained, append-only audit log stored in
// SQLite, separate from application logs.
//
// Guarantees: entries cannot be changed or removed through SQL (triggers), and
// any change made by other means (e.g. editing the file) breaks the hash chain,
// which Verify detects. It is tamper-EVIDENT, not tamper-proof: a party with
// write access to the database file can rewrite the whole chain. Mitigations
// (separate OS user, periodic external anchoring of the head hash) are Phase 8.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lovecode/lovecode/internal/security"
)

const genesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

type Event struct {
	Actor         string
	Action        string
	Target        string
	CorrelationID string
	Detail        map[string]any
}

type Entry struct {
	ID            int64           `json:"id"`
	Time          time.Time       `json:"ts"`
	Actor         string          `json:"actor"`
	Action        string          `json:"action"`
	Target        string          `json:"target"`
	CorrelationID string          `json:"correlation_id"`
	Detail        json.RawMessage `json:"detail"`
	PrevHash      string          `json:"prev_hash"`
	Hash          string          `json:"hash"`
}

type Log struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *Log { return &Log{db: db, now: time.Now} }

func compute(prev, ts, actor, action, target, corr, detail string) string {
	h := sha256.New()
	for _, f := range []string{prev, ts, actor, action, target, corr, detail} {
		h.Write([]byte(f))
		h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Append records an event. Detail is redacted before it is stored. The write
// transaction is IMMEDIATE (see storage.Open), so concurrent appends serialize
// and the chain stays linear.
func (l *Log) Append(ctx context.Context, e Event) error {
	detail := "{}"
	if len(e.Detail) > 0 {
		b, err := json.Marshal(security.RedactValue(e.Detail))
		if err != nil {
			return fmt.Errorf("marshal audit detail: %w", err)
		}
		detail = string(b)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	prev := genesisHash
	err = tx.QueryRowContext(ctx, `SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ts := l.now().UTC().Format(time.RFC3339Nano)
	hash := compute(prev, ts, e.Actor, e.Action, e.Target, e.CorrelationID, detail)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log(ts,actor,action,target,correlation_id,detail,prev_hash,hash) VALUES(?,?,?,?,?,?,?,?)`,
		ts, e.Actor, e.Action, e.Target, e.CorrelationID, detail, prev, hash); err != nil {
		return err
	}
	return tx.Commit()
}

// List returns the newest entries first. action filters when non-empty.
func (l *Log) List(ctx context.Context, limit int, action string) ([]Entry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT id,ts,actor,action,target,correlation_id,detail,prev_hash,hash FROM audit_log`
	var args []any
	if action != "" {
		q += ` WHERE action=?`
		args = append(args, action)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var ts, detail string
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.Target, &e.CorrelationID, &detail, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

// VerifyResult describes a chain verification.
type VerifyResult struct {
	OK         bool  `json:"ok"`
	Entries    int64 `json:"entries"`
	FirstBadID int64 `json:"first_bad_id,omitempty"`
}

// Verify recomputes the entire chain. Cost is O(n); intended for admin use.
func (l *Log) Verify(ctx context.Context) (VerifyResult, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT id,ts,actor,action,target,correlation_id,detail,prev_hash,hash FROM audit_log ORDER BY id ASC`)
	if err != nil {
		return VerifyResult{}, err
	}
	defer rows.Close()
	res := VerifyResult{OK: true}
	prev := genesisHash
	for rows.Next() {
		var id int64
		var ts, actor, action, target, corr, detail, ph, h string
		if err := rows.Scan(&id, &ts, &actor, &action, &target, &corr, &detail, &ph, &h); err != nil {
			return res, err
		}
		res.Entries++
		if ph != prev || compute(ph, ts, actor, action, target, corr, detail) != h {
			return VerifyResult{OK: false, Entries: res.Entries, FirstBadID: id}, rows.Err()
		}
		prev = h
	}
	return res, rows.Err()
}
