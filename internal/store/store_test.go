package store_test

import (
	"database/sql"
	"errors"
	"fmt"
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
				if names := strings.Contains(ie.Error(), "newer hlab version"); names != newer {
					t.Errorf("Error() = %q; naming a newer hlab version: %v, want %v", ie.Error(), names, newer)
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
}
