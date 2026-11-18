package images

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// rescueFile is the prepared rescue base in Dir.
const rescueFile = "rescue.vhdx"

// EnsureRescue returns the path of the prepared rescue base
// (<Dir>\rescue.vhdx), preparing it once from the base of RescueImage.
// Concurrent calls prepare once.
func (s *Store) EnsureRescue(ctx context.Context) (string, error) {
	if s.opts.PrepareRescue == nil || s.opts.RescueImage == "" {
		return "", errors.New("images: no rescue image configured")
	}
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
