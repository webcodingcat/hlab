//go:build windows

package windows

import (
	"errors"
	"log/slog"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Platform is the Windows platform: Hyper-V, the host network, the NRPT
// rules and the life cycle. Close stops their PowerShell processes.
type Platform struct {
	platform.Platform
	hypervisor *Hypervisor
	hostNet    *HostNet
	hostDNS    *HostDNS
}

// New returns the Windows platform; logger receives stray PowerShell output.
func New(logger *slog.Logger) *Platform {
	hypervisor := NewHypervisor(logger)
	hostNet := NewHostNet(logger)
	hostDNS := NewHostDNS(logger)
	return &Platform{
		Platform: platform.Platform{
			Hypervisor: hypervisor,
			HostNet:    hostNet,
			HostDNS:    hostDNS,
			Lifecycle:  NewLifecycle(MutexName, InstanceFile(), NewAgentDialer(hypervisor)),
		},
		hypervisor: hypervisor,
		hostNet:    hostNet,
		hostDNS:    hostDNS,
	}
}

// NewAgentDialer returns the dialer that reaches agents over hv_sock; it
// finds a VM's ID through the hypervisor.
func NewAgentDialer(hypervisor *Hypervisor) platform.AgentDialer {
	return hvsockDialer{hypervisor: hypervisor}
}

// Close stops the PowerShell processes of every part.
func (p *Platform) Close() error {
	return errors.Join(p.hypervisor.Close(), p.hostNet.Close(), p.hostDNS.Close())
}
