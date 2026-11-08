package system

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// HostResources are the host's logical processors and memory, which bound
// what the lab's VMs may take.
type HostResources struct {
	Threads  int
	MemoryMB int
}

// HostResources asks the hypervisor what the host has.
func (s *System) HostResources(ctx context.Context) (HostResources, error) {
	resources, err := s.platform.Hypervisor.HostResources(ctx)
	if err != nil {
		return HostResources{}, fmt.Errorf("reading the host's resources: %w", err)
	}
	return HostResources{Threads: resources.Threads, MemoryMB: resources.MemoryMB}, nil
}

// ServerSpec is a server's VM: its size, its disk on a cached image, its
// seed and its public NIC.
type ServerSpec struct {
	VM        string
	CPUs      int
	MemoryMB  int
	Disk      string // the server's differencing disk
	DiskGB    int
	BaseImage string // the cached image the disk differs from
	Seed      string // the seed ISO
	SeedFiles map[string][]byte
	MAC       string // of the public NIC
}

// CreateServer makes a server's VM, off: its disk on the base image, its
// seed in the DVD drive and its public NIC on the public switch, isolated
// in the private VLAN. A disk that is there already is kept as it is, and
// keptDisk says so: the server lost its VM but not its disk (an uninstall
// that kept the data). The seed is always made anew. If a step fails, what
// was made goes again; a kept disk stays.
func (s *System) CreateServer(ctx context.Context, spec ServerSpec) (keptDisk bool, err error) {
	hypervisor := s.platform.Hypervisor
	err = hypervisor.CreateDisk(ctx, spec.Disk, spec.DiskGB, spec.BaseImage)
	keptDisk = errors.Is(err, platform.ErrExists)
	if err != nil && !keptDisk {
		return false, fmt.Errorf("creating the disk of %s: %w", spec.VM, err)
	}
	made := []string{spec.Seed}
	if !keptDisk {
		made = append(made, spec.Disk)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.DeleteServer(context.WithoutCancel(ctx), spec.VM, made...))
		}
	}()
	// a seed of an earlier VM, or what a create cut short left
	if err := hypervisor.DeleteDisk(ctx, spec.Seed); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return keptDisk, fmt.Errorf("removing the old seed of %s: %w", spec.VM, err)
	}
	if err := hypervisor.CreateSeed(ctx, spec.Seed, spec.SeedFiles); err != nil {
		return keptDisk, fmt.Errorf("creating the seed of %s: %w", spec.VM, err)
	}
	vm := platform.VMSpec{
		Name:     spec.VM,
		Tag:      VMTag,
		CPUs:     spec.CPUs,
		MemoryMB: spec.MemoryMB,
		Disks:    []platform.DiskSlot{{Slot: 0, Path: spec.Disk}},
		NICs:     []platform.NICSpec{publicNIC(spec.MAC)},
	}
	if err := hypervisor.CreateVM(ctx, vm); err != nil {
		return keptDisk, fmt.Errorf("creating the VM %s: %w", spec.VM, err)
	}
	if err := hypervisor.SetISO(ctx, spec.VM, spec.Seed); err != nil {
		return keptDisk, fmt.Errorf("inserting the seed of %s: %w", spec.VM, err)
	}
	return keptDisk, nil
}

// publicNIC is a server's public NIC: on the public switch, where it sees
// only the appliance.
func publicNIC(mac string) platform.NICSpec {
	return platform.NICSpec{MAC: mac, Switch: PublicSwitch, Port: platform.Isolated()}
}

// DeleteServer removes a server's VM (turned off first) and its files, its
// disk and its seed; what is gone already counts as removed.
func (s *System) DeleteServer(ctx context.Context, vm string, files ...string) error {
	hypervisor := s.platform.Hypervisor
	var errs []error
	if err := hypervisor.DeleteVM(ctx, vm); err != nil && !errors.Is(err, platform.ErrNotFound) {
		errs = append(errs, fmt.Errorf("removing the VM %s: %w", vm, err))
	}
	for _, file := range files {
		if err := hypervisor.DeleteDisk(ctx, file); err != nil && !errors.Is(err, platform.ErrNotFound) {
			errs = append(errs, fmt.Errorf("removing %s: %w", file, err))
		}
	}
	return errors.Join(errs...)
}

