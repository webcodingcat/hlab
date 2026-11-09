package cloud

import (
	"cmp"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about networks (measured 2026-11-08). Their errors carry
// the details null, unless they name fields.
const (
	networkResource         = "network"
	networkNotFound         = "entity not found"
	networkNameTaken        = "name is already used"
	networkProtected        = "network is delete protected"
	networkRangeInvalid     = "invalid ip range"
	networkPrefixInvalid    = "must be a valid IP prefix"
	networkSubnetInvalid    = "invalid subnetwork"
	networkRouteInvalid     = "invalid route"
	networkGatewayInvalid   = "invalid gateway"
	networkOverlap          = "ip ranges of subnetworks or routes overlap"          // a create
	networkOverlapAction    = "ip range overlaps with existing subnetwork or route" // add_subnet, add_route
	networkZonesNotAligned  = "subnetwork zones are not aligned"
	networkZoneUnknown      = "network zone does not exist"
	networkValueInvalid     = "field contains invalid value"
	networkVSwitchMissing   = "Field must not be empty"
	networkVSwitchNotFound  = "vswitch not found"
	networkSubnetLimit      = "subnet limit reached"
	networkRouteLimit       = "route limit reached"
	networkSubnetNotFound   = "subnet not found"
	networkRouteNotFound    = "route not found"
	networkHasMembers       = "network has attached resources"
	networkCIDRInvalid      = "cidrv4 violated" // delete_subnet's ip_range
	networkNameTooLong      = "value required to be smaller"
	networkNameTooShort     = "field value is too short" // a PUT's empty name
	networkTypeNotAllowed   = "value not allowed"        // members' type
	networkLabelKey         = "key contains invalid characters or is malformed"
	networkLabelValue       = "value contains invalid characters or is malformed" // not measured: follows the key's
	networkNameMax          = 128
	networkSubnetsMax       = 50
	networkRoutesMax        = 100
	networkSmallestRange    = 24 // bits
	networkSmallestSubnet   = 30 // bits
	subnetTypeCloud         = "cloud"
	subnetTypeServer        = "server"
	subnetTypeVSwitch       = "vswitch"
	networkMemberServer     = "server"
	networkMemberBalancer   = "load_balancer"
	networkMemberOK         = "ok"
	networkMemberAttaching  = "attaching"
	networkMemberUpdating   = "updating"
	networkMemberDetaching  = "detaching"
	networkLimitResourceErr = "resource_limit_exceeded"
	networkBusy             = "network is locked by another action" // not measured: no other action locks a network
)

// Hetzner's private ranges for networks and routes (RFC 1918), and the
// address its servers use as the public gateway, which no route may name.
var (
	privateRanges = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	publicGateway = netip.MustParseAddr("172.31.1.1")
)

// networkCreateRequest is the body of POST /networks. It is HLab's own
// type, to tell a missing field from an empty one.
type networkCreateRequest struct {
	Name                  *string            `json:"name"`
	IPRange               *string            `json:"ip_range"`
	Labels                *map[string]string `json:"labels"`
	Subnets               []subnetRequest    `json:"subnets"`
	Routes                []routeRequest     `json:"routes"`
	ExposeRoutesToVSwitch *bool              `json:"expose_routes_to_vswitch"`
}

// subnetRequest is a subnet in a create or an add_subnet.
type subnetRequest struct {
	Type        *string `json:"type"`
	IPRange     *string `json:"ip_range"`
	NetworkZone *string `json:"network_zone"`
	VSwitchID   *int64  `json:"vswitch_id"`
}

// routeRequest is a route in a create, an add_route or a delete_route.
type routeRequest struct {
	Destination *string `json:"destination"`
	Gateway     *string `json:"gateway"`
}

// networkUpdateRequest is the body of PUT /networks/{id}; a missing field
// stays as it is.
type networkUpdateRequest struct {
	Name                  *string            `json:"name"`
	Labels                *map[string]string `json:"labels"`
	ExposeRoutesToVSwitch *bool              `json:"expose_routes_to_vswitch"`
}

// networkAnswer is a network as the API shows it. schema.Network leaves
// out a subnet's vswitch_id, which Hetzner sends as null.
type networkAnswer struct {
	ID                    int64                    `json:"id"`
	Name                  string                   `json:"name"`
	Created               time.Time                `json:"created"`
	IPRange               string                   `json:"ip_range"`
	Subnets               []networkSubnetAnswer    `json:"subnets"`
	Routes                []schema.NetworkRoute    `json:"routes"`
	Servers               []int64                  `json:"servers"`
	LoadBalancers         []int64                  `json:"load_balancers"`
	Protection            schema.NetworkProtection `json:"protection"`
	Labels                map[string]string        `json:"labels"`
	ExposeRoutesToVSwitch bool                     `json:"expose_routes_to_vswitch"`
}

// networkSubnetAnswer is a subnet as the API shows it.
type networkSubnetAnswer struct {
	Type        string `json:"type"`
	IPRange     string `json:"ip_range"`
	NetworkZone string `json:"network_zone"`
	Gateway     string `json:"gateway"`
	VSwitchID   *int64 `json:"vswitch_id"`
}

// networkSortKeys are the sort values of GET /networks. Hetzner ignores
// an unknown one (measured 2026-11-08).
var networkSortKeys = web.SortKeys[networkAnswer]{
	"id":      func(a, b networkAnswer) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b networkAnswer) int { return cmp.Compare(a.Name, b.Name) },
	"created": func(a, b networkAnswer) int { return a.Created.Compare(b.Created) },
}

