//go:build windows

package windows

import (
	"context"
	"io"
	"net"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/platform"
)

// New returns the Windows platform.
func New() platform.Platform {
	return platform.Platform{
		Hypervisor: hypervisor{},
		HostNet:    hostNet{},
		HostDNS:    hostDNS{},
		Lifecycle:  NewLifecycle(MutexName, InstanceFile()),
	}
}

// hypervisor is Hyper-V (phase 1b).
type hypervisor struct{}

func (hypervisor) CreateVM(context.Context, platform.VMSpec) error {
	return platform.ErrNotImplemented
}
func (hypervisor) StartVM(context.Context, string) error    { return platform.ErrNotImplemented }
func (hypervisor) ShutdownVM(context.Context, string) error { return platform.ErrNotImplemented }
func (hypervisor) StopVM(context.Context, string) error     { return platform.ErrNotImplemented }
func (hypervisor) ResetVM(context.Context, string) error    { return platform.ErrNotImplemented }
func (hypervisor) PauseVM(context.Context, string, bool) error {
	return platform.ErrNotImplemented
}
func (hypervisor) DeleteVM(context.Context, string) error { return platform.ErrNotImplemented }
func (hypervisor) VMState(context.Context, string) (platform.VMState, error) {
	return platform.VMState{}, platform.ErrNotImplemented
}
func (hypervisor) FindVMs(context.Context, string) ([]platform.FoundVM, error) {
	return nil, platform.ErrNotImplemented
}
func (hypervisor) SetCPUMemory(context.Context, string, int, int) error {
	return platform.ErrNotImplemented
}
func (hypervisor) AttachDisk(context.Context, string, platform.DiskSlot) error {
	return platform.ErrNotImplemented
}
func (hypervisor) DetachDisk(context.Context, string, int) error {
	return platform.ErrNotImplemented
}
func (hypervisor) AttachNIC(context.Context, string, platform.NICSpec) error {
	return platform.ErrNotImplemented
}
func (hypervisor) DetachNIC(context.Context, string, string) error {
	return platform.ErrNotImplemented
}
func (hypervisor) SetISO(context.Context, string, string) error {
	return platform.ErrNotImplemented
}
func (hypervisor) CreateDisk(context.Context, string, int, string) error {
	return platform.ErrNotImplemented
}
func (hypervisor) ResizeDisk(context.Context, string, int) error {
	return platform.ErrNotImplemented
}
func (hypervisor) FlattenDisk(context.Context, string, string) error {
	return platform.ErrNotImplemented
}
func (hypervisor) CheckpointDisk(context.Context, string) (string, func() error, error) {
	return "", nil, platform.ErrNotImplemented
}
func (hypervisor) CreateSeed(context.Context, string, map[string][]byte) error {
	return platform.ErrNotImplemented
}
func (hypervisor) Counters(context.Context, []string) (map[string]platform.VMCounters, error) {
	return nil, platform.ErrNotImplemented
}
func (hypervisor) Console(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, platform.ErrNotImplemented
}

// hostNet is the Hyper-V switches and the host's addresses and routes (phase 1b).
type hostNet struct{}

func (hostNet) CheckRanges(context.Context, []netip.Prefix) error {
	return platform.ErrNotImplemented
}
func (hostNet) SetUp(context.Context, platform.LinkSpec) error { return platform.ErrNotImplemented }
func (hostNet) TearDown(context.Context) error                 { return platform.ErrNotImplemented }
func (hostNet) SetPortVLAN(context.Context, string, string, platform.PortMode) error {
	return platform.ErrNotImplemented
}

// hostDNS is the NRPT rules (phase 1b).
type hostDNS struct{}

func (hostDNS) Apply(context.Context, []platform.DNSRule) error { return platform.ErrNotImplemented }
func (hostDNS) Clear(context.Context) error                     { return platform.ErrNotImplemented }

// agentDialer is hv_sock (phase 1b).
type agentDialer struct{}

func (agentDialer) Dial(context.Context, string, uint32) (net.Conn, error) {
	return nil, platform.ErrNotImplemented
}
