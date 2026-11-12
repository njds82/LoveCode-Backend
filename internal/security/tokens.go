package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrNotFound     = errors.New("not found")
)

// ScopeAdmin implies every other scope.
const ScopeAdmin = "admin"

type Principal struct {
	TokenID string
	Name    string
	Scopes  []string
}

// Allows reports whether the principal holds scope (or admin).
func (p *Principal) Allows(scope string) bool {
	if p == nil {
		return false
	}
	for _, s := range p.Scopes {
		if s == ScopeAdmin || s == scope {
			return true
		}
	}
	return false
}

type TokenStore struct {
	db  *sql.DB
	now func() time.Time
}

func NewTokenStore(db *sql.DB) *TokenStore { return &TokenStore{db: db, now: time.Now} }

// NewSecret returns a 256-bit random token with a recognisable prefix.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lc_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashSecret is sha256 hex. A fast hash is appropriate because secrets are
// 256-bit random values, not human passwords.
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create issues a token. ttl <= 0 means no expiry. The plaintext secret is
// returned exactly once and is not recoverable afterwards.
func (t *TokenStore) Create(ctx context.Context, name string, scopes []string, ttl time.Duration) (string, Principal, error) {
	secret, err := NewSecret()
	if err != nil {
		return "", Principal{}, err
	}
	id, err := newID()
	if err != nil {
		return "", Principal{}, err
	}
	sc, _ := json.Marshal(scopes)
	now := t.now().UTC()
	var exp any
	if ttl > 0 {
		exp = now.Add(ttl).Format(time.RFC3339Nano)
	}
	_, err = t.db.ExecContext(ctx,
		`INSERT INTO api_tokens(id,name,token_hash,scopes,created_at,expires_at) VALUES(?,?,?,?,?,?)`,
		id, name, HashSecret(secret), string(sc), now.Format(time.RFC3339Nano), exp)
	if err != nil {
		return "", Principal{}, fmt.Errorf("insert token: %w", err)
	}
	return secret, Principal{TokenID: id, Name: name, Scopes: scopes}, nil
}

// Verify resolves a presented secret to a Principal.
func (t *TokenStore) Verify(ctx context.Context, secret string) (*Principal, error) {
	var id, name, scopesJSON string
	var exp, rev sql.NullString
	err := t.db.QueryRowContext(ctx,
		`SELECT id,name,scopes,expires_at,revoked_at FROM api_tokens WHERE token_hash=?`,
		HashSecret(secret)).Scan(&id, &name, &scopesJSON, &exp, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if rev.Valid {
		return nil, ErrInvalidToken
	}
	if exp.Valid {
		et, perr := time.Parse(time.RFC3339Nano, exp.String)
		if perr != nil || !t.now().Before(et) {
			return nil, ErrInvalidToken
		}
	}
	var scopes []string
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		return nil, fmt.Errorf("corrupt scopes for token %s: %w", id, err)
	}
	return &Principal{TokenID: id, Name: name, Scopes: scopes}, nil
}

func (t *TokenStore) Revoke(ctx context.Context, id string) error {
	res, err := t.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at=? WHERE id=? AND revoked_at IS NULL`,
		t.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (t *TokenStore) CountActive(ctx context.Context) (int, error) {
	var n int
	err := t.db.QueryRowContext(ctx, `SELECT count(*) FROM api_tokens WHERE revoked_at IS NULL`).Scan(&n)
	return n, err
}

// BootstrapOwner creates the first admin token if no active token exists and
// writes it to <dir>/owner.token with mode 0600. The secret is never logged.
func BootstrapOwner(ctx context.Context, ts *TokenStore, dir string) (path string, created bool, err error) {
	n, err := ts.CountActive(ctx)
	if err != nil {
		return "", false, err
	}
	if n > 0 {
		return "", false, nil
	}
	secret, _, err := ts.Create(ctx, "owner", []string{ScopeAdmin}, 0)
	if err != nil {
		return "", false, err
	}
	path = filepath.Join(dir, "owner.token")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write owner token: %w", err)
	}
	return path, true, nil
}
