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
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// dsn builds a file: URI so paths with spaces, '#', '?' or '%' (and Windows
// drive paths) reach SQLite intact. _txlock=immediate makes every transaction
// BEGIN IMMEDIATE, so concurrent writers (also from another process) queue on
// the busy timeout instead of failing on a lock upgrade.
func dsn(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/dir -> /C:/dir, giving file:///C:/dir
	}
	q := url.Values{}
	q.Add("_txlock", "immediate")
	for _, pr := range []string{"busy_timeout(5000)", "foreign_keys(1)", "journal_mode(WAL)", "synchronous(NORMAL)"} {
		q.Add("_pragma", pr)
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: q.Encode()}
	return u.String(), nil
}

// Open opens (creating if needed) the database at path and migrates it.
// An existing directory or file with a wider mode is tightened to 0700/0600.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create store dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure store dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create store file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("create store file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure store file: %w", err)
	}
	uri, err := dsn(path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	var s *Store
	// Switching a fresh file to WAL needs a lock the busy timeout does not
	// always wait for, so a concurrent first open can see SQLITE_BUSY: retry.
	for attempt := 0; ; attempt++ {
		db, err := sql.Open("sqlite", uri)
		if err != nil {
			return nil, fmt.Errorf("open store: %w", err)
		}
		// One connection serialises access within the process and keeps every
		// connection-level pragma in force.
		db.SetMaxOpenConns(1)
		s = &Store{db: db}
		err = s.migrate()
		if err == nil {
			break
		}
		_ = db.Close()
		if attempt >= 40 || !strings.Contains(err.Error(), "SQLITE_BUSY") {
			return nil, err
		}
		time.Sleep(25 * time.Millisecond)
	}
	db := s.db
	// The WAL and shared-memory files are created lazily; keep them private.
	for _, sfx := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+sfx, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = db.Close()
			return nil, fmt.Errorf("secure store file: %w", err)
		}
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
			FROM json_each(CASE
				WHEN NOT json_valid(new.parts_json) THEN '[]'
				WHEN json_type(new.parts_json) <> 'array' THEN '[]'
				ELSE new.parts_json END)
			WHERE type = 'object'
		), ''));
	END;
	CREATE TRIGGER messages_au AFTER UPDATE OF parts_json ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.n;
		INSERT INTO messages_fts(rowid, text) VALUES (new.n, COALESCE((
			SELECT group_concat(json_extract(value, '$.text'), ' ')
			FROM json_each(CASE
				WHEN NOT json_valid(new.parts_json) THEN '[]'
				WHEN json_type(new.parts_json) <> 'array' THEN '[]'
				ELSE new.parts_json END)
			WHERE type = 'object'
		), ''));
	END;
	CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
		DELETE FROM messages_fts WHERE rowid = old.n;
	END;`,

	// v2 holds every schema change of 0.3.0, so later work never races on
	// user_version. profile_id names a preset (a code constant) or a row of
	// profiles, so it has no foreign key. A NULL cost_usd is "cost unknown".
	// usage is rebuilt (SQLite cannot change a primary key) so a run can
	// hold a turn row and other kinds of rows (a title) with the same turn.
	`CREATE TABLE profiles (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		preset TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL,
		toolsets_json TEXT NOT NULL DEFAULT '[]',
		policy_json TEXT NOT NULL DEFAULT '{}',
		deny_tools_json TEXT NOT NULL DEFAULT '[]',
		call_method INTEGER NOT NULL DEFAULT 0,
		step_limit INTEGER NOT NULL DEFAULT 25,
		provider_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		instructions TEXT NOT NULL DEFAULT '',
		keep_history INTEGER NOT NULL DEFAULT 1,
		created INTEGER NOT NULL,
		updated INTEGER NOT NULL
	);

	ALTER TABLE conversations ADD COLUMN profile_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE conversations ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE conversations ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE conversations ADD COLUMN ephemeral INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE conversations ADD COLUMN site_url TEXT NOT NULL DEFAULT '';
	ALTER TABLE conversations ADD COLUMN site_context TEXT NOT NULL DEFAULT '';
	ALTER TABLE conversations ADD COLUMN site_context_key TEXT NOT NULL DEFAULT '';
	ALTER TABLE conversations ADD COLUMN title_source TEXT NOT NULL DEFAULT '';
	CREATE INDEX conversations_site_updated ON conversations(site, updated DESC);

	CREATE TABLE site_settings (
		site TEXT PRIMARY KEY,
		url TEXT NOT NULL DEFAULT '',
		instructions TEXT NOT NULL DEFAULT '',
		local_only INTEGER NOT NULL DEFAULT 0,
		updated INTEGER NOT NULL
	);

	CREATE TABLE usage_v2 (
		run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		turn INTEGER NOT NULL,
		kind TEXT NOT NULL DEFAULT 'turn',
		input INTEGER NOT NULL,
		output INTEGER NOT NULL,
		cached INTEGER NOT NULL DEFAULT 0,
		cache_write INTEGER NOT NULL DEFAULT 0,
		cost_usd REAL,
		cost_source TEXT NOT NULL DEFAULT '',
		price_date TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (run_id, turn, kind)
	);
	INSERT INTO usage_v2(run_id,turn,input,output,cached) SELECT run_id,turn,input,output,cached FROM usage;
	DROP TABLE usage;
	ALTER TABLE usage_v2 RENAME TO usage;

	CREATE TABLE attachments (
		id TEXT PRIMARY KEY,
		conv_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		msg_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
		name TEXT NOT NULL,
		mime TEXT NOT NULL,
		size INTEGER NOT NULL,
		sha256 TEXT NOT NULL,
		text TEXT NOT NULL DEFAULT '',
		data BLOB,
		created INTEGER NOT NULL
	);
	CREATE INDEX attachments_conv ON attachments(conv_id);

	CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`,
}

func (s *Store) migrate() error {
	// BEGIN IMMEDIATE (see dsn) plus a re-read of user_version inside the
	// transaction: two processes opening a fresh file migrate it once.
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("migrate store: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var cur int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&cur); err != nil {
		return fmt.Errorf("read store version: %w", err)
	}
	if cur > len(migrations) {
		return fmt.Errorf("store was written by a newer app version (schema %d, this app knows %d); update the app", cur, len(migrations))
	}
	for v := cur; v < len(migrations); v++ {
		if _, err := tx.Exec(migrations[v]); err != nil {
			return fmt.Errorf("migrate store to v%d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			return fmt.Errorf("migrate store to v%d: %w", v+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate store: %w", err)
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

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// execOne runs an UPDATE or DELETE that must hit a row: no row is ErrNotFound,
// and every error carries op.
func execOne(op string, db execer, query string, args ...any) error {
	res, err := db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return nil
}

// fkNotFound maps a foreign key failure (the parent row does not exist) to
// ErrNotFound and wraps any other error with op.
func fkNotFound(op string, err error) error {
	if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// rowsErr wraps an iteration error with op.
func rowsErr(rows *sql.Rows, op string) error {
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
