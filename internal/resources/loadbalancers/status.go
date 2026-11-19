package loadbalancers

import (
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

// lbStatus holds the health and counters read from the agent.
type lbStatus struct{} //nolint:unused // filled by plan 08-17

// statusRoutes registers the metrics endpoint.
func (m *Module) statusRoutes(resource.Registrar) {}

// Jobs implements resource.Scheduled.
func (m *Module) Jobs() []scheduler.Job { return nil }

// health is the health of target key on listen port of load balancer
// lbID: "healthy", "unhealthy" or "unknown".
func (m *Module) health(int64, string, int) string { return "unknown" } //nolint:unused // used by viewTargets (plan 08-16)
