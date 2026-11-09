package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// networkActions is the networks' action table. Hetzner takes a second
// action on a network while one runs and runs it after (measured
// 2026-11-08: two add_route and a delete_route sent at once), so the
// table is queued; change_protection runs alongside.
func networkActions(l *lab.Lab) lab.ActionTable {
	return lab.ActionTable{
		ResourceType: networkResource,
		Busy:         networkBusy,
		Queued:       true,
		Actions: map[string]lab.ActionSpec{
			"add_subnet":        {Name: "add_subnet", Plan: planAddSubnet(l)},
			"delete_subnet":     {Name: "delete_subnet", Plan: planDeleteSubnet(l)},
			"add_route":         {Name: "add_route", Plan: planAddRoute(l)},
			"delete_route":      {Name: "delete_route", Plan: planDeleteRoute(l)},
			"change_ip_range":   {Name: "change_ip_range", Plan: planChangeIPRange(l)},
			"change_protection": {Name: "change_protection", Plan: planNetworkProtection, Unflagged: true},
		},
	}
}

// undoNetwork returns an undo that makes change to the network, if it is
// still there.
func undoNetwork(id int64, change func(tx *state.Tx, network *state.Network)) func(tx *state.Tx) error {
	return func(tx *state.Tx) error {
		network, err := state.Get(tx, state.Networks, id)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		change(tx, network)
		return nil
	}
}

// networkPlan returns the plan of a change to a network's subnets, range or
// routes: the appliance gets the pictures, and Hetzner's action ends at
// once (measured 2026-11-08, also with servers in the network). The undo
// runs only if nothing was queued behind: a later plan built on this
// change (a delete_subnet and then an add_subnet of the same range), and
// taking it back would leave the network with the range twice.
func networkPlan(l *lab.Lab, id int64, undo func(tx *state.Tx, network *state.Network)) lab.Plan {
	return lab.Plan{Steps: l.Machine().ChangeNetwork, Undo: undoNetwork(id, undo), UndoIfLast: true}
}

// planAddSubnet adds a subnet, checked as in a create but with the
// action's own text for an overlap; without an ip_range it takes the first
// free /24, or a smaller block (freeSubnet).
func planAddSubnet(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body subnetRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		checkSubnetFields(&check, "", body, false)
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		subnet, err := newSubnet(network, body, networkOverlapAction)
		if err != nil {
			return lab.Plan{}, err
		}
		if len(network.Subnets) >= networkSubnetsMax {
			return lab.Plan{}, web.RefusalNull(http.StatusForbidden, networkLimitResourceErr, networkSubnetLimit)
		}
		network.Subnets = append(network.Subnets, subnet)
		sortSubnets(network)
		subnetRange := netip.MustParsePrefix(subnet.IPRange)
		return networkPlan(l, network.ID, func(tx *state.Tx, network *state.Network) {
			// a server's attach runs in the server's queue, not the
			// network's, and may have taken an address in the new subnet
			for address := range takenAddresses(tx, network, 0) {
				if subnetRange.Contains(address) {
					return
				}
			}
			network.Subnets = slices.DeleteFunc(network.Subnets, func(other state.NetworkSubnet) bool { return other == subnet })
		}), nil
	}
}

// deleteSubnetRequest is the body of delete_subnet.
type deleteSubnetRequest struct {
	IPRange *string `json:"ip_range"`
}

