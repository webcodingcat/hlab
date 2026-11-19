package loadbalancers

import (
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
)

// Contribute implements resource.NetContributor: one agentapi.LoadBalancer
// per load balancer.
func (m *Module) Contribute(store.Tx, *agentapi.Config) error { return nil }
