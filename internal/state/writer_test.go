package state

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// quiet is a logger for writers whose failures the test causes on purpose.
var quiet = slog.New(slog.DiscardHandler)

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
	writer := NewWriter(path, quiet)
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

// A request that outlives the service's stop timeout may still change the
// lab after it closed; that must not panic on the closed wake-up channel.
func TestPutAfterCloseIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writer := NewWriter(path, quiet)
	writer.Put([]byte("before"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	writer.Put([]byte("after"))

	if got := readFile(t, path); got != "before" {
		t.Errorf("the file holds %q, want before", got)
	}
}

// A Put that comes while the writer wakes up leaves a wake-up behind for
// content that is on disk already. Writing it again would replace the file
// under a reader, which fails on Windows ("access denied").
func TestAWakeUpWithoutNewContentWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writer := NewWriter(path, quiet)
	writer.Put([]byte("first"))
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writer.wake <- struct{}{} // the wake-up a Put left behind
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the writer wrote the same content again: %v", err)
	}
}

func TestFailedWriteLeavesTheOldFileAndIsTriedAgain(t *testing.T) {
	retryFirst, retryLongest = time.Millisecond, time.Millisecond
	t.Cleanup(func() { retryFirst, retryLongest = time.Second, 30*time.Second })
	path := filepath.Join(t.TempDir(), "state.json")
	writer := NewWriter(path, quiet)
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

	// no new content: the writer tries again on its own
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for writer.Flush() != nil {
		if time.Now().After(deadline) {
			t.Fatal("the failed write was not tried again")
		}
		time.Sleep(time.Millisecond)
	}
	if got := readFile(t, path); got != "new" {
		t.Errorf("the file holds %q, want new", got)
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
