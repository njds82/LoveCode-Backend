package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lovecode/lovecode/internal/storage"
)

func newStore(t *testing.T) *TokenStore {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return NewTokenStore(db.DB)
}

func TestTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	ts := newStore(t)
	secret, p, err := ts.Create(ctx, "dev", []string{"read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ts.Verify(ctx, secret)
	if err != nil || got.TokenID != p.TokenID {
		t.Fatalf("verify: %v %+v", err, got)
	}
	if !got.Allows("read") || got.Allows("write") {
		t.Fatal("scope check wrong")
	}
	if _, err := ts.Verify(ctx, secret+"x"); err != ErrInvalidToken {
		t.Fatalf("wrong secret should fail, got %v", err)
	}
	if err := ts.Revoke(ctx, p.TokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Verify(ctx, secret); err != ErrInvalidToken {
		t.Fatal("revoked token must fail")
	}
	if err := ts.Revoke(ctx, p.TokenID); err != ErrNotFound {
		t.Fatalf("double revoke: %v", err)
	}
}

func TestTokenExpiry(t *testing.T) {
	ctx := context.Background()
	ts := newStore(t)
	secret, _, err := ts.Create(ctx, "tmp", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.Verify(ctx, secret); err != nil {
		t.Fatal(err)
	}
	ts.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := ts.Verify(ctx, secret); err != ErrInvalidToken {
		t.Fatalf("expired token must fail, got %v", err)
	}
}

func TestAdminImpliesAll(t *testing.T) {
	p := &Principal{Scopes: []string{ScopeAdmin}}
	if !p.Allows("anything") {
		t.Fatal("admin should allow all")
	}
	var nilP *Principal
	if nilP.Allows("read") {
		t.Fatal("nil principal must deny")
	}
}

func TestBootstrapOwnerOnce(t *testing.T) {
	ctx := context.Background()
	ts := newStore(t)
	dir := t.TempDir()
	path, created, err := BootstrapOwner(ctx, ts, dir)
	if err != nil || !created {
		t.Fatalf("bootstrap: %v %v", created, err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("token file perm %v", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if _, err := ts.Verify(ctx, strings.TrimSpace(string(raw))); err != nil {
		t.Fatalf("bootstrapped token invalid: %v", err)
	}
	if _, created, _ := BootstrapOwner(ctx, ts, dir); created {
		t.Fatal("must not create a second owner token")
	}
}

func TestRedaction(t *testing.T) {
	secret, _ := NewSecret()
	in := "Authorization: Bearer " + secret + " key=sk-abcdefghijklmnopqrstuv"
	out := RedactString(in)
	if strings.Contains(out, secret) || strings.Contains(out, "sk-abcdef") {
		t.Fatalf("leak: %s", out)
	}
	m := RedactValue(map[string]any{
		"api_key": "plain-value",
		"nested":  map[string]any{"Password": "hunter2", "ok": "fine"},
		"list":    []any{"hf_" + strings.Repeat("a", 30)},
	}).(map[string]any)
	if m["api_key"] != Redacted || m["nested"].(map[string]any)["Password"] != Redacted {
		t.Fatalf("sensitive keys not masked: %v", m)
	}
	if m["nested"].(map[string]any)["ok"] != "fine" {
		t.Fatal("non-sensitive value altered")
	}
	if m["list"].([]any)[0] != Redacted {
		t.Fatal("pattern in slice not masked")
	}
}
