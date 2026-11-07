// Package platform holds the interfaces between HLab and the machine it runs on:
// the hypervisor, the host's network, the host's DNS and the process's life
// cycle. Each platform (Windows with Hyper-V, later Linux, and the fake for the
// tests) implements them.
//
// The interfaces take and return plain values (names, paths, addresses), never
// records of the state. What both hypervisors can do defines them; quirks such as
// Hyper-V's private VLANs hide behind types like PortMode.
package platform

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

// The errors a platform reports, so that callers can tell them apart with
// errors.Is. A platform wraps them with details.
var (
	// ErrNotImplemented: the platform cannot do this (yet).
	ErrNotImplemented = errors.New("not implemented")
	// ErrNotFound: the VM, disk, NIC or slot does not exist.
	ErrNotFound = errors.New("not found")
	// ErrExists: a VM, disk, NIC or slot of that name is there already.
	ErrExists = errors.New("already exists")
	// ErrRunning: the VM must be off for this.
	ErrRunning = errors.New("the VM is running")
	// ErrNotRunning: the VM must be running for this.
	ErrNotRunning = errors.New("the VM is not running")
	// ErrInUse: an address range is used on the machine already.
	ErrInUse = errors.New("in use")
	// ErrLocked: another HLab holds the data folder.
	ErrLocked = errors.New("locked by another process")
)

// Platform puts the four parts of a platform together.
type Platform struct {
	Hypervisor Hypervisor
	HostNet    HostNet
	HostDNS    HostDNS
	Lifecycle  Lifecycle
}

// Hypervisor runs the VMs and their disks.
type Hypervisor interface {
	// CreateVM creates a VM that is off: generation 2 (UEFI) with Secure Boot,
	// static memory, its disks and NICs.
	CreateVM(ctx context.Context, spec VMSpec) error
	// StartVM starts a VM; a running VM stays as it is.
	StartVM(ctx context.Context, name string) error
	// ShutdownVM asks the guest to shut down (ACPI, or Hyper-V's shutdown
	// service) and does not wait; a guest may ignore it. An off VM stays as it
	// is. The deadline (shut down, then StopVM) is the caller's: lab and
	// system decide how long to wait.
	ShutdownVM(ctx context.Context, name string) error
	// StopVM turns a VM off at once; an off VM stays as it is.
	StopVM(ctx context.Context, name string) error
	// ResetVM resets a running VM.
	ResetVM(ctx context.Context, name string) error
	// PauseVM pauses or resumes a running VM.
	PauseVM(ctx context.Context, name string, paused bool) error
	// DeleteVM deletes a VM; its disks stay.
	DeleteVM(ctx context.Context, name string) error
	// VMState reports whether a VM runs and whether its guest answers.
	VMState(ctx context.Context, name string) (VMState, error)
	// FindVMs lists the VMs that carry a tag (in their notes, or unit names),
	// sorted by name; it finds what a crashed HLab left behind.
	FindVMs(ctx context.Context, tag string) ([]FoundVM, error)
	// SetCPUMemory changes the CPUs and memory of a VM that is off.
	SetCPUMemory(ctx context.Context, name string, cpus int, memoryMB int) error
	// AttachDisk puts a disk into a free slot of a VM, also while it runs.
	AttachDisk(ctx context.Context, name string, disk DiskSlot) error
	// DetachDisk takes the disk out of a slot, also while the VM runs.
	DetachDisk(ctx context.Context, name string, slot int) error
	// AttachNIC adds a NIC to a VM, also while it runs.
	AttachNIC(ctx context.Context, name string, nic NICSpec) error
	// DetachNIC removes the NIC with this MAC address.
	DetachNIC(ctx context.Context, name string, mac string) error
	// SetISO inserts an ISO into the VM's drive, also while it runs; ""
	// ejects it.
	SetISO(ctx context.Context, name string, path string) error
	// CreateDisk creates a disk; with a parent it is a differencing disk on it.
	CreateDisk(ctx context.Context, path string, sizeGB int, parent string) error
	// ResizeDisk grows a disk; disks never shrink, as on Hetzner.
	ResizeDisk(ctx context.Context, path string, sizeGB int) error
	// FlattenDisk writes src with all its parents into one disk dst.
	FlattenDisk(ctx context.Context, src, dst string) error
	// CreateSeed writes the cloud-init seed: an ISO labeled "cidata".
	CreateSeed(ctx context.Context, path string, files map[string][]byte) error
}

