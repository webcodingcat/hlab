// Package platform describes everything about VMs and disks that depends on
// the hypervisor. platform/hyperv implements it.
package platform

import (
	"context"
	"fmt"
	"net"
	"time"
)

// Platform manages switches, VMs, adapters and disks.
type Platform interface {
	EnsureSwitch(ctx context.Context, spec SwitchSpec) error
	// DeleteSwitch removes the switch with exactly this name; a missing
	// switch is no error.
	DeleteSwitch(ctx context.Context, name string) error
	CreateVM(ctx context.Context, spec VMSpec) error
	SetVM(ctx context.Context, name string, spec VMSpec) error // cpu, ram (stopped only)
	DeleteVM(ctx context.Context, name string) error
	// SetStopAction sets only the VM's automatic stop action, the action
	// the host applies to it when Windows shuts down. Hyper-V refuses a
	// change on a running VM with an InvalidState error.
	SetStopAction(ctx context.Context, name string, a StopAction) error
	// VMs returns the names of the VMs whose name starts with prefix.
	VMs(ctx context.Context, prefix string) ([]string, error)
	Power(ctx context.Context, name string, op PowerOp) error
	State(ctx context.Context, name string) (VMState, error)
	// AttachNIC and DetachNIC also work on a running VM (hot add and
	// remove).
	AttachNIC(ctx context.Context, vm string, nic NICSpec) error
	DetachNIC(ctx context.Context, vm, mac string) error
	// SetNICDeny replaces the MAC deny ACLs (both directions) of the VM's
	// adapter with MAC mac by deny, running or not; idempotent.
	SetNICDeny(ctx context.Context, vm, mac string, deny []net.HardwareAddr) error
	// CreateDisk creates a thin VHDX, optionally as a copy of the base from.
	CreateDisk(ctx context.Context, path string, sizeBytes int64, from string, page83 [16]byte) error
	// CreateChildDisk creates the differencing VHDX path on the VHDX parent
	// with a new disk identifier.
	CreateChildDisk(ctx context.Context, path, parent string) error
	ResizeDisk(ctx context.Context, path string, sizeBytes int64) error
	AttachDisk(ctx context.Context, vm, path string, lun int) error // hot-add on SCSI controller 0
	DetachDisk(ctx context.Context, vm, path string) error
	// CompactDisk shrinks the detached dynamic VHDX path to the blocks in
	// use (Optimize-VHD -Mode Full).
	CompactDisk(ctx context.Context, path string) error
	// SetDVD sets the DVD drive on SCSI controller 1: slot 0 cidata, slot 1
	// user ISO; "" ejects.
	SetDVD(ctx context.Context, vm string, slot int, iso string) error
	SetBootOrder(ctx context.Context, vm string, order []BootDevice) error
	// Snapshot makes a crash-consistent copy of a running or stopped disk:
	// target becomes a standalone dynamic VHDX with a new disk identifier.
	Snapshot(ctx context.Context, vm, diskPath, target string) error
	// MetricsAll reads the counters of all vms in one platform call. A VM
	// that is not running (no counter instances) gets a zero sample; a VM
	// that does not exist is left out of the map.
	MetricsAll(ctx context.Context, vms []string) (map[string]Sample, error)
	HostResources(ctx context.Context) (Resources, error)
}

// SwitchSpec describes the internal switch; the host adapter is isolated.
type SwitchSpec struct {
	Name                         string
	PVLANPrimary, PVLANSecondary int
}

// VMSpec describes a VM.
type VMSpec struct {
	Name       string // "<tag>-server-<id>", "<tag>-appliance"
	CPUs       int
	MemoryMB   int64  // static
	Folder     string // VM configuration folder in the data folder
	StopAction StopAction
}

// PowerOp is a power operation.
type PowerOp int

// Power operations.
const (
	PowerStart    PowerOp = iota
	PowerStop             // hard
	PowerShutdown         // Shutdown integration service
	PowerReboot           // Shutdown integration service
	PowerReset
	PowerSave
	// PowerShutdownRetry is a shutdown that asks a still booting guest
	// again until it accepts or a deadline has passed.
	PowerShutdownRetry
)