// registerNetworks adds the routes of the networks and their actions.
func registerNetworks(api *web.API, l *lab.Lab) {
	table := networkActions(l)
	api.Handle("GET /networks", func(req *web.Request) (int, any, error) {
		selector, err := web.ParseSelector(req.URL.Query().Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var networks []networkAnswer
		_ = l.View(func(tx *state.Tx) error {
			for _, network := range state.All(tx, state.Networks) {
				if network.ProjectID == req.Caller.ProjectID && selector.Matches(network.Labels) {
					networks = append(networks, networkJSON(tx, network))
				}
			}
			return nil
		})
		networks = byName(req, networks, func(network networkAnswer) string { return network.Name })
		if err := web.Sort(req, networks, networkSortKeys); err != nil {
			slices.SortFunc(networks, networkSortKeys["id"]) // an unknown sort value
		}
		return listJSON(req, "networks", networks)
	})

	api.Handle("POST /networks", func(req *web.Request) (int, any, error) {
		return createNetwork(req, l)
	})

	// an ID that is no number: Hetzner's 405 without a body (measured
	// 2026-11-09)
	api.AnswerInvalidID("GET /networks/{id}", bareMethodNotAllowed("PUT, DELETE"))
	api.Handle("GET /networks/{id}", func(req *web.Request) (int, any, error) {
		var out networkAnswer
		err := l.View(func(tx *state.Tx) error {
			network, err := callerNetwork(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err == nil {
				out = networkJSON(tx, network)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]networkAnswer{"network": out}, nil
	})

	api.Handle("PUT /networks/{id}", func(req *web.Request) (int, any, error) {
		return updateNetwork(req, l)
	})

	api.Handle("DELETE /networks/{id}", func(req *web.Request) (int, any, error) {
		return deleteNetwork(req, l)
	})

	api.Handle("GET /networks/{id}/members", func(req *web.Request) (int, any, error) {
		return listNetworkMembers(req, l)
	})

	for _, command := range []string{"add_subnet", "delete_subnet", "add_route", "delete_route", "change_ip_range", "change_protection"} {
		api.Handle("POST /networks/{id}/actions/"+command, func(req *web.Request) (int, any, error) {
			action, err := act(l, table, command, req)
			if err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, map[string]schema.Action{"action": actionJSON(&action)}, nil
		})
	}
	registerResourceActions(api, l, "networks", networkResource)
}

// createNetwork creates a network with its subnets and routes; Hetzner
// answers no action. It checks the fields first, then the range, the
// subnets and the routes, then the limits and the name (measured
// 2026-11-08).
func createNetwork(req *web.Request, l *lab.Lab) (int, any, error) {
	var body networkCreateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	if body.Name == nil || *body.Name == "" {
		check.Fail("name", "'name' is required")
	} else if len([]rune(*body.Name)) > networkNameMax {
		check.Fail("name", networkNameTooLong)
	}
	var ipRange netip.Prefix
	if body.IPRange == nil {
		check.Fail("ip_range", "'ip_range' is required")
	} else if prefix, err := netip.ParsePrefix(*body.IPRange); err != nil {
		check.Fail("ip_range", networkPrefixInvalid)
	} else {
		ipRange = prefix
	}
	for i, subnet := range body.Subnets {
		checkSubnetFields(&check, "subnets["+strconv.Itoa(i)+"].", subnet, true)
	}
	for i, route := range body.Routes {
		checkRouteFields(&check, "routes["+strconv.Itoa(i)+"].", route)
	}
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, networkLabelKey, networkLabelValue)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	if !validNetworkRange(ipRange) {
		return 0, nil, web.InvalidInput(networkRangeInvalid)
	}
	network := &state.Network{
		ProjectID: req.Caller.ProjectID,
		Name:      *body.Name,
		Labels:    map[string]string{},
		Created:   l.Now(),
		IPRange:   ipRange.String(),
		Subnets:   []state.NetworkSubnet{},
		Routes:    []state.NetworkRoute{},
	}
	if body.Labels != nil {
		network.Labels = *body.Labels
	}
	if body.ExposeRoutesToVSwitch != nil {
		network.ExposeRoutesToVSwitch = *body.ExposeRoutesToVSwitch
	}
	for _, request := range body.Subnets {
		subnet, err := newSubnet(network, request, networkOverlap)
		if err != nil {
			return 0, nil, err
		}
		network.Subnets = append(network.Subnets, subnet)
	}
	for _, request := range body.Routes {
		route, err := newRoute(network, request, networkOverlap)
		if err != nil {
			return 0, nil, err
		}
		network.Routes = append(network.Routes, route)
	}
	if len(network.Subnets) > networkSubnetsMax {
		return 0, nil, web.RefusalNull(http.StatusForbidden, networkLimitResourceErr, networkSubnetLimit)
	}
	if len(network.Routes) > networkRoutesMax {
		return 0, nil, web.RefusalNull(http.StatusForbidden, networkLimitResourceErr, networkRouteLimit)
	}
	sortSubnets(network)
	var out networkAnswer
	err := l.Change(func(tx *state.Tx) error {
		if err := lab.CheckProject(tx, network.ProjectID); err != nil {
			return err
		}
		if _, taken := state.ByName(tx, state.Networks, network.ProjectID, network.Name); taken {
			return web.RefusalNull(http.StatusConflict, "uniqueness_error", networkNameTaken)
		}
		vlan, err := lab.NewVLAN(tx)
		if err != nil {
			return web.Refusal(http.StatusPreconditionFailed, "resource_unavailable", err.Error())
		}
		network.ID, network.VLAN = state.NewID(tx, state.Networks), vlan
		state.Put(tx, state.Networks, network)
		out = networkJSON(tx, network)
		return nil
	})
	if errors.Is(err, lab.ErrProjectGone) {
		return 0, nil, web.TokenInvalid()
	}
	if err != nil {
		return 0, nil, err
	}
	// the appliance gets the network's VLAN and VRF; no server is in it yet,
	// so the answer need not wait
	l.Machine().UpdatePicturesLater()
	return http.StatusCreated, map[string]networkAnswer{"network": out}, nil
}

// updateNetwork changes a network's name, labels and
// expose_routes_to_vswitch, which the lab only keeps: it has no vSwitch.
func updateNetwork(req *web.Request, l *lab.Lab) (int, any, error) {
	var body networkUpdateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Update: true}
	if body.Name != nil && *body.Name == "" {
		check.Fail("name", networkNameTooShort)
	} else if body.Name != nil && len([]rune(*body.Name)) > networkNameMax {
		check.Fail("name", networkNameTooLong)
	}
	if body.Labels != nil {
		check.LabelsWith(*body.Labels, networkLabelKey, networkLabelValue)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	var out networkAnswer
	err := l.Change(func(tx *state.Tx) error {
		network, err := callerNetwork(tx, req.Caller.ProjectID, web.PathID(req, "id"))
		if err != nil {
			return err
		}
		if body.Name != nil && *body.Name != network.Name {
			if _, taken := state.ByName(tx, state.Networks, network.ProjectID, *body.Name); taken {
				return web.RefusalNull(http.StatusConflict, "uniqueness_error", networkNameTaken)
			}
			network.Name = *body.Name
		}
		if body.Labels != nil {
			network.Labels = *body.Labels
		}
		if body.ExposeRoutesToVSwitch != nil {
			network.ExposeRoutesToVSwitch = *body.ExposeRoutesToVSwitch
		}
		out = networkJSON(tx, network)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, map[string]networkAnswer{"network": out}, nil
}

// callerNetwork returns the network of an ID if it belongs to the project,
// with Hetzner's 404 otherwise.
func callerNetwork(tx *state.Tx, projectID, id int64) (*state.Network, error) {
	network, err := state.Get(tx, state.Networks, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && network.ProjectID != projectID) {
		return nil, web.NotFoundNull(networkNotFound)
	}
	return network, err
}

// networkJSON returns a network as the API shows it, with the servers in
// it. Each subnet's gateway is the network's first address, as on Hetzner.
func networkJSON(tx *state.Tx, network *state.Network) networkAnswer {
	out := networkAnswer{
		ID:                    network.ID,
		Name:                  network.Name,
		Created:               network.Created,
		IPRange:               network.IPRange,
		Subnets:               []networkSubnetAnswer{},
		Routes:                []schema.NetworkRoute{},
		Servers:               networkServers(tx, network.ID),
		LoadBalancers:         []int64{},
		Protection:            schema.NetworkProtection{Delete: network.Protection.Delete},
		Labels:                network.Labels,
		ExposeRoutesToVSwitch: network.ExposeRoutesToVSwitch,
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	gateway := netip.MustParsePrefix(network.IPRange).Addr().Next().String()
	for _, subnet := range network.Subnets {
		out.Subnets = append(out.Subnets, networkSubnetAnswer{
			Type: subnet.Type, IPRange: subnet.IPRange, NetworkZone: subnet.NetworkZone, Gateway: gateway,
		})
	}
	for _, route := range network.Routes {
		out.Routes = append(out.Routes, schema.NetworkRoute{Destination: route.Destination, Gateway: route.Gateway})
	}
	return out
}

// networkServers returns the IDs of the servers in a network, [] for none.
func networkServers(tx *state.Tx, networkID int64) []int64 {
	return append([]int64{}, state.IDs(tx, state.Servers, func(server *state.Server) bool {
		return slices.ContainsFunc(server.PrivateNets, func(attached state.ServerPrivateNet) bool {
			return attached.NetworkID == networkID
		})
	})...)
}

// networkMember is a server in a network, as GET /networks/{id}/members
// and the network's checks need it.
type networkMember struct {
	serverID int64
	address  netip.Addr
	aliases  []string
}

// networkMembers returns the servers in a network, by ID.
func networkMembers(tx *state.Tx, network *state.Network) []networkMember {
	var members []networkMember
	for _, server := range state.All(tx, state.Servers) {
		for _, attached := range server.PrivateNets {
			if attached.NetworkID != network.ID {
				continue
			}
			address, _ := netip.ParseAddr(attached.IP)
			members = append(members, networkMember{serverID: server.ID, address: address, aliases: attached.AliasIPs})
		}
	}
	return members
}

// networkMemberStatuses and networkMemberTypes are the values the members'
// filters take; Hetzner refuses others (measured 2026-11-08).
var (
	networkMemberStatuses = []string{networkMemberOK, networkMemberAttaching, networkMemberDetaching, networkMemberUpdating, "error"}
	networkMemberTypes    = []string{networkMemberServer, networkMemberBalancer}
)

// networkMemberSortKeys are the sort values of the members; Hetzner
// ignores an unknown one (measured 2026-11-08).
var networkMemberSortKeys = web.SortKeys[schema.NetworkMember]{
	"id":     func(a, b schema.NetworkMember) int { return cmp.Compare(a.ID, b.ID) },
	"type":   func(a, b schema.NetworkMember) int { return cmp.Compare(a.Type, b.Type) },
	"status": func(a, b schema.NetworkMember) int { return cmp.Compare(a.Status, b.Status) },
	"ip": func(a, b schema.NetworkMember) int {
		return netip.MustParseAddr(a.IP).Compare(netip.MustParseAddr(b.IP))
	},
}

// listNetworkMembers answers the servers in a network, each with the
// subnet its address is in, filtered by type, status and subnet; the lab has no load balancers yet.
func listNetworkMembers(req *web.Request, l *lab.Lab) (int, any, error) {
	query := req.URL.Query()
	check := web.Check{Singular: true}
	for _, value := range query["type"] {
		if !slices.Contains(networkMemberTypes, value) {
			check.Fail("type", networkTypeNotAllowed)
		}
	}
	for _, value := range query["status"] {
		if !slices.Contains(networkMemberStatuses, value) {
			check.Fail("status", networkTypeNotAllowed)
		}
	}
	var subnets []netip.Prefix
	for i, value := range query["subnet"] {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			check.Fail("filters.subnets["+strconv.Itoa(i)+"]", networkPrefixInvalid)
		}
		subnets = append(subnets, prefix.Masked())
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	var members []schema.NetworkMember
	err := l.View(func(tx *state.Tx) error {
		network, err := callerNetwork(tx, req.Caller.ProjectID, web.PathID(req, "id"))
		if err != nil {
			return err
		}
		var all []schema.NetworkMember
		statuses := memberStatuses(tx, network.ID)
		for _, member := range networkMembers(tx, network) {
			status := statuses[member.serverID]
			if status == "" {
				status = networkMemberOK
			}
			all = append(all, schema.NetworkMember{
				Type:     networkMemberServer,
				ID:       member.serverID,
				IP:       member.address.String(),
				Status:   status,
				AliasIPs: append([]string{}, member.aliases...),
				Subnet:   memberSubnet(network, member.address),
			})
		}
		for _, member := range all {
			if matchesMembersQuery(query, subnets, member) {
				members = append(members, member)
			}
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	if err := web.Sort(req, members, networkMemberSortKeys); err != nil {
		slices.SortFunc(members, networkMemberSortKeys["id"]) // an unknown sort value
	}
	return listJSON(req, "members", members)
}

// matchesMembersQuery tells whether a member passes the filters: each
// filter given must name the member's value.
func matchesMembersQuery(query map[string][]string, subnets []netip.Prefix, member schema.NetworkMember) bool {
	if types, given := query["type"]; given && !slices.Contains(types, member.Type) {
		return false
	}
	if statuses, given := query["status"]; given && !slices.Contains(statuses, member.Status) {
		return false
	}
	return len(subnets) == 0 || slices.Contains(subnets, netip.MustParsePrefix(member.Subnet))
}

// memberSubnet returns the subnet that holds an address of the network.
func memberSubnet(network *state.Network, address netip.Addr) string {
	for _, subnet := range network.Subnets {
		if netip.MustParsePrefix(subnet.IPRange).Contains(address) {
			return subnet.IPRange
		}
	}
	return network.IPRange
}

// memberStatuses returns the statuses of a network's members that are not
// "ok": "attaching" while a server's attach runs, "updating" while its
// alias IPs change (not measured). It looks through the actions once for
// all members.
func memberStatuses(tx *state.Tx, networkID int64) map[int64]string {
	statuses := map[int64]string{}
	running := state.IDs(tx, state.Actions, func(action *state.Action) bool {
		return action.Status == state.ActionRunning && namesResource(action, networkResource, networkID)
	})
	for _, id := range running {
		action, err := state.Get(tx, state.Actions, id)
		if err != nil {
			continue
		}
		for _, resource := range action.Resources {
			if resource.Type != lab.ServerResource {
				continue
			}
			switch action.Command {
			case "attach_to_network":
				statuses[resource.ID] = networkMemberAttaching
			case "change_alias_ips":
				if statuses[resource.ID] == "" {
					statuses[resource.ID] = networkMemberUpdating
				}
			}
		}
	}
	return statuses
}

// deleteNetwork deletes a network, also one that servers are in: as on
// Hetzner, they leave it at once, without an action of theirs (measured
// 2026-11-08), and their NICs leave the VMs in the background.
func deleteNetwork(req *web.Request, l *lab.Lab) (int, any, error) {
	id := web.PathID(req, "id")
	var nics []lab.ServerNIC
	var members []state.ResourceRef
	// an action that runs on the network ends "aborted", also a server's
	// attach (measured 2026-11-09)
	aborted, err := l.ChangeAborting(state.ResourceRef{Type: networkResource, ID: id}, func(tx *state.Tx) error {
		network, err := callerNetwork(tx, req.Caller.ProjectID, id)
		if err != nil {
			return err
		}
		if network.Protection.Delete {
			return web.RefusalNull(http.StatusLocked, "protected", networkProtected)
		}
		for _, member := range networkMembers(tx, network) {
			server, err := state.Get(tx, state.Servers, member.serverID)
			if err != nil {
				return err
			}
			attached, index := serverNetwork(server, id)
			server.PrivateNets = slices.Delete(server.PrivateNets, index, index+1)
			nics = append(nics, lab.ServerNIC{ServerID: server.ID, MAC: attached.MAC})
			members = append(members, state.ResourceRef{Type: lab.ServerResource, ID: server.ID})
		}
		state.Delete(tx, state.Networks, id)
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	// a member's running action, such as its create, may still put the
	// NIC into the VM: the NICs leave after it
	l.Machine().DetachNICsLater(nics, append(aborted, l.RunningOn(members)...))
	return http.StatusNoContent, nil, nil
}
