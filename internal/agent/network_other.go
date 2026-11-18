//go:build !linux

package agent

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Network is the Applier of the public segment; it needs Linux.
type Network struct {
	Interface string
}

// NewNetwork returns the Applier of the public segment on iface.
func NewNetwork(iface string) *Network { return &Network{Interface: iface} }

// Name implements Applier.
func (n *Network) Name() string { return "network" }

// Apply implements Applier; it always fails outside Linux.
func (n *Network) Apply(context.Context, agentapi.Config) error {
	return errors.New("network: only supported on Linux")
}
