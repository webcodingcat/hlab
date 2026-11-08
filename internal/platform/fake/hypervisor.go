package fake

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/webcodingcat/hlab/internal/platform"
)

// vm is a VM of the fake machine.
type vm struct {
	tag      string
	cpus     int
	memoryMB int
	power    platform.PowerState
	disks    map[int]string // slot → path
	nics     map[string]platform.NICSpec
	iso      string
}

// disk is a disk file of the fake machine.
type disk struct {
	sizeGB int
	parent string
}

// Hypervisor is the fake machine's hypervisor.
type Hypervisor struct {
	fake *Fake
}

// vm returns a VM by name; the caller holds the mutex.
func (h *Hypervisor) vm(name string) (*vm, error) {
	machine, found := h.fake.vms[name]
	if !found {
		return nil, fmt.Errorf("VM %q: %w", name, platform.ErrNotFound)
	}
	return machine, nil
}

// CreateVM creates a VM that is off.
func (h *Hypervisor) CreateVM(ctx context.Context, spec platform.VMSpec) error {
	if err := h.fake.enter(ctx, "CreateVM", spec.Name); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	if _, found := h.fake.vms[spec.Name]; found {
		return fmt.Errorf("VM %q: %w", spec.Name, platform.ErrExists)
	}
	machine := &vm{
		tag:      spec.Tag,
		cpus:     spec.CPUs,
		memoryMB: spec.MemoryMB,
		power:    platform.PowerOff,
		disks:    map[int]string{},
		nics:     map[string]platform.NICSpec{},
	}
	for _, slot := range spec.Disks {
		if _, found := h.fake.disks[slot.Path]; !found {
			return fmt.Errorf("disk %q: %w", slot.Path, platform.ErrNotFound)
		}
		machine.disks[slot.Slot] = slot.Path
	}
	for _, nic := range spec.NICs {
		machine.nics[nic.MAC] = nic
	}
	h.fake.vms[spec.Name] = machine
	return nil
}

// setPower moves a VM from one of the allowed power states to another.
func (h *Hypervisor) setPower(ctx context.Context, method, name string, to platform.PowerState, from ...platform.PowerState) error {
	if err := h.fake.enter(ctx, method, name); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if machine.power == to {
		return nil
	}
	if !slices.Contains(from, machine.power) {
		reason := platform.ErrRunning
		if machine.power == platform.PowerOff {
			reason = platform.ErrNotRunning
		}
		return fmt.Errorf("VM %q is %s and cannot become %s: %w", name, machine.power, to, reason)
	}
	machine.power = to
	return nil
}

// StartVM starts a VM that is off.
func (h *Hypervisor) StartVM(ctx context.Context, name string) error {
	return h.setPower(ctx, "StartVM", name, platform.PowerRunning, platform.PowerOff)
}

// ShutdownVM turns a running VM off; the fake's guest obeys at once.
func (h *Hypervisor) ShutdownVM(ctx context.Context, name string) error {
	return h.setPower(ctx, "ShutdownVM", name, platform.PowerOff, platform.PowerRunning)
}

// StopVM turns a VM off at once, also a paused one.
func (h *Hypervisor) StopVM(ctx context.Context, name string) error {
	return h.setPower(ctx, "StopVM", name, platform.PowerOff, platform.PowerRunning, platform.PowerPaused)
}

// ResetVM resets a running VM; it keeps running.
func (h *Hypervisor) ResetVM(ctx context.Context, name string) error {
	if err := h.fake.enter(ctx, "ResetVM", name); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if machine.power != platform.PowerRunning {
		return fmt.Errorf("VM %q is %s: %w", name, machine.power, platform.ErrNotRunning)
	}
	return nil
}

// PauseVM pauses or resumes a running VM.
func (h *Hypervisor) PauseVM(ctx context.Context, name string, paused bool) error {
	if paused {
		return h.setPower(ctx, "PauseVM", name, platform.PowerPaused, platform.PowerRunning)
	}
	return h.setPower(ctx, "PauseVM", name, platform.PowerRunning, platform.PowerPaused)
}

