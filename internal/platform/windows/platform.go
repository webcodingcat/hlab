//go:build windows

package windows

import (
	"context"
	"log/slog"
	"net"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/platform"
)

// New returns the Windows platform; logger receives stray PowerShell output.
func New(logger *slog.Logger) platform.Platform {
	return platform.Platform{
		Hypervisor: NewHypervisor(logger),
		HostNet:    hostNet{},
		HostDNS:    hostDNS{},
		Lifecycle:  NewLifecycle(MutexName, InstanceFile()),
	}
}

// hostNet is the Hyper-V switches and the host's addresses and routes (phase 1b).
type hostNet struct{}

func (hostNet) CheckRanges(context.Context, []netip.Prefix) error {
	return platform.ErrNotImplemented
}
func (hostNet) SetUp(context.Context, platform.LinkSpec) error { return platform.ErrNotImplemented }
func (hostNet) TearDown(context.Context) error                 { return platform.ErrNotImplemented }

// hostDNS is the NRPT rules (phase 1b).
type hostDNS struct{}

func (hostDNS) Apply(context.Context, []platform.DNSRule) error { return platform.ErrNotImplemented }
func (hostDNS) Clear(context.Context) error                     { return platform.ErrNotImplemented }

// agentDialer is hv_sock (phase 1b).
type agentDialer struct{}

func (agentDialer) Dial(context.Context, string, uint32) (net.Conn, error) {
	return nil, platform.ErrNotImplemented
}
