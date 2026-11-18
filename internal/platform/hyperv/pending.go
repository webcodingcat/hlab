package hyperv

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Methods not built yet: Snapshot and Metrics follow in later phases.

var errNotImplemented = errors.New("not implemented")

func notImplemented(op string) error {
	return &platform.Error{Kind: platform.Failed, Op: op, Err: errNotImplemented}
}

// Snapshot is not implemented yet.
func (p *Platform) Snapshot(ctx context.Context, vm, diskPath, target string) error {
	return notImplemented("Snapshot")
}

// Metrics is not implemented yet.
func (p *Platform) Metrics(ctx context.Context, vm string) (platform.Sample, error) {
	return platform.Sample{}, notImplemented("Metrics")
}
