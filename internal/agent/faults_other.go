//go:build !linux

package agent

import (
	"context"
	"net"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Faults is the Applier of the network, latency and storage box faults; it
// needs Linux and does nothing elsewhere.
type Faults struct {
	Interface string
	TrunkMAC  net.HardwareAddr
}

// NewFaults returns the faults Applier for the public interface and the
// trunk adapter with trunkMAC.
func NewFaults(iface string, trunkMAC net.HardwareAddr) *Faults {
	return &Faults{Interface: iface, TrunkMAC: trunkMAC}
}

// Name implements Applier.
func (f *Faults) Name() string { return "faults" }

// Apply implements Applier; it does nothing outside Linux.
func (f *Faults) Apply(context.Context, agentapi.Config) error { return nil }