// HostNet sets up the link between the host and the lab's networks.
type HostNet interface {
	// CheckRanges fails with ErrInUse if one of the ranges is used on the
	// machine already.
	CheckRanges(ctx context.Context, ranges []netip.Prefix) error
	// SetUp creates the switches, host addresses and routes, and takes over
	// what an earlier run left.
	SetUp(ctx context.Context, link LinkSpec) error
	// TearDown removes what SetUp created.
	TearDown(ctx context.Context) error
}

// HostDNS sends the host's lookups of the lab's names to the lab.
type HostDNS interface {
	// Apply makes rules the host's whole set: rules that are missing go.
	Apply(ctx context.Context, rules []DNSRule) error
	// Clear removes every rule of the lab.
	Clear(ctx context.Context) error
}

// Lifecycle is what the HLab process needs from the machine to start and stop.
type Lifecycle interface {
	// Lock makes this process the only HLab on the machine, for its data
	// folder; ErrLocked if another one runs.
	Lock(dataDir string) (unlock func(), err error)
	// WriteSecret writes a secret file that local users may read (Windows:
	// Users; Linux: the group hlab): the control token and the env tokens.
	WriteSecret(path string, data []byte) error
	// StopRequests is closed when the machine asks HLab to stop (tray, sign
	// out, SIGTERM).
	StopRequests() <-chan struct{}
	// AgentDialer reaches the agent inside the appliance (hv_sock, vsock).
	AgentDialer() AgentDialer
}

// AgentDialer opens connections to the agent inside a VM.
type AgentDialer interface {
	Dial(ctx context.Context, vm string, port uint32) (net.Conn, error)
}

// VMSpec describes a new VM.
type VMSpec struct {
	Name     string
	Tag      string // marks the VM as the lab's, for FindVMs
	CPUs     int
	MemoryMB int
	Disks    []DiskSlot
	NICs     []NICSpec
}

// DiskSlot is a disk in a slot of a VM; slot 0 holds the system disk.
type DiskSlot struct {
	Slot int
	Path string
}

// NICSpec is a NIC of a VM on a switch. Its port mode is set with the NIC
// and never changes; a NIC without a switch is not connected.
type NICSpec struct {
	MAC    string
	Switch string
	Port   PortMode
}

// PowerState is whether a VM runs.
type PowerState string

// The power states of a VM.
const (
	PowerOff     PowerState = "off"
	PowerRunning PowerState = "running"
	PowerPaused  PowerState = "paused"
)

// VMState is a VM's power state and whether its guest answers.
type VMState struct {
	Power     PowerState
	Heartbeat bool
}

// FoundVM is a VM that FindVMs found.
type FoundVM struct {
	Name  string
	Power PowerState
}

// LinkSpec is the host's side of the lab: its switch, its addresses and the
// routes into the lab through the appliance.
type LinkSpec struct {
	Switch    string
	Addresses []netip.Prefix
	Routes    []netip.Prefix
	Gateway   netip.Addr
}

// PortKind is how a switch port treats VLANs.
type PortKind string

// The kinds of switch ports.
const (
	PortIsolated    PortKind = "isolated"    // sees only promiscuous ports
	PortPromiscuous PortKind = "promiscuous" // sees every port
	PortAccess      PortKind = "access"      // in one VLAN, untagged
	PortTrunk       PortKind = "trunk"       // every VLAN, tagged
)

// PortMode is the VLAN setting of a switch port; build it with Isolated,
// Promiscuous, Access or Trunk.
type PortMode struct {
	Kind PortKind
	VLAN int // only for PortAccess
}

// Isolated is a port that sees only promiscuous ports (a server's public NIC).
func Isolated() PortMode { return PortMode{Kind: PortIsolated} }

// Promiscuous is a port that sees every port (the appliance's public NIC).
func Promiscuous() PortMode { return PortMode{Kind: PortPromiscuous} }

// Access is a port in one VLAN, untagged (a server's private NIC).
func Access(vlan int) PortMode { return PortMode{Kind: PortAccess, VLAN: vlan} }

// Trunk is a port that carries every VLAN, tagged (the appliance's private NIC).
func Trunk() PortMode { return PortMode{Kind: PortTrunk} }

// DNSRule sends the host's lookups below a domain to name servers.
type DNSRule struct {
	Domain  string // such as "hlab.internal"
	Servers []netip.Addr
}
