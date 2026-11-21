package store_test

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/webcodingcat/hlab/internal/store"
)

// setVersion writes user_version v into the SQLite file at path, creating
// it if needed, without going through store.Open.
func setVersion(t *testing.T, path string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS other (a TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		t.Fatal(err)
	}
}

// putKV opens the store at path, sets key k and closes it.
func putKV(t *testing.T, path, k, v string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Tx(func(tx store.Tx) error { return store.SetKV(tx, k, v) }); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStore(t *testing.T) {
	t.Run("LIFE-08", func(t *testing.T) {
		cases := []struct {
			name    string
			prepare func(t *testing.T, path string)
			// want is nil when the file opens.
			want *store.IncompatibleError
		}{
			{name: "version 0, no file", prepare: func(*testing.T, string) {}},
			{name: "version 0, empty file", prepare: func(t *testing.T, path string) {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}},
			{name: "version 1", prepare: func(t *testing.T, path string) { putKV(t, path, "k", "v") }},
			{name: "higher version", prepare: func(t *testing.T, path string) {
				setVersion(t, path, store.SchemaVersion+1)
			}, want: &store.IncompatibleError{Found: store.SchemaVersion + 1, Want: store.SchemaVersion}},
			{name: "lower unknown version", prepare: func(t *testing.T, path string) {
				setVersion(t, path, -1)
			}, want: &store.IncompatibleError{Found: -1, Want: store.SchemaVersion}},
			{name: "not a database", prepare: func(t *testing.T, path string) {
				data := strings.Repeat("this is a text file, not an SQLite database\n", 100)
				if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}, want: &store.IncompatibleError{NotDatabase: true}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.db")
				tc.prepare(t, path)
				st, err := store.Open(path)
				if st != nil {
					t.Cleanup(func() { _ = st.Close() })
				}
				if tc.want == nil {
					if err != nil {
						t.Fatalf("Open: %v", err)
					}
					// The store is usable.
					if err := st.Tx(func(tx store.Tx) error { return store.SetKV(tx, "probe", "1") }); err != nil {
						t.Fatalf("SetKV after Open: %v", err)
					}
					return
				}
				var ie *store.IncompatibleError
				if !errors.As(err, &ie) {
					t.Fatalf("Open = %v, want *store.IncompatibleError", err)
				}
				if ie.NotDatabase != tc.want.NotDatabase {
					t.Errorf("NotDatabase = %v, want %v", ie.NotDatabase, tc.want.NotDatabase)
				}
				if !tc.want.NotDatabase && (ie.Found != tc.want.Found || ie.Want != tc.want.Want) {
					t.Errorf("Found, Want = %d, %d, want %d, %d", ie.Found, ie.Want, tc.want.Found, tc.want.Want)
				}
				newer := !tc.want.NotDatabase && tc.want.Found > tc.want.Want
				if ie.Newer() != newer {
					t.Errorf("Newer() = %v, want %v", ie.Newer(), newer)
				}
				if names := strings.Contains(ie.Error(), "newer HLab version"); names != newer {
					t.Errorf("Error() = %q; naming a newer HLab version: %v, want %v", ie.Error(), names, newer)
				}
			})
		}

		t.Run("discard", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.db")
			putKV(t, path, "k", "v")
			for _, side := range []string{"-wal", "-shm"} {
				if err := os.WriteFile(path+side, []byte(side), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			now := time.Date(2026, 11, 19, 13, 4, 5, 0, time.FixedZone("CET", 3600))
			const suffix = ".discarded-20261119T120405Z"
			renamed, err := store.Discard(path, now)
			if err != nil {
				t.Fatalf("Discard: %v", err)
			}
			if renamed != path+suffix {
				t.Errorf("Discard = %s, want %s", renamed, path+suffix)
			}
			for _, side := range []string{"", "-wal", "-shm"} {
				if _, err := os.Stat(path + side); !os.IsNotExist(err) {
					t.Errorf("%s still exists after Discard (%v)", path+side, err)
				}
				if _, err := os.Stat(path + side + suffix); err != nil {
					t.Errorf("discarded file: %v", err)
				}
			}

			st, err := store.Open(path)
			if err != nil {
				t.Fatalf("Open after Discard: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			err = st.Tx(func(tx store.Tx) error {
				_, err := store.GetKV(tx, "k")
				return err
			})
			if !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetKV(k) after Discard: %v, want store.ErrNotFound", err)
			}
		})
	})

	t.Run("PROJ-06_migration", func(t *testing.T) {
		// A state of schema version 1 opens as version 2.
		path := filepath.Join(t.TempDir(), "state.db")
		created := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
		sum := sha256.Sum256([]byte("token-value"))
		writeVersion1(t, path, created, sum[:])

		st, err := store.Open(path)
		if err != nil {
			t.Fatalf("Open of a version 1 state: %v", err)
		}
		err = st.Tx(func(tx store.Tx) error {
			p, err := store.ProjectByName(tx, "alpha")
			if err != nil {
				return fmt.Errorf("ProjectByName(alpha): %w", err)
			}
			want := store.Project{ID: 7, Name: "alpha", DisplayName: "alpha", Created: created}
			if p.ID != want.ID || p.Name != want.Name || p.DisplayName != want.DisplayName || !p.Created.Equal(want.Created) {
				t.Errorf("migrated project = %+v, want %+v", p, want)
			}
			tok, tp, err := store.TokenByHash(tx, sum[:])
			if err != nil {
				return fmt.Errorf("TokenByHash: %w", err)
			}
			if tok.Name != "t" || tok.ProjectID != 7 || tp.ID != 7 || tp.DisplayName != "alpha" {
				t.Errorf("token after migration = %+v of project %+v, want token t of project 7 with display name alpha", tok, tp)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("read migrated state: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if v := userVersion(t, path); v != 2 {
			t.Errorf("user_version after migration = %d, want 2", v)
		}

		// A new state is created as version 2 and keeps the display name.
		fresh := filepath.Join(t.TempDir(), "state.db")
		st, err = store.Open(fresh)
		if err != nil {
			t.Fatalf("Open of a new state: %v", err)
		}
		err = st.Tx(func(tx store.Tx) error {
			if _, err := store.CreateProject(tx, "web-coding-cat", "Web Coding Cat"); err != nil {
				return fmt.Errorf("CreateProject: %w", err)
			}
			p, err := store.ProjectByName(tx, "web-coding-cat")
			if err != nil {
				return fmt.Errorf("ProjectByName: %w", err)
			}
			if p.DisplayName != "Web Coding Cat" {
				t.Errorf("display name = %q, want %q", p.DisplayName, "Web Coding Cat")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("new state: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if v := userVersion(t, fresh); v != 2 {
			t.Errorf("user_version of a new state = %d, want 2", v)
		}
	})

	t.Run("UI-11_resources_by_type", func(t *testing.T) {
		st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })

		var p, q, empty store.Project
		err = st.Tx(func(tx store.Tx) error {
			var err error
			if p, err = store.CreateProject(tx, "p", "P"); err != nil {
				return err
			}
			if q, err = store.CreateProject(tx, "q", "Q"); err != nil {
				return err
			}
			if empty, err = store.CreateProject(tx, "e", "E"); err != nil {
				return err
			}
			var id int64
			put := func(project int64, kind string, doc map[string]any) error {
				id++
				return store.Put(tx, store.Meta{Kind: kind, ID: id, ProjectID: project, Name: fmt.Sprintf("r-%d", id)}, doc)
			}
			for _, r := range []struct {
				project int64
				kind    string
				doc     map[string]any
			}{
				{p.ID, "servers", map[string]any{}},
				{p.ID, "servers", map[string]any{}},
				{p.ID, "images", map[string]any{"type": "snapshot"}},
				{p.ID, "images", map[string]any{"type": "backup"}},
				{p.ID, "images", map[string]any{"type": "backup"}},
				{p.ID, "ssh_keys", map[string]any{}},
				{q.ID, "servers", map[string]any{}},
				{q.ID, "volumes", map[string]any{}},
				{q.ID, "images", map[string]any{"type": "snapshot"}},
			} {
				if err := put(r.project, r.kind, r.doc); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}

		for _, tc := range []struct {
			name    string
			project int64
			want    map[string]int
		}{
			{"p", p.ID, map[string]int{"servers": 2, "snapshots": 1, "backups": 2, "ssh_keys": 1}},
			{"empty", empty.ID, map[string]int{}},
		} {
			var got map[string]int
			err := st.Tx(func(tx store.Tx) error {
				var err error
				got, err = store.CountResourcesByType(tx, tc.project)
				return err
			})
			if err != nil {
				t.Fatalf("CountResourcesByType(%s): %v", tc.name, err)
			}
			if got == nil || !maps.Equal(got, tc.want) {
				t.Errorf("CountResourcesByType(%s) = %#v, want %#v", tc.name, got, tc.want)
			}
		}
	})
}

// writeVersion1 writes a state of schema version 1 to path: project 7
// "alpha" created at created with token "t" whose hash is sum.
func writeVersion1(t *testing.T, path string, created time.Time, sum []byte) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ts := created.UTC().Format(time.RFC3339Nano)
	for _, stmt := range []string{
		`CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT UNIQUE NOT NULL, created TEXT NOT NULL)`,
		`CREATE TABLE tokens (id INTEGER PRIMARY KEY, project_id INTEGER NOT NULL REFERENCES projects ON DELETE CASCADE,
			name TEXT NOT NULL, sha256 BLOB UNIQUE NOT NULL, read_only INTEGER NOT NULL, created TEXT NOT NULL,
			UNIQUE(project_id, name))`,
		`CREATE TABLE resources (kind TEXT NOT NULL, id INTEGER NOT NULL, project_id INTEGER REFERENCES projects ON DELETE CASCADE,
			name TEXT, labels TEXT NOT NULL DEFAULT '{}', created TEXT NOT NULL, doc TEXT NOT NULL,
			PRIMARY KEY(kind, id))`,
		`CREATE INDEX resources_project ON resources(project_id, kind)`,
		`CREATE TABLE actions (id INTEGER PRIMARY KEY, project_id INTEGER, command TEXT NOT NULL, status TEXT NOT NULL,
			progress INTEGER NOT NULL, started TEXT NOT NULL, finished TEXT, error TEXT, resources TEXT NOT NULL)`,
		`CREATE TABLE ids (kind TEXT PRIMARY KEY, next INTEGER NOT NULL)`,
		`CREATE TABLE kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`PRAGMA user_version = 1`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects (id, name, created) VALUES (7, 'alpha', ?)`, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tokens (project_id, name, sha256, read_only, created) VALUES (7, 't', ?, 0, ?)`, sum, ts); err != nil {
		t.Fatal(err)
	}
}

// userVersion reads PRAGMA user_version of the SQLite file at path.
func userVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
