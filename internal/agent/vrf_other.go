//go:build !linux

package agent

import (
	"context"
	"errors"
	"net"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// VRF is the Applier of private networks on the trunk link; it needs Linux.
type VRF struct {
	TrunkMAC net.HardwareAddr
}

// NewVRF returns the Applier of private networks on the link with
// trunkMAC.
func NewVRF(trunkMAC net.HardwareAddr) *VRF { return &VRF{TrunkMAC: trunkMAC} }

// Name implements Applier.
func (v *VRF) Name() string { return "vrf" }

// Apply implements Applier; it always fails outside Linux.
func (v *VRF) Apply(context.Context, agentapi.Config) error {
	return errors.New("vrf: only supported on Linux")
}
