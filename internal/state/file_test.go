package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// save writes s to a state file in a new folder and returns its path.
func save(t *testing.T, s *State) string {
	t.Helper()
	data, err := Encode(s, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := writeFile(path, data); err != nil {
		t.Fatal(err)
	}
	return path
}

// useVersion makes the code write version and know migrations, for one test.
func useVersion(t *testing.T, version int, known map[int]Migration) {
	oldVersion, oldMigrations := currentVersion, migrations
	currentVersion, migrations = version, known
	t.Cleanup(func() { currentVersion, migrations = oldVersion, oldMigrations })
}

func TestSavedStateLoadsAgain(t *testing.T) {
	base, id := withKey(t)

	loaded, err := Load(save(t, base))
	if err != nil {
		t.Fatal(err)
	}
	tx := Begin(loaded)
	key, err := Get(tx, SSHKeys, id)
	if err != nil {
		t.Fatal(err)
	}
	if key.Name != "admin" || key.Labels["team"] != "a" {
		t.Errorf("the key loaded as %+v", key)
	}
	if next := NewID(tx, SSHKeys); next != id+1 {
		t.Errorf("the next ID is %d, want %d", next, id+1)
	}
}

func TestStateFileListsRecordsSortedByID(t *testing.T) {
	tx := Begin(New())
	for _, id := range []int64{1000002, 1000001} {
		Put(tx, Projects, &Project{ID: id})
	}
	data, err := Encode(tx.Result(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var out struct {
		Kinds map[string][]struct{ ID int64 } `json:"kinds"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if projects := out.Kinds["projects"]; len(projects) != 2 || projects[0].ID != 1000001 {
		t.Errorf("the projects are listed as %v", projects)
	}
	if _, listed := out.Kinds["actions"]; !listed {
		t.Error("an empty kind is left out of the file")
	}
}

func TestMissingStateFileIsAnEmptyState(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if projects := All(Begin(s), Projects); len(projects) != 0 {
		t.Errorf("an empty state has projects: %v", projects)
	}
}

func TestNewerStateFileIsRefused(t *testing.T) {
	path := save(t, New())
	useVersion(t, 0, map[int]Migration{})

	if _, err := Load(path); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("got %v, want ErrNewerVersion", err)
	}
}

func TestUnknownKindIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := `{"version": 1, "kinds": {"servers": []}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), `unknown kind "servers"`) {
		t.Fatalf("got %v, want an error about the unknown kind", err)
	}
}

// testMigration is a migration from version 1 to 2 for the test below: version
// 2 lets every project reach the Internet.
func testMigration(raw map[string]any) error {
	kinds := raw["kinds"].(map[string]any)
	for _, project := range kinds["projects"].([]any) {
		project.(map[string]any)["internet"] = true
	}
	return nil
}

func TestVersion1LoadsAfterAMigrationToVersion2(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "state-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	useVersion(t, 2, map[int]Migration{1: testMigration})

	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tx := Begin(s)
	project, err := Get(tx, Projects, 1000001)
	if err != nil {
		t.Fatal(err)
	}
	if !project.Internet || project.Name != "default" {
		t.Errorf("the project loaded as %+v", project)
	}
	if key, err := Get(tx, SSHKeys, 1000003); err != nil || key.Fingerprint == "" {
		t.Errorf("the SSH key loaded as %+v, %v", key, err)
	}
	if next := NewID(tx, Actions); next != 1000005 {
		t.Errorf("the next action ID is %d, want 1000005", next)
	}
	kept, err := os.ReadFile(path + ".v1")
	if err != nil || string(kept) != string(original) {
		t.Errorf("the old file was not kept as state.json.v1 (%v)", err)
	}
	data, err := Encode(s, time.Now())
	if err != nil || !strings.Contains(string(data), `"version": 2`) {
		t.Errorf("the migrated state saves as %.40q…, %v; want version 2", data, err)
	}
}

func TestMissingMigrationIsAnError(t *testing.T) {
	path := save(t, New())
	useVersion(t, 2, map[int]Migration{})

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "no migration from version 1") {
		t.Fatalf("got %v, want an error about the missing migration", err)
	}
}
