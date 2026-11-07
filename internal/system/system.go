// Package system does the lab's work on the machine through the platform: VMs,
// disks, the host's network and DNS, the appliance. It never sees the state:
// it gets IDs, names, values and pictures, and returns plain values.
//
// For now it holds what the lab's life cycle needs; the later phases add the
// work of each resource.
package system

import (
	"context"
	"fmt"
	"os"

	"github.com/webcodingcat/hlab/internal/platform"
)

// System is the lab's hands on the machine.
type System struct {
	platform platform.Platform
}

// New returns a system that works through the given platform.
func New(parts platform.Platform) *System {
	return &System{platform: parts}
}

// ClaimDataDir creates the data folder if needed and makes this process the
// only HLab that uses it; release gives it up.
func (s *System) ClaimDataDir(dataDir string) (release func(), err error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	release, err = s.platform.Lifecycle.Lock(dataDir)
	if err != nil {
		return nil, fmt.Errorf("claiming the data folder %s: %w", dataDir, err)
	}
	return release, nil
}

// VMRunning tells whether a VM is on; a paused VM counts as on: the lab
// pauses VMs only to freeze them for a while.
func (s *System) VMRunning(ctx context.Context, vm string) (bool, error) {
	state, err := s.platform.Hypervisor.VMState(ctx, vm)
	if err != nil {
		return false, err
	}
	return state.Power != platform.PowerOff, nil
}
