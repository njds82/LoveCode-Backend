package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	a, err := db.Migrate(ctx)
	if err != nil || len(a) == 0 {
		t.Fatalf("first migrate: %v %v", a, err)
	}
	b, err := db.Migrate(ctx)
	if err != nil || len(b) != 0 {
		t.Fatalf("second migrate should be a no-op: %v %v", b, err)
	}
}

func TestAuditLogAppendOnly(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO audit_log(ts,actor,action,prev_hash,hash) VALUES('t','a','x','p','h')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE audit_log SET actor='evil'`); err == nil {
		t.Fatal("UPDATE should be blocked")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM audit_log`); err == nil {
		t.Fatal("DELETE should be blocked")
	}
}

func TestBackupProducesOpenableCopy(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	db.Migrate(ctx)
	dest := filepath.Join(t.TempDir(), "b.db")
	if err := db.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	cp, err := Open(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	var n int
	if err := cp.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("backup missing data: n=%d err=%v", n, err)
	}
	if err := db.Backup(ctx, dest); err == nil {
		t.Fatal("backup must refuse to overwrite")
	}
}