// DeleteVM deletes a VM; its disks stay.
func (h *Hypervisor) DeleteVM(ctx context.Context, name string) error {
	if err := h.fake.enter(ctx, "DeleteVM", name); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	if _, err := h.vm(name); err != nil {
		return err
	}
	delete(h.fake.vms, name)
	return nil
}

// VMState reports a VM's power; the fake's guest answers whenever it runs.
func (h *Hypervisor) VMState(ctx context.Context, name string) (platform.VMState, error) {
	if err := h.fake.enter(ctx, "VMState", name); err != nil {
		return platform.VMState{}, err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return platform.VMState{}, err
	}
	return platform.VMState{Power: machine.power, Heartbeat: machine.power == platform.PowerRunning}, nil
}

// FindVMs lists the VMs with a tag, sorted by name.
func (h *Hypervisor) FindVMs(ctx context.Context, tag string) ([]platform.FoundVM, error) {
	if err := h.fake.enter(ctx, "FindVMs", tag); err != nil {
		return nil, err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	found := []platform.FoundVM{}
	for name, machine := range h.fake.vms {
		if machine.tag == tag {
			found = append(found, platform.FoundVM{Name: name, Power: machine.power})
		}
	}
	slices.SortFunc(found, func(a, b platform.FoundVM) int { return strings.Compare(a.Name, b.Name) })
	return found, nil
}

// SetCPUMemory changes the CPUs and memory of a VM that is off.
func (h *Hypervisor) SetCPUMemory(ctx context.Context, name string, cpus int, memoryMB int) error {
	if err := h.fake.enter(ctx, "SetCPUMemory", name, cpus, memoryMB); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if machine.power != platform.PowerOff {
		return fmt.Errorf("VM %q: %w", name, platform.ErrRunning)
	}
	machine.cpus, machine.memoryMB = cpus, memoryMB
	return nil
}

// AttachDisk puts an existing disk into a free slot.
func (h *Hypervisor) AttachDisk(ctx context.Context, name string, slot platform.DiskSlot) error {
	if err := h.fake.enter(ctx, "AttachDisk", name, slot.Slot, slot.Path); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if _, taken := machine.disks[slot.Slot]; taken {
		return fmt.Errorf("slot %d of VM %q: %w", slot.Slot, name, platform.ErrExists)
	}
	if _, found := h.fake.disks[slot.Path]; !found {
		return fmt.Errorf("disk %q: %w", slot.Path, platform.ErrNotFound)
	}
	machine.disks[slot.Slot] = slot.Path
	return nil
}

// DetachDisk empties a slot.
func (h *Hypervisor) DetachDisk(ctx context.Context, name string, slot int) error {
	if err := h.fake.enter(ctx, "DetachDisk", name, slot); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if _, taken := machine.disks[slot]; !taken {
		return fmt.Errorf("slot %d of VM %q: %w", slot, name, platform.ErrNotFound)
	}
	delete(machine.disks, slot)
	return nil
}

// AttachNIC adds a NIC with a MAC address the VM does not have yet.
func (h *Hypervisor) AttachNIC(ctx context.Context, name string, nic platform.NICSpec) error {
	if err := h.fake.enter(ctx, "AttachNIC", name, nic.MAC); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if _, taken := machine.nics[nic.MAC]; taken {
		return fmt.Errorf("NIC %s of VM %q: %w", nic.MAC, name, platform.ErrExists)
	}
	machine.nics[nic.MAC] = nic
	return nil
}

// DetachNIC removes a NIC.
func (h *Hypervisor) DetachNIC(ctx context.Context, name string, mac string) error {
	if err := h.fake.enter(ctx, "DetachNIC", name, mac); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if _, found := machine.nics[mac]; !found {
		return fmt.Errorf("NIC %s of VM %q: %w", mac, name, platform.ErrNotFound)
	}
	delete(machine.nics, mac)
	return nil
}

// SetISO inserts or ejects an ISO.
func (h *Hypervisor) SetISO(ctx context.Context, name string, path string) error {
	if err := h.fake.enter(ctx, "SetISO", name, path); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	machine, err := h.vm(name)
	if err != nil {
		return err
	}
	if _, found := h.fake.disks[path]; path != "" && !found {
		return fmt.Errorf("ISO %q: %w", path, platform.ErrNotFound)
	}
	machine.iso = path
	return nil
}

// CreateDisk creates an empty or a differencing disk.
func (h *Hypervisor) CreateDisk(ctx context.Context, path string, sizeGB int, parent string) error {
	if err := h.fake.enter(ctx, "CreateDisk", path, sizeGB, parent); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	if _, found := h.fake.disks[path]; found {
		return fmt.Errorf("disk %q: %w", path, platform.ErrExists)
	}
	if parent != "" {
		if _, found := h.fake.disks[parent]; !found {
			return fmt.Errorf("parent disk %q: %w", parent, platform.ErrNotFound)
		}
	}
	h.fake.disks[path] = &disk{sizeGB: sizeGB, parent: parent}
	return nil
}

// ResizeDisk grows a disk; a smaller size is refused.
func (h *Hypervisor) ResizeDisk(ctx context.Context, path string, sizeGB int) error {
	if err := h.fake.enter(ctx, "ResizeDisk", path, sizeGB); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	file, found := h.fake.disks[path]
	if !found {
		return fmt.Errorf("disk %q: %w", path, platform.ErrNotFound)
	}
	if sizeGB < file.sizeGB {
		return fmt.Errorf("disk %q has %d GB and cannot shrink to %d GB", path, file.sizeGB, sizeGB)
	}
	file.sizeGB = sizeGB
	return nil
}

// FlattenDisk writes src into a new disk dst without a parent.
func (h *Hypervisor) FlattenDisk(ctx context.Context, src, dst string) error {
	if err := h.fake.enter(ctx, "FlattenDisk", src, dst); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	file, found := h.fake.disks[src]
	if !found {
		// a disk file the system wrote itself, such as the image cache's
		// VHD: the fake knows it only from the file system
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("disk %q: %w", src, platform.ErrNotFound)
		}
		file = &disk{}
	}
	if _, found := h.fake.disks[dst]; found {
		return fmt.Errorf("disk %q: %w", dst, platform.ErrExists)
	}
	h.fake.disks[dst] = &disk{sizeGB: file.sizeGB}
	return nil
}

