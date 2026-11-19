package loadbalancers

import (
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/store"
)

// networkRoutes registers attach_to_network, detach_from_network and
// enable/disable_public_interface.
func (m *Module) networkRoutes(resource.Registrar) {}

// attachAtCreate attaches the new load balancer r to network (0: none).
func (m *Module) attachAtCreate(_ store.Tx, _ *Record, network int64) error {
	if network != 0 {
		return &api.FieldError{Field: "network", Message: "networks are not supported"}
	}
	return nil
}

// InNetwork returns the IDs and IPs of the load balancers attached to
// network netID.
func (m *Module) InNetwork(store.Tx, int64, int64) ([]networks.LBMember, error) { return nil, nil }

// NetworkDeleted detaches every load balancer from network netID.
func (m *Module) NetworkDeleted(store.Tx, int64) error { return nil }
