// Package storage opens the SQLite database and applies embedded migrations.
//
// Driver: modernc.org/sqlite (pure Go, no CGO) so the binary builds with a plain
// Go toolchain on any distribution. Trade-off: slower than the CGO driver on
// write-heavy loads; revisit with benchmarks (docs/ARCHITECTURE.md, "Open decisions").
package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type DB struct {
	*sql.DB
	path string
}

// Open opens (creating if needed) the database at path with WAL, a busy
// timeout, foreign keys, and IMMEDIATE write transactions so concurrent writers
// queue instead of failing with SQLITE_BUSY upgrades.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()

	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Bounded pool: WAL allows concurrent readers; writers serialize in SQLite.
	sdb.SetMaxOpenConns(4)
	sdb.SetMaxIdleConns(2)
	sdb.SetConnMaxLifetime(30 * time.Minute)
	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return &DB{DB: sdb, path: path}, nil
}

func (d *DB) Path() string { return d.path }

// Migrate applies every unapplied migration in version order, each in its own
// transaction, and returns the names it applied.
func (d *DB) Migrate(ctx context.Context) ([]string, error) {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var applied []string
	for _, e := range entries {
		name := e.Name()
		verStr, _, ok := strings.Cut(name, "_")
		if !ok {
			return applied, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		ver, err := strconv.Atoi(verStr)
		if err != nil {
			return applied, fmt.Errorf("migration %q: bad version: %w", name, err)
		}
		var one int
		err = d.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version=?`, ver).Scan(&one)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return applied, err
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return applied, err
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return applied, fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`,
			ver, name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, err
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// Backup writes a consistent copy of the database to dest using VACUUM INTO.
// dest must not exist.
func (d *DB) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup destination %s already exists", dest)
	}
	quoted := strings.ReplaceAll(dest, "'", "''")
	if _, err := d.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return os.Chmod(dest, 0o600)
}

// Ping reports database reachability for health checks.
func (d *DB) Ping(ctx context.Context) error { return d.PingContext(ctx) }
