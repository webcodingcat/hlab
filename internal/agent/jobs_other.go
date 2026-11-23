//go:build !linux

package agent

import (
	"context"
	"errors"
)

// mkfs needs Linux tools.
func mkfs(context.Context, int, string) error {
	return errors.New("mkfs: only supported on Linux")
}

// rescuePrepare needs Linux tools.
func rescuePrepare(context.Context, int) error {
	return errors.New("rescue-prepare: only supported on Linux")
}

// FindDisk needs Linux.
func FindDisk(context.Context, int) (string, error) {
	return "", errors.New("finding a disk by LUN needs Linux")
}
