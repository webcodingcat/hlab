//go:build windows

package windows

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows/pshost"
)

//go:embed hyperv.ps1
var hyperVScript string

// Hypervisor is Hyper-V, driven by the commands of hyperv.ps1 in long-running
// PowerShell processes. It needs administrator rights or the group Hyper-V
// Administrators.
type Hypervisor struct {
	host *pshost.Host
}

// NewHypervisor returns Hyper-V; logger receives stray PowerShell output.
func NewHypervisor(logger *slog.Logger) *Hypervisor {
	return &Hypervisor{host: pshost.New(hyperVScript, pshost.Options{Logger: logger})}
}

// Close stops the PowerShell processes.
func (h *Hypervisor) Close() error {
	return h.host.Close()
}

// kinds maps the kinds of failures in hyperv.ps1 and hostnet.ps1 to the
// platform's errors.
var kinds = map[string]error{
	"not_found":   platform.ErrNotFound,
	"exists":      platform.ErrExists,
	"running":     platform.ErrRunning,
	"not_running": platform.ErrNotRunning,
	"in_use":      platform.ErrInUse,
}

// call runs a command of a script in host and turns its failure into the
// platform's error.
func call(ctx context.Context, host *pshost.Host, command string, args, result any) error {
	err := host.Call(ctx, command, args, result)
	var failure *pshost.Error
	if errors.As(err, &failure) {
		if known, found := kinds[failure.Kind]; found {
			return fmt.Errorf("%s: %w", failure.Message, known)
		}
	}
	return err
}

// call runs a command of hyperv.ps1.
func (h *Hypervisor) call(ctx context.Context, command string, args, result any) error {
	return call(ctx, h.host, command, args, result)
}

// vmArgs names a VM.
type vmArgs struct {
	Name string `json:"name"`
}

// nicArgs is a NIC as hyperv.ps1 takes it.
type nicArgs struct {
	MAC    string   `json:"mac"`
	Switch string   `json:"switch"`
	Port   portArgs `json:"port"`
}

// portArgs is a port mode as hyperv.ps1 takes it.
type portArgs struct {
	Kind platform.PortKind `json:"kind"`
	VLAN int               `json:"vlan"`
}

func toNIC(nic platform.NICSpec) nicArgs {
	return nicArgs{MAC: nic.MAC, Switch: nic.Switch, Port: portArgs{Kind: nic.Port.Kind, VLAN: nic.Port.VLAN}}
}

// diskArgs is a disk in a slot as hyperv.ps1 takes it.
type diskArgs struct {
	Slot int    `json:"slot"`
	Path string `json:"path"`
}

// CreateVM creates a VM that is off: generation 2 with Secure Boot (Microsoft
// UEFI CA), static memory, the disks on SCSI controller 0 at their slots, an
// empty DVD drive on controller 1 and the NICs. If a step fails, the VM is
// removed again.
func (h *Hypervisor) CreateVM(ctx context.Context, spec platform.VMSpec) error {
	disks := []diskArgs{}
	for _, disk := range spec.Disks {
		disks = append(disks, diskArgs(disk))
	}
	nics := []nicArgs{}
	for _, nic := range spec.NICs {
		nics = append(nics, toNIC(nic))
	}
	return h.call(ctx, "create-vm", map[string]any{
		"name": spec.Name, "tag": spec.Tag, "cpus": spec.CPUs, "memory_mb": spec.MemoryMB,
		"disks": disks, "nics": nics,
	}, nil)
}

// StartVM starts a VM that is off.
func (h *Hypervisor) StartVM(ctx context.Context, name string) error {
	return h.call(ctx, "start-vm", vmArgs{Name: name}, nil)
}

// ShutdownVM asks the guest's shutdown service to shut down and does not
// wait. Hyper-V has no ACPI button; a guest without the service ignores the
// request, as a guest without ACPI does on Hetzner.
func (h *Hypervisor) ShutdownVM(ctx context.Context, name string) error {
	return h.call(ctx, "shutdown-vm", vmArgs{Name: name}, nil)
}

// StopVM turns a VM off at once.
func (h *Hypervisor) StopVM(ctx context.Context, name string) error {
	return h.call(ctx, "stop-vm", vmArgs{Name: name}, nil)
}

// ResetVM resets a running VM.
func (h *Hypervisor) ResetVM(ctx context.Context, name string) error {
	return h.call(ctx, "reset-vm", vmArgs{Name: name}, nil)
}

// PauseVM pauses or resumes a VM.
func (h *Hypervisor) PauseVM(ctx context.Context, name string, paused bool) error {
	return h.call(ctx, "pause-vm", map[string]any{"name": name, "paused": paused}, nil)
}

// DeleteVM turns a VM off and deletes it; its disks stay.
func (h *Hypervisor) DeleteVM(ctx context.Context, name string) error {
	return h.call(ctx, "delete-vm", vmArgs{Name: name}, nil)
}

// VMState reports a VM's power and whether its heartbeat service answers.
func (h *Hypervisor) VMState(ctx context.Context, name string) (platform.VMState, error) {
	var state struct {
		Power     platform.PowerState `json:"power"`
		Heartbeat bool                `json:"heartbeat"`
	}
	if err := h.call(ctx, "vm-state", vmArgs{Name: name}, &state); err != nil {
		return platform.VMState{}, err
	}
	return platform.VMState{Power: state.Power, Heartbeat: state.Heartbeat}, nil
}