// DeleteDisk deletes a disk that no VM uses.
func (h *Hypervisor) DeleteDisk(ctx context.Context, path string) error {
	if err := h.fake.enter(ctx, "DeleteDisk", path); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	if _, found := h.fake.disks[path]; !found {
		return fmt.Errorf("disk %q: %w", path, platform.ErrNotFound)
	}
	for name, machine := range h.fake.vms {
		if slices.Contains(slices.Collect(maps.Values(machine.disks)), path) {
			return fmt.Errorf("disk %q of VM %q: %w", path, name, platform.ErrInUse)
		}
	}
	delete(h.fake.disks, path)
	return nil
}

// HostResources reports the fake host's threads and memory
// (SetHostResources).
func (h *Hypervisor) HostResources(ctx context.Context) (platform.HostResources, error) {
	if err := h.fake.enter(ctx, "HostResources"); err != nil {
		return platform.HostResources{}, err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	return h.fake.resources, nil
}

// CreateSeed keeps the seed's files in memory.
func (h *Hypervisor) CreateSeed(ctx context.Context, path string, files map[string][]byte) error {
	if err := h.fake.enter(ctx, "CreateSeed", path); err != nil {
		return err
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	h.fake.seeds[path] = files
	h.fake.disks[path] = &disk{}
	return nil
}
