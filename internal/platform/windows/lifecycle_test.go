//go:build windows

package windows_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// newLifecycle returns a life cycle with a mutex of the test's own, so that
// a running HLab does not get in the way.
func newLifecycle(t *testing.T) *windows.Lifecycle {
	t.Helper()
	return windows.NewLifecycle(`Local\HLabTest-`+filepath.Base(t.TempDir()), filepath.Join(t.TempDir(), "instance"))
}

func TestASecondLockNamesTheRunningDataFolder(t *testing.T) {
	lifecycle := newLifecycle(t)
	first := t.TempDir()
	unlock, err := lifecycle.Lock(first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lifecycle.Lock(t.TempDir())
	if !errors.Is(err, platform.ErrLocked) || !strings.Contains(err.Error(), first) {
		t.Errorf("got %v, want platform.ErrLocked naming %s", err, first)
	}
	unlock()
	unlockAgain, err := lifecycle.Lock(t.TempDir())
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlockAgain()
}

func TestWriteTokenReplacesTheToken(t *testing.T) {
	lifecycle := newLifecycle(t)
	path := filepath.Join(t.TempDir(), "control.token")
	for _, token := range []string{"first", "second"} {
		if err := lifecycle.WriteToken(path, []byte(token)); err != nil {
			t.Fatal(err)
		}
		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(written) != token {
			t.Errorf("got %q, want %q", written, token)
		}
	}
}