// FindVMs lists the VMs whose notes are the tag.
func (h *Hypervisor) FindVMs(ctx context.Context, tag string) ([]platform.FoundVM, error) {
	var found []struct {
		Name  string              `json:"name"`
		Power platform.PowerState `json:"power"`
	}
	if err := h.call(ctx, "find-vms", map[string]string{"tag": tag}, &found); err != nil {
		return nil, err
	}
	vms := []platform.FoundVM{}
	for _, vm := range found {
		vms = append(vms, platform.FoundVM{Name: vm.Name, Power: vm.Power})
	}
	return vms, nil
}

// SetCPUMemory changes the CPUs and the static memory of a VM that is off.
func (h *Hypervisor) SetCPUMemory(ctx context.Context, name string, cpus int, memoryMB int) error {
	return h.call(ctx, "set-cpu-memory", map[string]any{"name": name, "cpus": cpus, "memory_mb": memoryMB}, nil)
}

// AttachDisk puts a disk into a free slot, also while the VM runs.
func (h *Hypervisor) AttachDisk(ctx context.Context, name string, disk platform.DiskSlot) error {
	return h.call(ctx, "attach-disk", map[string]any{"name": name, "disk": diskArgs(disk)}, nil)
}

// DetachDisk takes the disk out of a slot, also while the VM runs.
func (h *Hypervisor) DetachDisk(ctx context.Context, name string, slot int) error {
	return h.call(ctx, "detach-disk", map[string]any{"name": name, "slot": slot}, nil)
}

// AttachNIC adds a NIC, also while the VM runs.
func (h *Hypervisor) AttachNIC(ctx context.Context, name string, nic platform.NICSpec) error {
	return h.call(ctx, "attach-nic", map[string]any{"name": name, "nic": toNIC(nic)}, nil)
}

// DetachNIC removes the NIC with this MAC address.
func (h *Hypervisor) DetachNIC(ctx context.Context, name string, mac string) error {
	return h.call(ctx, "detach-nic", map[string]any{"name": name, "mac": mac}, nil)
}

// SetISO puts an ISO into the VM's DVD drive; "" ejects it.
func (h *Hypervisor) SetISO(ctx context.Context, name string, path string) error {
	return h.call(ctx, "set-iso", map[string]any{"name": name, "path": path}, nil)
}

// CreateDisk creates a dynamic VHDX, or with a parent a differencing one,
// grown to sizeGB if the parent is smaller. It creates the folder.
func (h *Hypervisor) CreateDisk(ctx context.Context, path string, sizeGB int, parent string) error {
	return h.call(ctx, "create-disk", map[string]any{"path": path, "size_gb": sizeGB, "parent": parent}, nil)
}

// ResizeDisk grows a VHDX, also while a running VM uses it.
func (h *Hypervisor) ResizeDisk(ctx context.Context, path string, sizeGB int) error {
	return h.call(ctx, "resize-disk", map[string]any{"path": path, "size_gb": sizeGB}, nil)
}

// FlattenDisk writes src with its parents into a new dynamic VHDX dst
// (Convert-VHD).
func (h *Hypervisor) FlattenDisk(ctx context.Context, src, dst string) error {
	return h.call(ctx, "flatten-disk", map[string]any{"src": src, "dst": dst}, nil)
}

// DeleteDisk deletes a VHDX that no VM uses.
func (h *Hypervisor) DeleteDisk(ctx context.Context, path string) error {
	return h.call(ctx, "delete-disk", map[string]any{"path": path}, nil)
}

// vmID returns the ID of a VM, which hv_sock addresses it by.
func (h *Hypervisor) vmID(ctx context.Context, name string) (string, error) {
	var id string
	err := h.call(ctx, "vm-id", vmArgs{Name: name}, &id)
	return id, err
}

// HostResources reports the host's logical processors and physical memory.
func (h *Hypervisor) HostResources(ctx context.Context) (platform.HostResources, error) {
	var resources struct {
		Threads  int `json:"threads"`
		MemoryMB int `json:"memory_mb"`
	}
	if err := h.call(ctx, "host-resources", map[string]any{}, &resources); err != nil {
		return platform.HostResources{}, err
	}
	return platform.HostResources{Threads: resources.Threads, MemoryMB: resources.MemoryMB}, nil
}

// DiskSpace reports the size of the drive that holds path and the space
// free on it for the caller (which a quota may make less than the drive's).
func (h *Hypervisor) DiskSpace(ctx context.Context, path string) (platform.DiskSpace, error) {
	var space struct {
		TotalMB int64 `json:"total_mb"`
		FreeMB  int64 `json:"free_mb"`
	}
	if err := h.call(ctx, "disk-space", map[string]string{"path": path}, &space); err != nil {
		return platform.DiskSpace{}, err
	}
	return platform.DiskSpace{TotalMB: space.TotalMB, FreeMB: space.FreeMB}, nil
}

// CreateSeed writes an ISO labeled "cidata" with the files (IMAPI2).
func (h *Hypervisor) CreateSeed(ctx context.Context, path string, files map[string][]byte) error {
	return h.call(ctx, "create-seed", map[string]any{"path": path, "files": files}, nil)
}