func (o PowerOp) String() string {
	switch o {
	case PowerStart:
		return "start"
	case PowerStop:
		return "stop"
	case PowerShutdown:
		return "shutdown"
	case PowerReboot:
		return "reboot"
	case PowerReset:
		return "reset"
	case PowerSave:
		return "save"
	case PowerShutdownRetry:
		return "shutdown-retry"
	}
	return fmt.Sprintf("PowerOp(%d)", int(o))
}

// StopAction is what the host does with a VM when it shuts down.
type StopAction int

// Stop actions.
const (
	StopSave StopAction = iota
	StopShutdown
)

func (a StopAction) String() string {
	switch a {
	case StopSave:
		return "save"
	case StopShutdown:
		return "shutdown"
	}
	return fmt.Sprintf("StopAction(%d)", int(a))
}

// VMState is the state of a VM.
type VMState int

// VM states.
const (
	StateOff VMState = iota
	StateStarting
	StateRunning
	StateStopping
	StateSaved
	StatePaused
	StateOther
)

func (s VMState) String() string {
	switch s {
	case StateOff:
		return "off"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateSaved:
		return "saved"
	case StatePaused:
		return "paused"
	case StateOther:
		return "other"
	}
	return fmt.Sprintf("VMState(%d)", int(s))
}

// Segment is where a network adapter is connected: the public PVLAN
// (isolated port), a private network's access VLAN 2..4092 or the trunk.
// Promiscuous (with PublicPVLAN) makes the adapter the PVLAN's promiscuous
// port; only the appliance's public adapter uses it. Trunk carries VLANs
// 2..4092 tagged with native VLAN 0; only the appliance's second adapter
// uses it.
type Segment struct {
	PublicPVLAN bool
	Promiscuous bool
	VLAN        int
	Trunk       bool
}

// SegmentFor is the one place deciding the segment of an adapter
// (decisions/locations.md): all locations share one segment, so a public
// adapter (networkID 0) is always on the public PVLAN. Private networks do
// not exist yet; their adapters get an access VLAN once they do.
func SegmentFor(location string, networkID int64) Segment {
	_ = location
	if networkID == 0 {
		return Segment{PublicPVLAN: true}
	}
	return Segment{}
}

// NICSpec describes a network adapter.
type NICSpec struct {
	MAC      net.HardwareAddr
	Segment  Segment
	DenyMACs []net.HardwareAddr // other members of the same private network
}

// BootDevice is one entry of the boot order: Kind "disk" (Slot = LUN) or
// "dvd" (Slot = 0|1).
type BootDevice struct {
	Kind string
	Slot int
}

// Sample holds counters of a VM. CPUPercent is computed by the platform
// between two MetricsAll reads of the same VM (per vCPU summed, 100 = one vCPU
// fully busy; 0 on the first call). All other counters are cumulative and
// restart at 0 when the VM restarts.
type Sample struct {
	Time                          time.Time
	CPUPercent                    float64
	DiskReadBytes, DiskWriteBytes uint64
	NetRxBytes, NetTxBytes        uint64
	DiskReadOps, DiskWriteOps     uint64
	NetRxPackets, NetTxPackets    uint64
}

// Resources are the host's resources.
type Resources struct {
	MemoryMB, FreeMemoryMB int64
	LogicalCPUs            int
	FreeDiskBytes          int64
}

// ErrorKind classifies a platform error; the action worker maps it to a
// Hetzner error code.
type ErrorKind int

// Error kinds.
const (
	Failed ErrorKind = iota
	NotFound
	InvalidState
	NoResources
)

func (k ErrorKind) String() string {
	switch k {
	case Failed:
		return "failed"
	case NotFound:
		return "not found"
	case InvalidState:
		return "invalid state"
	case NoResources:
		return "no resources"
	}
	return fmt.Sprintf("ErrorKind(%d)", int(k))
}

// Error is the error every Platform method returns.
type Error struct {
	Kind ErrorKind
	Op   string // the method, for example "CreateVM"
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Op + ": " + e.Kind.String()
	}
	return e.Op + ": " + e.Kind.String() + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
