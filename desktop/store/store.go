// Package store keeps the desktop assistant's conversations, runs, tool calls,
// usage and provider settings in a local SQLite file. Provider secrets are
// never stored here; they live in the OS keychain.
//
// IDs are 128-bit random hex strings. Times are UTC unix milliseconds.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure Go driver, no CGO
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is safe for concurrent use by multiple goroutines.
type Store struct {
	db *sql.DB
}

// DefaultPath returns <user config dir>/Foxmayn Frappe Desktop/assistant.db.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config dir: %w", err)
	}
	return filepath.Join(dir, "Foxmayn Frappe Desktop", "assistant.db"), nil
}

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create store file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("create store file: %w", err)
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	// One connection serialises access: no SQLITE_BUSY between goroutines and
	// every connection-level pragma holds.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		site TEXT NOT NULL,
		mode TEXT NOT NULL,
		provider_id TEXT NOT NULL,
		model TEXT NOT NULL,
		created INTEGER NOT NULL,
		updated INTEGER NOT NULL
	);
	CREATE INDEX conversations_updated ON conversations(updated DESC);

	CREATE TABLE messages (
		n INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL UNIQUE,
		conv_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL,
		role TEXT NOT NULL,
		parts_json TEXT NOT NULL,
		created INTEGER NOT NULL,
		UNIQUE (conv_id, seq)
	);

	CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		conv_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		status TEXT NOT NULL,
		steps INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT '',
		started INTEGER NOT NULL,
		ended INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX runs_conv ON runs(conv_id);

	CREATE TABLE tool_calls (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		msg_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
		tool TEXT NOT NULL,
		site TEXT NOT NULL,
		args_json TEXT NOT NULL,
		result_text TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		approval TEXT NOT NULL DEFAULT '',
		started INTEGER NOT NULL,
		ended INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX tool_calls_run ON tool_calls(run_id);

	CREATE TABLE usage (
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		turn INTEGER NOT NULL,
		input INTEGER NOT NULL,
		output INTEGER NOT NULL,
		cached INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (run_id, turn)
	);

	CREATE TABLE providers (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		label TEXT NOT NULL,
		base_url TEXT NOT NULL DEFAULT '',
		default_model TEXT NOT NULL DEFAULT ''
	);

	CREATE VIRTUAL TABLE messages_fts USING fts5(text);

	CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, text) VALUES (new.n, COALESCE((
			SELECT group_concat(json_extract(value, '$.text'), ' ')
			FROM json_each(new.parts_json) WHERE json_valid(new.parts_json)
		), ''));
	END;
	CREATE TRIGGER messages_au AFTER UPDATE OF parts_json ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.n;
		INSERT INTO messages_fts(rowid, text) VALUES (new.n, COALESCE((
			SELECT group_concat(json_extract(value, '$.text'), ' ')
			FROM json_each(new.parts_json) WHERE json_valid(new.parts_json)
		), ''));
	END;
	CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.n;
	END;`,
}

func (s *Store) migrate() error {
	var cur int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&cur); err != nil {
		return fmt.Errorf("read store version: %w", err)
	}
	if cur > len(migrations) {
		return fmt.Errorf("store was written by a newer app version (schema %d, this app knows %d); update the app", cur, len(migrations))
	}
	for v := cur; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("migrate store: %w", err)
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate store to v%d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate store to v%d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate store to v%d: %w", v+1, err)
		}
	}
	return nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

func nowMS() int64 { return time.Now().UTC().UnixMilli() }

func ms(t int64) time.Time { return time.UnixMilli(t).UTC() }

func notFound(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
