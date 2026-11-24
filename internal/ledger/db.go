// Package ledger is Ferry's durable SQLite record: WAL mode, embedded
// forward-only migrations, concurrent readers and one serialized writer.
//
// Every write goes through WriteTx, which holds a process-wide write lock
// over a single-connection writer pool and opens BEGIN IMMEDIATE, so
// intermittent SQLITE_BUSY between Ferry's own goroutines cannot happen.
package ledger

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB is the ledger handle.
type DB struct {
	path   string
	writer *sql.DB
	reader *sql.DB
	mu     sync.Mutex
}

func dsn(path string, write bool) string {
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	if write {
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open opens (creating if needed) the ledger at path and applies migrations.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	db := &DB{path: path, writer: w, reader: r}
	if err := db.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Path is the database file path.
func (db *DB) Path() string { return db.path }

// Close closes both pools.
func (db *DB) Close() error {
	return errors.Join(db.reader.Close(), db.writer.Close())
}

// Read returns the concurrent reader pool.
func (db *DB) Read() *sql.DB { return db.reader }

// WriteTx runs fn in a serialized IMMEDIATE transaction. fn's error rolls
// back; a nil return commits.
func (db *DB) WriteTx(ctx context.Context, fn func(*sql.Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	// The write must land even if the caller's context was just canceled:
	// recording a cancellation is itself a write.
	tx, err := db.writer.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (db *DB) migrate(ctx context.Context) error {
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
			return err
		}
		entries, err := migrationFS.ReadDir("migrations")
		if err != nil {
			return err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			version := strings.TrimSuffix(name, ".sql")
			var exists int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
				return err
			}
			if exists > 0 {
				continue
			}
			body, err := migrationFS.ReadFile("migrations/" + name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", name, err)
			}
			if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, Now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// Now is the canonical timestamp format (RFC 3339, UTC, nanoseconds).
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// NullString maps "" to NULL.
func NullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
