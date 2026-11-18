package hyperv

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Methods not built yet: disks, adapters, DVD and boot order follow with
// the next plan, Snapshot and Metrics in later phases.

var errNotImplemented = errors.New("not implemented")

func notImplemented(op string) error {
	return &platform.Error{Kind: platform.Failed, Op: op, Err: errNotImplemented}
}

// AttachNIC is not implemented yet.
func (p *Platform) AttachNIC(ctx context.Context, vm string, nic platform.NICSpec) error {
	return notImplemented("AttachNIC")
}

// DetachNIC is not implemented yet.
func (p *Platform) DetachNIC(ctx context.Context, vm, mac string) error {
	return notImplemented("DetachNIC")
}

// CreateDisk is not implemented yet.
func (p *Platform) CreateDisk(ctx context.Context, path string, sizeBytes int64, from string, page83 [16]byte) error {
	return notImplemented("CreateDisk")
}

// ResizeDisk is not implemented yet.
func (p *Platform) ResizeDisk(ctx context.Context, path string, sizeBytes int64) error {
	return notImplemented("ResizeDisk")
}

// AttachDisk is not implemented yet.
func (p *Platform) AttachDisk(ctx context.Context, vm, path string, lun int) error {
	return notImplemented("AttachDisk")
}

// DetachDisk is not implemented yet.
func (p *Platform) DetachDisk(ctx context.Context, vm, path string) error {
	return notImplemented("DetachDisk")
}

// SetDVD is not implemented yet.
func (p *Platform) SetDVD(ctx context.Context, vm string, slot int, iso string) error {
	return notImplemented("SetDVD")
}

// SetBootOrder is not implemented yet.
func (p *Platform) SetBootOrder(ctx context.Context, vm string, order []platform.BootDevice) error {
	return notImplemented("SetBootOrder")
}

// Snapshot is not implemented yet.
func (p *Platform) Snapshot(ctx context.Context, vm, diskPath, target string) error {
	return notImplemented("Snapshot")
}

// Metrics is not implemented yet.
func (p *Platform) Metrics(ctx context.Context, vm string) (platform.Sample, error) {
	return platform.Sample{}, notImplemented("Metrics")
}
