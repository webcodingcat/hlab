// Package store holds the persistent state of hlabd in one SQLite file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// SchemaVersion is the schema version this binary reads and writes.
const SchemaVersion = 1

var (
	// ErrNotFound reports that a row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict reports a violated uniqueness constraint.
	ErrConflict = errors.New("store: conflict")
)

const schema = `
CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL, created TEXT NOT NULL);
CREATE TABLE tokens   (id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects ON DELETE CASCADE,
                       name TEXT NOT NULL, sha256 BLOB UNIQUE NOT NULL, read_only INTEGER NOT NULL, created TEXT NOT NULL,
                       UNIQUE(project_id, name));
CREATE TABLE resources (kind TEXT NOT NULL, id INTEGER NOT NULL, project_id INTEGER REFERENCES projects ON DELETE CASCADE,
                       name TEXT, labels TEXT NOT NULL DEFAULT '{}', created TEXT NOT NULL, doc TEXT NOT NULL,
                       PRIMARY KEY(kind, id));
CREATE INDEX resources_project ON resources(project_id, kind);
CREATE TABLE actions  (id INTEGER PRIMARY KEY, project_id INTEGER, command TEXT NOT NULL, status TEXT NOT NULL,
                       progress INTEGER NOT NULL, started TEXT NOT NULL, finished TEXT, error TEXT, resources TEXT NOT NULL);
CREATE TABLE ids      (kind TEXT PRIMARY KEY, next INTEGER NOT NULL);
CREATE TABLE kv       (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Store is an open state database. It uses a single connection, so there is
// exactly one writer and transactions run one after another.
type Store struct {
	db *sql.DB
}

// Tx is a running transaction, passed to the function given to Store.Tx.
type Tx struct {
	tx *sql.Tx
}

// IncompatibleError reports a state file this binary cannot use: another
// schema version, or no SQLite database at all.
type IncompatibleError struct {
	Found, Want int  // user_version of the file and of this binary
	NotDatabase bool // the file is no SQLite database (SQLITE_NOTADB/CORRUPT)
}

func (e *IncompatibleError) Error() string {
	switch {
	case e.NotDatabase:
		return "store: the state file is not a readable SQLite database"
	case e.Newer():
		return fmt.Sprintf("store: the state has schema version %d from a newer hlab version, this binary uses %d", e.Found, e.Want)
	default:
		return fmt.Sprintf("store: the state has unknown schema version %d, this binary uses %d", e.Found, e.Want)
	}
}

// Newer reports whether the file was written by a newer hlab version.
func (e *IncompatibleError) Newer() bool { return !e.NotDatabase && e.Found > e.Want }

// Open opens or creates the database at path and creates schema version 1
// in a new file. A file with another schema version or a file that is no
// SQLite database gives an *IncompatibleError.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_txlock", "immediate")
	dsn := "file:" + (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath() + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, notDatabase(err)
	}
	return s, nil
}

// notDatabase turns SQLITE_NOTADB and SQLITE_CORRUPT into an
// *IncompatibleError, judged by the result code only.
func notDatabase(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		if c := se.Code() & 0xff; c == sqlite3.SQLITE_NOTADB || c == sqlite3.SQLITE_CORRUPT {
			return &IncompatibleError{Want: SchemaVersion, NotDatabase: true}
		}
	}
	return err
}

func (s *Store) migrate() error {
	if err := s.db.Ping(); err != nil {
		return err
	}
	return s.Tx(func(tx Tx) error {
		var v int
		if err := tx.tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		switch v {
		case SchemaVersion:
			return nil
		case 0:
			if _, err := tx.tx.Exec(schema); err != nil {
				return fmt.Errorf("create schema: %w", err)
			}
			_, err := tx.tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion))
			return err
		default:
			return &IncompatibleError{Found: v, Want: SchemaVersion}
		}
	})
}

// Discard renames the state file at path and its -wal and -shm files to
// <name>.discarded-<UTC time> and returns the new name of path. Missing side
// files are skipped.
func Discard(path string, now time.Time) (string, error) {
	suffix := ".discarded-" + now.UTC().Format("20060102T150405Z")
	if err := os.Rename(path, path+suffix); err != nil {
		return "", err
	}
	for _, side := range []string{"-wal", "-shm"} {
		if err := os.Rename(path+side, path+side+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return path + suffix, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Tx runs fn in a transaction. It commits if fn returns nil and rolls back
// otherwise, returning fn's error.
func (s *Store) Tx(fn func(tx Tx) error) error {
	t, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(Tx{tx: t}); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

func (t Tx) exec(query string, args ...any) (sql.Result, error) {
	res, err := t.tx.Exec(query, args...)
	return res, mapErr(err)
}

// mapErr turns uniqueness violations into ErrConflict and missing rows into
// ErrNotFound.
func mapErr(err error) error {
	var se *sqlite.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &se) && (se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY):
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

// affected returns ErrNotFound if res changed no row.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func now() string { return formatTime(time.Now()) }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// NextID returns the next ID of kind, starting at 1. IDs are global per kind
// across projects and never reused.
func NextID(tx Tx, kind string) (int64, error) {
	var id int64
	err := tx.tx.QueryRow(`INSERT INTO ids(kind, next) VALUES(?, 2)
		ON CONFLICT(kind) DO UPDATE SET next = next + 1 RETURNING next - 1`, kind).Scan(&id)
	return id, mapErr(err)
}

// GetKV returns the value stored under key, or ErrNotFound.
func GetKV(tx Tx, key string) (string, error) {
	var v string
	err := tx.tx.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	return v, mapErr(err)
}

// SetKV stores value under key, replacing an existing value.
func SetKV(tx Tx, key, value string) error {
	_, err := tx.exec(`INSERT INTO kv(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// KVKeys returns the keys starting with prefix, sorted.
func KVKeys(tx Tx, prefix string) ([]string, error) {
	rows, err := tx.tx.Query(`SELECT key FROM kv WHERE substr(key, 1, length(?1)) = ?1 ORDER BY key`, prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// DeleteKV removes key; a missing key is no error.
func DeleteKV(tx Tx, key string) error {
	_, err := tx.exec(`DELETE FROM kv WHERE key = ?`, key)
	return err
}
