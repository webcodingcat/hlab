package primaryips

import (
	"errors"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// AssigneeLoadBalancer is the assignee_type of a primary IP held by a load
// balancer.
const AssigneeLoadBalancer = "load_balancer"

// heldByLB reports whether r is assigned to a load balancer.
func heldByLB(r Record) bool {
	return r.AssigneeID != nil && r.AssigneeType == AssigneeLoadBalancer
}

// heldByServer reports whether r is assigned to server serverID.
func heldByServer(r Record, serverID int64) bool {
	return r.AssigneeID != nil && *r.AssigneeID == serverID && !heldByLB(r)
}

// AssignLoadBalancer assigns the given primary IPs (nil: none) to load
// balancer lbID of location. Each must be in the project, of its family,
// unassigned, not blocked and in location. It returns the IPv4 and the
// IPv6 /64; an IP not given leaves its zero value.
func (m *Module) AssignLoadBalancer(tx store.Tx, projectID, lbID int64, location string, ipv4, ipv6 *int64) (netip.Addr, netip.Prefix, error) {
	var addr netip.Addr
	var net6 netip.Prefix
	if loc, ok := m.location(location); ok {
		location = loc.Name
	}
	for _, fam := range []struct {
		typ   string
		id    *int64
		field string
	}{
		{typeIPv4, ipv4, "public_net.ipv4"},
		{typeIPv6, ipv6, "public_net.ipv6"},
	} {
		if fam.id == nil {
			continue
		}
		r, err := Load(tx, projectID, *fam.id)
		if errors.Is(err, store.ErrNotFound) {
			return addr, net6, &api.FieldError{Field: fam.field, Message: "primary IP not found"}
		}
		if err != nil {
			return addr, net6, err
		}
		switch {
		case r.Type != fam.typ:
			return addr, net6, &api.FieldError{Code: "primary_ip_version_mismatch", Message: "the primary IP is not of type " + fam.typ}
		case r.AssigneeID != nil:
			return addr, net6, &api.FieldError{Code: "primary_ip_assigned", Message: "the primary IP is already assigned"}
		case r.Blocked:
			return addr, net6, &api.FieldError{Code: "primary_ip_blocked", Message: "the primary IP is blocked"}
		case r.Hlab.Location != location:
			return addr, net6, &api.FieldError{Code: "primary_ip_location_mismatch", Message: "the primary IP is in another location"}
		}
		id := lbID
		r.AssigneeID = &id
		r.AssigneeType = AssigneeLoadBalancer
		if err := put(tx, r); err != nil {
			return addr, net6, err
		}
		if r.Type == typeIPv4 {
			addr, _ = netip.ParseAddr(r.IP)
		} else {
			net6, _ = netip.ParsePrefix(r.IP)
		}
	}
	return addr, net6, nil
}

// LoadBalancerDeleted unassigns the primary IPs held by load balancer
// lbID, or deletes those with auto_delete.
func (m *Module) LoadBalancerDeleted(tx store.Tx, lbID int64) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if !heldByLB(r) || *r.AssigneeID != lbID {
			continue
		}
		if r.AutoDelete {
			if err := remove(tx, r); err != nil {
				return err
			}
			continue
		}
		r.AssigneeID = nil
		r.AssigneeType = "server"
		if err := put(tx, r); err != nil {
			return err
		}
	}
	return nil
}
