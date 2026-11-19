//go:build !linux

package agent

import (
	"context"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Firewall is the Applier of the server firewalls; it needs Linux and does
// nothing elsewhere.
type Firewall struct {
	Interface string
}

// NewFirewall returns the firewall Applier for the public interface.
func NewFirewall(iface string) *Firewall { return &Firewall{Interface: iface} }

// Name implements Applier.
func (f *Firewall) Name() string { return "firewall" }

// Apply implements Applier; it does nothing outside Linux.
func (f *Firewall) Apply(context.Context, agentapi.Config) error { return nil }
