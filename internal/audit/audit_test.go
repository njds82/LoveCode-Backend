package audit

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lovecode/lovecode/internal/storage"
)

func newLog(t *testing.T) (*Log, *storage.DB) {
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
	return New(db.DB), db
}

func TestChainVerifies(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	for i := 0; i < 5; i++ {
		if err := l.Append(ctx, Event{Actor: "a", Action: "x", Detail: map[string]any{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := l.Verify(ctx)
	if err != nil || !r.OK || r.Entries != 5 {
		t.Fatalf("verify: %+v %v", r, err)
	}
}

func TestSecretsRedactedInDetail(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	l.Append(ctx, Event{Actor: "a", Action: "x", Detail: map[string]any{"api_key": "super-secret-value", "note": "Bearer abcdefghijklmnop"}})
	es, _ := l.List(ctx, 10, "")
	d := string(es[0].Detail)
	if strings.Contains(d, "super-secret-value") || strings.Contains(d, "abcdefghijklmnop") {
		t.Fatalf("secret stored: %s", d)
	}
}

func TestTamperDetected(t *testing.T) {
	ctx := context.Background()
	l, db := newLog(t)
	for i := 0; i < 3; i++ {
		l.Append(ctx, Event{Actor: "a", Action: "x"})
	}
	// Simulate an attacker with raw file access: remove the trigger, edit a row.
	if _, err := db.ExecContext(ctx, `DROP TRIGGER audit_log_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE audit_log SET actor='mallory' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	r, err := l.Verify(ctx)
	if err != nil || r.OK || r.FirstBadID != 2 {
		t.Fatalf("tamper not detected: %+v %v", r, err)
	}
}

func TestConcurrentAppendsKeepChain(t *testing.T) {
	ctx := context.Background()
	l, _ := newLog(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Append(ctx, Event{Actor: "a", Action: "c"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	r, err := l.Verify(ctx)
	if err != nil || !r.OK || r.Entries != 20 {
		t.Fatalf("chain broken under concurrency: %+v %v", r, err)
	}
}
