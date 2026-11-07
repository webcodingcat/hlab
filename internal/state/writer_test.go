package state

import (
	"os"
	"path/filepath"
	"testing"
)

// readFile returns a file's content, or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWriterWritesTheNewestContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writer := NewWriter(path)
	for _, content := range []string{"first", "second", "third"} {
		writer.Put([]byte(content))
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "third" {
		t.Errorf("the file holds %q, want third", got)
	}
}

func TestFailedWriteLeavesTheOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writer := NewWriter(path)
	t.Cleanup(func() { _ = writer.Close() })
	writer.Put([]byte("old"))
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	// a folder where the temporary file goes makes the next write fail
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}

	writer.Put([]byte("new"))
	if err := writer.Flush(); err == nil {
		t.Fatal("the write did not fail")
	}
	if got := readFile(t, path); got != "old" {
		t.Errorf("the file holds %q, want old", got)
	}

	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	writer.Put([]byte("newer"))
	if err := writer.Flush(); err != nil {
		t.Fatalf("the next write failed too: %v", err)
	}
}

func TestWriteCutOffHalfwayLeavesTheOldState(t *testing.T) {
	base, id := withKey(t)
	path := save(t, base)
	// what a machine that stops halfway through a write leaves behind
	if err := os.WriteFile(path+".tmp", []byte(`{"version": 1, "kinds": {"proj`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Get(Begin(loaded), SSHKeys, id); err != nil {
		t.Errorf("the old state is lost: %v", err)
	}
	if err := writeFile(path, []byte("{}")); err != nil {
		t.Errorf("the next write fails over the remains: %v", err)
	}
}
