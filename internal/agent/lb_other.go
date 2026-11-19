//go:build !linux

package agent

import (
	"context"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// LoadBalancers is the Applier of the load balancers; it needs Linux and
// does nothing elsewhere.
type LoadBalancers struct {
	Dir string
}

// NewLoadBalancers returns the load balancer Applier keeping its files
// below dir.
func NewLoadBalancers(dir string) *LoadBalancers { return &LoadBalancers{Dir: dir} }

// Name implements Applier.
func (l *LoadBalancers) Name() string { return "loadbalancers" }

// Apply implements Applier; it does nothing outside Linux.
func (l *LoadBalancers) Apply(context.Context, agentapi.Config) error { return nil }
