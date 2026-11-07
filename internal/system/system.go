// Package system does the lab's work on the machine through the platform: VMs,
// disks, the host's network and DNS, the appliance. It never sees the state:
// it gets IDs, names, values and pictures, and returns plain values.
//
// For now it holds what the lab's life cycle needs; the later phases add the
// work of each resource.
package system

import (
	"context"
	"encoding/json"
	"errors"
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

// ReadSecrets reads a file that WriteSecrets wrote: secrets by ID. A missing
// file has none.
func (s *System) ReadSecrets(path string) (map[int64]string, error) {
	secrets := map[int64]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return secrets, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return secrets, nil
}

// WriteSecrets writes secrets by ID as JSON, so that local users may read
// them, as they may read the control token. Lifecycle.WriteSecret sets those
// rights (an ACL on Windows, 0640 and the group hlab on Linux); reading needs
// no platform.
func (s *System) WriteSecrets(path string, secrets map[int64]string) error {
	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return err
	}
	return s.platform.Lifecycle.WriteSecret(path, append(data, '\n'))
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
