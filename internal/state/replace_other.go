//go:build !windows

package state

import (
	"os"
	"path/filepath"
)

// replaceFile moves from over to, and returns once the move is on disk: the
// rename is in the folder, so the folder is synced too.
func replaceFile(from, to string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(to))
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}
