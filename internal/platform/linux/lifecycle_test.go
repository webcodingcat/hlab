//go:build linux

package linux_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/linux"
)

func TestASecondLockNamesTheRunningDataFolder(t *testing.T) {
	lifecycle := linux.NewLifecycle(filepath.Join(t.TempDir(), "hlab.lock"))
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

func TestWriteTokenLetsTheGroupRead(t *testing.T) {
	lifecycle := linux.NewLifecycle(filepath.Join(t.TempDir(), "hlab.lock"))
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("got mode %v, want 0640", info.Mode().Perm())
	}
}