// planDeleteSubnet removes a subnet in which no server has its address.
func planDeleteSubnet(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body deleteSubnetRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		var subnetRange netip.Prefix
		if body.IPRange == nil {
			check.Fail("ip_range", "'ip_range' is required")
		} else if prefix, err := netip.ParsePrefix(*body.IPRange); err != nil || !prefix.Addr().Is4() {
			check.Fail("ip_range", networkCIDRInvalid)
		} else {
			subnetRange = prefix
		}
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		index := slices.IndexFunc(network.Subnets, func(subnet state.NetworkSubnet) bool {
			return subnet.IPRange == subnetRange.String()
		})
		if index < 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", networkSubnetNotFound)
		}
		// only the members' own addresses count; whether Hetzner also refuses
		// a subnet that holds a member's alias IP is not measured
		for _, member := range networkMembers(tx, network) {
			if subnetRange.Contains(member.address) {
				return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", networkHasMembers)
			}
		}
		subnet := network.Subnets[index]
		network.Subnets = slices.Delete(network.Subnets, index, index+1)
		return networkPlan(l, network.ID, func(_ *state.Tx, network *state.Network) {
			network.Subnets = append(network.Subnets, subnet)
			sortSubnets(network)
		}), nil
	}
}

// planAddRoute adds a route, checked as in a create but with the action's
// own text for an overlap.
func planAddRoute(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body routeRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		checkRouteFields(&check, "", body)
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		route, err := newRoute(network, body, networkOverlapAction)
		if err != nil {
			return lab.Plan{}, err
		}
		if len(network.Routes) >= networkRoutesMax {
			return lab.Plan{}, web.RefusalNull(http.StatusForbidden, networkLimitResourceErr, networkRouteLimit)
		}
		network.Routes = append(network.Routes, route)
		return networkPlan(l, network.ID, func(_ *state.Tx, network *state.Network) {
			network.Routes = slices.DeleteFunc(network.Routes, func(other state.NetworkRoute) bool { return other == route })
		}), nil
	}
}

// planDeleteRoute removes the route with the destination and the gateway
// given.
func planDeleteRoute(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body routeRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		checkRouteFields(&check, "", body)
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		// checkRouteFields has parsed both already
		destination := netip.MustParsePrefix(*body.Destination).String()
		gateway := netip.MustParseAddr(*body.Gateway).String()
		index := slices.IndexFunc(network.Routes, func(route state.NetworkRoute) bool {
			return route.Destination == destination && route.Gateway == gateway
		})
		if index < 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", networkRouteNotFound)
		}
		route := network.Routes[index]
		network.Routes = slices.Delete(network.Routes, index, index+1)
		return networkPlan(l, network.ID, func(_ *state.Tx, network *state.Network) {
			network.Routes = append(network.Routes, route)
		}), nil
	}
}

// changeIPRangeRequest is the body of change_ip_range.
type changeIPRangeRequest struct {
	IPRange *string `json:"ip_range"`
}

// planChangeIPRange extends a network without members: the same first
// address with a shorter prefix (measured 2026-11-08: the same range again
// is refused too).
func planChangeIPRange(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body changeIPRangeRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		var ipRange netip.Prefix
		if body.IPRange == nil {
			check.Fail("ip_range", "'ip_range' is required")
		} else if prefix, err := netip.ParsePrefix(*body.IPRange); err != nil {
			check.Fail("ip_range", networkPrefixInvalid)
		} else {
			ipRange = prefix
		}
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		current := netip.MustParsePrefix(network.IPRange)
		if !validNetworkRange(ipRange) || ipRange.Addr() != current.Addr() || ipRange.Bits() >= current.Bits() {
			return lab.Plan{}, web.InvalidInput(networkRangeInvalid)
		}
		if len(networkMembers(tx, network)) > 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", networkHasMembers)
		}
		before := network.IPRange
		network.IPRange = ipRange.String()
		return networkPlan(l, network.ID, func(_ *state.Tx, network *state.Network) {
			if network.IPRange == ipRange.String() {
				network.IPRange = before
			}
		}), nil
	}
}

// networkProtectionRequest is the body of change_protection.
type networkProtectionRequest struct {
	Delete *bool `json:"delete"`
}

// planNetworkProtection sets the delete protection at once.
func planNetworkProtection(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body networkProtectionRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	network, err := callerNetwork(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	if body.Delete != nil {
		network.Protection.Delete = *body.Delete
	}
	return lab.Plan{}, nil
}
