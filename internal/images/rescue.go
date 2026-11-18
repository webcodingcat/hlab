package images

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// rescueFile is the prepared rescue base in Dir. Its name carries the
// version of the preparation: a changed preparation takes a new name, so
// existing installs prepare the rescue base again. v2 adds the SCSI order
// script to the initramfs.
const rescueFile = "rescue-v2.vhdx"

// EnsureRescue returns the path of the prepared rescue base
// (<Dir>\rescue-v2.vhdx), preparing it once from the base of RescueImage.
// Concurrent calls prepare once.
func (s *Store) EnsureRescue(ctx context.Context) (string, error) {
	dst := filepath.Join(s.opts.Dir, rescueFile)

	lock := s.lock("rescue/")
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("images: %w", err)
	}
	base, err := s.Ensure(ctx, s.opts.RescueImage)
	if err != nil {
		return "", err
	}
	if err := s.opts.PrepareRescue(ctx, base, dst); err != nil {
		return "", fmt.Errorf("images: rescue base: %w", err)
	}
	return dst, nil
}
