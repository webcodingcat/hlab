package hyperv

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Methods not built yet: Snapshot follows in a later phase.

var errNotImplemented = errors.New("not implemented")

func notImplemented(op string) error {
	return &platform.Error{Kind: platform.Failed, Op: op, Err: errNotImplemented}
}

// Snapshot is not implemented yet.
func (p *Platform) Snapshot(ctx context.Context, vm, diskPath, target string) error {
	return notImplemented("Snapshot")
}
