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

// newLifecycle returns a life cycle with a mutex and an instance file of the
// test's own, so that a running HLab does not get in the way, and the
// instance file's path.
func newLifecycle(t *testing.T) (*windows.Lifecycle, string) {
	t.Helper()
	instanceFile := filepath.Join(t.TempDir(), "instance")
	return windows.NewLifecycle(`Local\HLabTest-`+filepath.Base(t.TempDir()), instanceFile, nil), instanceFile
}

func TestASecondLockNamesTheRunningDataFolderUntilUnlock(t *testing.T) {
	lifecycle, instanceFile := newLifecycle(t)
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
	if _, err := os.Stat(instanceFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the instance file is still there after unlock: %v", err)
	}
	unlockAgain, err := lifecycle.Lock(t.TempDir())
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlockAgain()
}

func TestWriteSecretReplacesTheToken(t *testing.T) {
	lifecycle, _ := newLifecycle(t)
	path := filepath.Join(t.TempDir(), "control.token")
	for _, token := range []string{"first", "second"} {
		if err := lifecycle.WriteSecret(path, []byte(token)); err != nil {
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
