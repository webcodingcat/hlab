//go:build !linux

package agent

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// Isolation is the Applier of the isolation backstop; it needs Linux.
type Isolation struct {
	Interface string
}

// NewIsolation returns the isolation Applier for the public interface.
func NewIsolation(iface string) *Isolation { return &Isolation{Interface: iface} }

// Name implements Applier.
func (n *Isolation) Name() string { return "isolation" }

// Apply implements Applier; it always fails outside Linux.
func (n *Isolation) Apply(context.Context, agentapi.Config) error {
	return errors.New("isolation: only supported on Linux")
}
