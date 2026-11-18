// Package store holds the persistent state of hlabd in one SQLite file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
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

// Open opens or creates the database at path and creates schema version 1
// in a new file. A file with another schema version is an error.
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
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
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
			return fmt.Errorf("store: schema version %d, this binary uses %d", v, SchemaVersion)
		}
	})
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

// DeleteKV removes key; a missing key is no error.
func DeleteKV(tx Tx, key string) error {
	_, err := tx.exec(`DELETE FROM kv WHERE key = ?`, key)
	return err
}
