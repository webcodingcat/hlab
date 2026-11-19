package networks

import (
	"errors"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/store"
)

// LoadBalancerMembers is the hook the cloud sets to the loadbalancers
// module; the membership of LBs lives on the LB records.
type LoadBalancerMembers interface {
	// InNetwork returns the IDs and IPs of the LBs attached to network netID.
	InNetwork(tx store.Tx, projectID, netID int64) ([]LBMember, error)
	// NetworkDeleted detaches every LB from netID.
	NetworkDeleted(tx store.Tx, netID int64) error
}

// LBMember is a load balancer attached to a network with its IP there.
type LBMember struct {
	ID int64
	IP netip.Addr
}

// SetLoadBalancers sets the loadbalancers hook; without it a network has
// no LB members.
func (m *Module) SetLoadBalancers(h LoadBalancerMembers) { m.lbs = h }

// lbMembers returns the LBs of projectID attached to network netID.
func (m *Module) lbMembers(tx store.Tx, projectID, netID int64) ([]LBMember, error) {
	if m.lbs == nil {
		return nil, nil
	}
	return m.lbs.InNetwork(tx, projectID, netID)
}

// lbIDs returns the IDs of the LBs of projectID attached to network netID.
func (m *Module) lbIDs(tx store.Tx, projectID, netID int64) ([]int64, error) {
	mbs, err := m.lbMembers(tx, projectID, netID)
	var ids []int64
	for _, mb := range mbs {
		ids = append(ids, mb.ID)
	}
	return ids, err
}

// AttachLoadBalancer checks network netID (same project, a subnet in
// network zone zone) and returns ip (given, in a subnet, not used by a
// server, LB or alias) or the next free IP of ipRange or of the first
// subnet that has one, together with the network's VLAN. Errors: 422
// ip_not_available, no_subnet_available, invalid_input.
func (m *Module) AttachLoadBalancer(tx store.Tx, projectID int64, zone string, netID int64, ip, ipRange string) (netip.Addr, int, error) {
	n, err := Load(tx, projectID, netID)
	if errors.Is(err, store.ErrNotFound) {
		return netip.Addr{}, 0, invalid("network", "network %d not found", netID)
	}
	if err != nil {
		return netip.Addr{}, 0, err
	}
	a, _, err := m.pickIP(tx, projectID, n, zone, ip, ipRange)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	return a, n.Hlab.VLAN, nil
}