// ReconnectServer puts a server's public NIC on the public switch again:
// a stop of the lab removes the switch, and the VMs keep their NICs without
// one (13.2). A NIC's port is set only when it is added, so it goes and
// comes back with the same MAC and port.
func (s *System) ReconnectServer(ctx context.Context, vm, mac string) error {
	hypervisor := s.platform.Hypervisor
	if err := hypervisor.DetachNIC(ctx, vm, mac); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("reconnecting %s: %w", vm, err)
	}
	if err := hypervisor.AttachNIC(ctx, vm, publicNIC(mac)); err != nil {
		return fmt.Errorf("reconnecting %s: %w", vm, err)
	}
	return nil
}

// Power is a power command for a server's VM.
type Power string

// The power commands, as the server actions use them.
const (
	PowerOn       Power = "on"       // start; a running VM stays as it is
	PowerOff      Power = "off"      // turn off at once
	PowerShutdown Power = "shutdown" // ask the guest to shut down, do not wait
	PowerReset    Power = "reset"    // reset a running VM, start an off one
)

// PowerServer runs a power command on a server's VM. A reset of an off VM
// starts it, as Hetzner's reset does on a server that is off (measured
// 2026-11-08).
func (s *System) PowerServer(ctx context.Context, vm string, power Power) error {
	hypervisor := s.platform.Hypervisor
	var err error
	switch power {
	case PowerOn:
		err = hypervisor.StartVM(ctx, vm)
	case PowerOff:
		err = hypervisor.StopVM(ctx, vm)
	case PowerShutdown:
		err = hypervisor.ShutdownVM(ctx, vm)
	case PowerReset:
		err = hypervisor.ResetVM(ctx, vm)
		if errors.Is(err, platform.ErrNotRunning) {
			err = hypervisor.StartVM(ctx, vm)
		}
	default:
		err = fmt.Errorf("unknown power command %q", power)
	}
	if err != nil {
		return fmt.Errorf("power %s of %s: %w", power, vm, err)
	}
	return nil
}

// ServerExists tells whether a server's VM is there.
func (s *System) ServerExists(ctx context.Context, vm string) (bool, error) {
	_, err := s.platform.Hypervisor.VMState(ctx, vm)
	if errors.Is(err, platform.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ServerRuns tells whether a server's VM runs (a paused VM counts as
// running); a VM that is gone does not.
func (s *System) ServerRuns(ctx context.Context, vm string) (bool, error) {
	state, err := s.platform.Hypervisor.VMState(ctx, vm)
	if errors.Is(err, platform.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state.Power != platform.PowerOff, nil
}

// WaitServerOff waits until a server's VM is off, at most timeout; it
// reports whether it is.
func (s *System) WaitServerOff(ctx context.Context, vm string, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		runs, err := s.ServerRuns(ctx, vm)
		if err != nil || !runs {
			return !runs, err
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		if err := sleep(ctx, serverPoll); err != nil {
			return false, err
		}
	}
}

// serverPoll is how often WaitServerOff asks for a VM's state.
const serverPoll = 250 * time.Millisecond

// ShutdownServers shuts the servers' VMs down, all at once: the guest
// first, turned off after the timeout (13.2).
func (s *System) ShutdownServers(ctx context.Context, vms []string, timeout time.Duration) error {
	var wg sync.WaitGroup
	errs := make([]error, len(vms))
	for i, vm := range vms {
		wg.Go(func() {
			if err := s.PowerServer(ctx, vm, PowerShutdown); err != nil {
				if errors.Is(err, platform.ErrNotFound) {
					return
				}
				errs[i] = err
			}
			off, err := s.WaitServerOff(ctx, vm, timeout)
			if err == nil && !off {
				err = s.PowerServer(context.WithoutCancel(ctx), vm, PowerOff)
			}
			errs[i] = errors.Join(errs[i], err)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
