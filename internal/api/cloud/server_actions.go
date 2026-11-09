package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strconv"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// serverActions is the servers' action table. Hetzner runs a server's
// actions one after another, so the table is queued: a power action
// during another waits instead of 423 (measured 2026-11-08). "create" and
// "delete" are no routes of their own: POST and DELETE /servers run them.
func serverActions(l *lab.Lab) lab.ActionTable {
	machine := l.Machine()
	// want_running changes only in the plans (planStop, planStart; the
	// create records it), when an action is asked for: queued actions then
	// leave it in the order they were sent
	return lab.ActionTable{
		ResourceType: lab.ServerResource,
		Busy:         serverBusy,
		Queued:       true,
		Actions: map[string]lab.ActionSpec{
			"create":            {Name: "create_server", Plan: planCreateServer(l)},
			"delete":            {Name: "delete_server", Plan: planDeleteServer(l)},
			"poweron":           {Name: "start_server", Plan: planStart(l, machine.PowerOnServer)},
			"poweroff":          {Name: "stop_server", Plan: planStop(machine.PowerOffServer)},
			"shutdown":          {Name: "shutdown_server", Plan: planSteps(machine.ShutdownServer)},
			"reboot":            {Name: "reboot_server", Plan: planStart(l, machine.RebootServer)},
			"reset":             {Name: "reset_server", Plan: planStart(l, machine.ResetServer)},
			"change_protection": {Name: "change_protection", Plan: planServerProtection, Unflagged: true},
			"attach_to_network": {Name: "attach_to_network", Plan: planAttachToNetwork(l)},
			// a create's network: it runs beside the create, not behind it
			"attach_at_create":    {Name: "attach_to_network", Plan: planAttachAtCreate(l), Unflagged: true},
			"detach_from_network": {Name: "detach_from_network", Plan: planDetachFromNetwork(l)},
			"change_alias_ips":    {Name: "change_alias_ips", Plan: planChangeAliasIPs(l)},
		},
	}
}

// planSteps returns the plan of a power action without quick changes: it
// checks that the caller has the server, and its steps call run with it.
func planSteps(run func(ctx context.Context, id int64) error) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		if _, err := callerServer(tx, req.ProjectID, req.ID); err != nil {
			return lab.Plan{}, err
		}
		return lab.Plan{Steps: func(ctx context.Context) error { return run(ctx, req.ID) }}, nil
	}
}

// planStop returns the plan of poweroff. It clears want_running at once,
// when the poweroff is asked for, not when its steps run. A start queued
// behind the poweroff then sees a server that does not want to run, so it
// checks the room and sets want_running again (planStart); the poweroff's
// steps leave it so. If the steps fail, want_running is as before.
func planStop(run func(ctx context.Context, id int64) error) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		wanted := server.WantRunning
		server.WantRunning = false
		return lab.Plan{
			Steps:      func(ctx context.Context) error { return run(ctx, req.ID) },
			Undo:       undoServer(req.ID, func(_ *state.Tx, server *state.Server) { server.WantRunning = wanted }),
			UndoIfLast: true, // a later plan's want_running stands
		}, nil
	}
}

// planStart returns the plan of an action that starts the server:
// poweron, and reboot and reset, which start a server that is off, as on
// Hetzner. If the server does not want to run yet, the plan checks the
// room on the host and sets want_running at once, so that the room check
// of every later action counts the server even before these steps run. If
// the steps fail, the server is off and does not want to run.
func planStart(l *lab.Lab, run func(ctx context.Context, id int64) error) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		plan := lab.Plan{Steps: func(ctx context.Context) error { return run(ctx, req.ID) }}
		if server.WantRunning {
			return plan, nil
		}
		// a type that the user's catalog dropped counts nothing, as in CheckRoom
		serverType, _ := l.Catalog().ServerTypeByName(server.Type)
		if err := l.Machine().CheckRoom(tx, server.ID, serverType); err != nil {
			return lab.Plan{}, err
		}
		server.WantRunning = true
		plan.Undo = undoServer(req.ID, func(_ *state.Tx, server *state.Server) {
			server.Status, server.WantRunning = state.ServerOff, false
		})
		plan.UndoIfLast = true // a later plan's want_running stands
		return plan, nil
	}
}

// planCreateServer builds the new server's VM; the action names the image
// too. If the steps fail, the server is deleted with its VM, its files and
// its new Primary IPs.
func planCreateServer(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		start := server.WantRunning
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: imageResource, ID: server.ImageID}},
			Steps:     func(ctx context.Context) error { return l.Machine().CreateServer(ctx, req.ID, start) },
			Undo:      undoServer(req.ID, releaseServer),
		}, nil
	}
}

// planDeleteServer removes the server's record at once (a create still
// running then fails and cleans up, Machine.CreateServer), as Hetzner's API
// does, and its VM, disk and seed in the steps. Its Primary IPs with
// auto_delete go with it, the others stay unassigned.
func planDeleteServer(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		if server.Protection.Delete {
			return lab.Plan{}, web.Refusal(http.StatusLocked, "protected", serverDeleteProtected)
		}
		// it leaves its networks as a detach does
		for _, attached := range server.PrivateNets {
			if network, err := state.Get(tx, state.Networks, attached.NetworkID); err == nil {
				noteMemberChange(l, tx, network, server.ID, leavingMember(network, server.ID, attached))
			}
		}
		releaseServer(tx, server)
		return lab.Plan{Steps: func(ctx context.Context) error { return l.Machine().DeleteServer(ctx, req.ID) }}, nil
	}
}

// releaseServer deletes a server's record and its Primary IPs with
// auto_delete; the others stay, unassigned, and so do its Floating IPs
// and its firewalls. Its NICs in private networks go with the record; the steps remove the
// VM with them.
func releaseServer(tx *state.Tx, server *state.Server) {
	unassignFloatingIPs(tx, server.ID)
	releaseFirewalls(tx, server.ID)
	for _, id := range []int64{server.IPv4, server.IPv6} {
		ip, err := state.Get(tx, state.PrimaryIPs, id)
		if err != nil {
			continue
		}
		if ip.AutoDelete {
			state.Delete(tx, state.PrimaryIPs, id)
		} else {
			ip.AssigneeID = 0
		}
	}
	state.Delete(tx, state.Servers, server.ID)
}

// serverProtectionRequest is the body of change_protection; a missing
// field stays as it is.
type serverProtectionRequest struct {
	Delete  *bool `json:"delete"`
	Rebuild *bool `json:"rebuild"`
}

// planServerProtection sets the server's protection at once.
func planServerProtection(tx *state.Tx, req lab.Request) (lab.Plan, error) {
	var body serverProtectionRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return lab.Plan{}, web.ParseFailed()
	}
	server, err := callerServer(tx, req.ProjectID, req.ID)
	if err != nil {
		return lab.Plan{}, err
	}
	if body.Delete != nil {
		server.Protection.Delete = *body.Delete
	}
	if body.Rebuild != nil {
		server.Protection.Rebuild = *body.Rebuild
	}
	return lab.Plan{}, nil
}

// undoServer returns an undo that makes change to the server, if it is still
// there: a reset of its project may have removed it meanwhile.
func undoServer(id int64, change func(tx *state.Tx, server *state.Server)) func(tx *state.Tx) error {
	return func(tx *state.Tx) error {
		server, err := state.Get(tx, state.Servers, id)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		change(tx, server)
		return nil
	}
}

// Hetzner's texts about a server's networks (measured 2026-11-08). Their
// errors carry the details null.
const (
	serverAlreadyAttached = "server already attached"
	serverNetworksOverlap = "network IP range overlaps with one of the resource networks"
	serverNetworksLimit   = "networks per resource limit reached"
	serverNoSubnet        = "no subnet or IP available"
	serverIPNotAvailable  = "IP not available"
	serverNotInNetwork    = "resource not found"
	serverAliasLimit      = "alias ips limit reached"
	serverIPInvalid       = "must be a valid IP address"
	serverSubnetNotFound  = "subnet not found"
	serverIPNotInRange    = "ip not in ip range"
	// Hetzner's limits: 3 networks per server, 5 alias IPs per network.
	serverNetworksMax = 3
	serverAliasIPsMax = 5
)

// attachToNetworkRequest is the body of attach_to_network.
type attachToNetworkRequest struct {
	Network  *int64   `json:"network"`
	IP       *string  `json:"ip"`
	IPRange  *string  `json:"ip_range"`
	AliasIPs []string `json:"alias_ips"`
}

// changeAliasIPsRequest is the body of change_alias_ips; detach_from_network
// reads only its network.
type changeAliasIPsRequest struct {
	Network  *int64    `json:"network"`
	AliasIPs *[]string `json:"alias_ips"`
}

// parseAliasIPs notes the alias IPs that are no IPv4 addresses and returns
// the others.
func parseAliasIPs(check *web.Check, aliases []string) []netip.Addr {
	var addresses []netip.Addr
	for i, alias := range aliases {
		address, err := netip.ParseAddr(alias)
		if err != nil || !address.Is4() {
			check.Fail("alias_ips["+strconv.Itoa(i)+"]", serverIPInvalid)
			continue
		}
		addresses = append(addresses, address)
	}
	return addresses
}

// planAttachToNetwork gives the server a NIC in a network: the address
// asked for, or the first free one of the subnet named by ip_range or of
// the last subnet with room, with up to five alias IPs. The steps put the
// NIC into the VM, also one that runs. Hetzner checks the fields, the
// network, the alias IPs' count, an attach to the network already, an
// overlap with the server's other networks and its three networks, then
// the addresses; it does not check the network zone (measured
// 2026-11-08).
func planAttachToNetwork(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body attachToNetworkRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		if body.Network == nil {
			check.Fail("network", "'network' is required")
		}
		var ip netip.Addr
		if body.IP != nil {
			if address, err := netip.ParseAddr(*body.IP); err != nil || !address.Is4() {
				check.Fail("ip", serverIPInvalid)
			} else {
				ip = address
			}
		}
		var ipRange netip.Prefix
		if body.IPRange != nil {
			if prefix, err := netip.ParsePrefix(*body.IPRange); err != nil || !prefix.Addr().Is4() {
				check.Fail("ip_range", networkCIDRInvalid)
			} else {
				ipRange = prefix.Masked()
			}
		}
		aliases := parseAliasIPs(&check, body.AliasIPs)
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, *body.Network)
		if err != nil {
			return lab.Plan{}, err
		}
		if len(aliases) > serverAliasIPsMax {
			return lab.Plan{}, web.RefusalNull(http.StatusForbidden, "invalid_input", serverAliasLimit)
		}
		attached, err := joinNetwork(l, tx, server, network, ip, ipRange, aliases)
		if err != nil {
			return lab.Plan{}, err
		}
		serverID, networkID := server.ID, network.ID
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: networkResource, ID: networkID}},
			Steps: func(ctx context.Context) error {
				return l.Machine().AttachServerToNetwork(ctx, serverID, networkID)
			},
			Undo: undoServer(serverID, func(_ *state.Tx, server *state.Server) {
				server.PrivateNets = slices.DeleteFunc(server.PrivateNets, func(nic state.ServerPrivateNet) bool {
					return nic.MAC == attached.MAC
				})
			}),
		}, nil
	}
}

// joinNetwork gives the server a NIC in a network, as an attach does: the
// address asked for, or the first free one of the subnet named by ipRange
// or of the last subnet with room, with its alias IPs. It checks what
// Hetzner checks after the fields, the network and the alias IPs' count,
// in Hetzner's order, and notes the change among the network's members.
func joinNetwork(l *lab.Lab, tx *state.Tx, server *state.Server, network *state.Network,
	ip netip.Addr, ipRange netip.Prefix, aliases []netip.Addr) (state.ServerPrivateNet, error) {
	if err := checkServerNetworks(tx, server, network); err != nil {
		return state.ServerPrivateNet{}, err
	}
	if ipRange.IsValid() && !slices.Contains(subnetPrefixes(network), ipRange) {
		return state.ServerPrivateNet{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", serverSubnetNotFound)
	}
	if ipRange.IsValid() && ip.IsValid() && !ipRange.Contains(ip) {
		return state.ServerPrivateNet{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", serverIPNotInRange)
	}
	taken := takenAddresses(tx, network, server.ID)
	if ip.IsValid() {
		if err := addressRefusal(network, ip, taken); err != nil {
			return state.ServerPrivateNet{}, err
		}
	} else {
		var found bool
		if ip, found = freeAddress(network, ipRange, taken); !found {
			return state.ServerPrivateNet{}, web.RefusalNull(http.StatusUnprocessableEntity, "no_subnet_available", serverNoSubnet)
		}
	}
	if err := aliasRefusal(network, ip, aliases, taken); err != nil {
		return state.ServerPrivateNet{}, err
	}
	noteMemberChange(l, tx, network, server.ID, nil)
	attached := state.ServerPrivateNet{
		NetworkID: network.ID,
		IP:        ip.String(),
		AliasIPs:  addressStrings(aliases),
		MAC:       lab.NewPrivateMAC(tx),
	}
	server.PrivateNets = append(server.PrivateNets, attached)
	return attached, nil
}

// planAttachAtCreate gives a new server a NIC in a network of its create
// request, which createServer has checked: the first free address of the
// last subnet with room. The create makes the VM with the NIC, so the
// steps only wait Hetzner's time; they call l.Sleep themselves, as they
// change nothing on the machine. Hetzner's attach ends a few seconds
// before the create (measured 2026-11-09), as an attach's time does
// before the create's.
func planAttachAtCreate(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body attachToNetworkRequest
		if err := json.Unmarshal(req.Body, &body); err != nil || body.Network == nil {
			return lab.Plan{}, web.ParseFailed()
		}
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, *body.Network)
		if err != nil {
			return lab.Plan{}, err
		}
		if _, err := joinNetwork(l, tx, server, network, netip.Addr{}, netip.Prefix{}, nil); err != nil {
			return lab.Plan{}, err
		}
		l.AddNote(lab.NoteKey{Kind: memberNote, Owner: network.ID, Subject: server.ID},
			lab.Note{Status: networkMemberAttaching}, 0, lab.MemberAttachingAtCreate)
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: networkResource, ID: network.ID}},
			Steps:     func(ctx context.Context) error { return l.Sleep(ctx, lab.NICAttach) },
		}, nil
	}
}

// checkServerNetworks refuses an attach to a network the server is in
// already, to one whose range overlaps one of the server's networks, and a
// fourth network.
func checkServerNetworks(tx *state.Tx, server *state.Server, network *state.Network) error {
	ipRange := netip.MustParsePrefix(network.IPRange)
	for _, attached := range server.PrivateNets {
		if attached.NetworkID == network.ID {
			return web.RefusalNull(http.StatusUnprocessableEntity, "server_already_attached", serverAlreadyAttached)
		}
	}
	for _, attached := range server.PrivateNets {
		other, err := state.Get(tx, state.Networks, attached.NetworkID)
		if err == nil && netip.MustParsePrefix(other.IPRange).Overlaps(ipRange) {
			return web.RefusalNull(http.StatusUnprocessableEntity, "networks_overlap", serverNetworksOverlap)
		}
	}
	if len(server.PrivateNets) >= serverNetworksMax {
		return web.RefusalNull(http.StatusForbidden, "resource_limit_exceeded", serverNetworksLimit)
	}
	return nil
}

// takenAddresses returns the addresses and alias IPs that the servers in a
// network hold, except those of one server (the one that changes).
func takenAddresses(tx *state.Tx, network *state.Network, except int64) map[netip.Addr]bool {
	taken := map[netip.Addr]bool{}
	for _, server := range state.All(tx, state.Servers) {
		if server.ID == except {
			continue
		}
		for _, attached := range server.PrivateNets {
			if attached.NetworkID != network.ID {
				continue
			}
			for _, text := range append([]string{attached.IP}, attached.AliasIPs...) {
				if address, err := netip.ParseAddr(text); err == nil {
					taken[address] = true
				}
			}
		}
	}
	return taken
}

// subnetPrefixes returns the network's subnets that hand out addresses to
// servers (all but a vSwitch's).
func subnetPrefixes(network *state.Network) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, subnet := range network.Subnets {
		if subnet.Type != subnetTypeVSwitch {
			prefixes = append(prefixes, netip.MustParsePrefix(subnet.IPRange))
		}
	}
	return prefixes
}

// addressRefusal returns Hetzner's refusal of an address for a server in a
// network, or nil: an address in no subnet (the network's gateway among
// them) has no subnet; a subnet's own and last address, and one another
// server holds, are not available (measured 2026-11-08).
func addressRefusal(network *state.Network, address netip.Addr, taken map[netip.Addr]bool) error {
	index := slices.IndexFunc(subnetPrefixes(network), func(prefix netip.Prefix) bool { return prefix.Contains(address) })
	if index < 0 {
		return web.RefusalNull(http.StatusUnprocessableEntity, "no_subnet_available", serverNoSubnet)
	}
	prefix := subnetPrefixes(network)[index]
	gateway := netip.MustParsePrefix(network.IPRange).Addr().Next()
	if address == prefix.Addr() || address == lastAddress(prefix) || address == gateway || taken[address] {
		return web.RefusalNull(http.StatusUnprocessableEntity, "ip_not_available", serverIPNotAvailable)
	}
	return nil
}

// freeAddress returns the first free address of the subnet ipRange names,
// or of the last subnet with one: Hetzner gave a server and a load
// balancer the second of two subnets (measured 2026-11-08).
func freeAddress(network *state.Network, ipRange netip.Prefix, taken map[netip.Addr]bool) (netip.Addr, bool) {
	prefixes := subnetPrefixes(network)
	slices.Reverse(prefixes)
	for _, prefix := range prefixes {
		if ipRange.IsValid() && ipRange != prefix {
			continue
		}
		for address := prefix.Addr().Next(); prefix.Contains(address); address = address.Next() {
			if addressRefusal(network, address, taken) == nil {
				return address, true
			}
		}
	}
	return netip.Addr{}, false
}

// lastAddress returns the last address of a prefix.
func lastAddress(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Masked().Addr().As4()
	host := uint32(1)<<(32-prefix.Bits()) - 1
	value := (uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])) | host
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

// aliasRefusal returns Hetzner's refusal of a server's alias IPs in a
// network, or nil: an alias IP twice has no subnet, as Hetzner answers it;
// the server's own address is not available (measured 2026-11-08).
func aliasRefusal(network *state.Network, ip netip.Addr, aliases []netip.Addr, taken map[netip.Addr]bool) error {
	seen := map[netip.Addr]bool{}
	for _, alias := range aliases {
		if seen[alias] {
			return web.RefusalNull(http.StatusUnprocessableEntity, "no_subnet_available", serverNoSubnet)
		}
		seen[alias] = true
		if alias == ip {
			return web.RefusalNull(http.StatusUnprocessableEntity, "ip_not_available", serverIPNotAvailable)
		}
		if err := addressRefusal(network, alias, taken); err != nil {
			return err
		}
	}
	return nil
}

// addressStrings returns addresses as text, an empty list for none.
func addressStrings(addresses []netip.Addr) []string {
	texts := []string{}
	for _, address := range addresses {
		texts = append(texts, address.String())
	}
	return texts
}

// serverNetwork returns the server's NIC in a network, and its index; -1
// if the server is not in it.
func serverNetwork(server *state.Server, networkID int64) (state.ServerPrivateNet, int) {
	index := slices.IndexFunc(server.PrivateNets, func(nic state.ServerPrivateNet) bool { return nic.NetworkID == networkID })
	if index < 0 {
		return state.ServerPrivateNet{}, -1
	}
	return server.PrivateNets[index], index
}

// planDetachFromNetwork takes the server's NIC out of a network: the
// record at once, the routes and then the NIC in the steps. If the steps
// fail, the NIC comes back into the record where it was.
func planDetachFromNetwork(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body changeAliasIPsRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		if body.Network == nil {
			check := web.Check{Singular: true}
			check.Fail("network", "'network' is required")
			return lab.Plan{}, check.Err()
		}
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, *body.Network)
		if err != nil {
			return lab.Plan{}, err
		}
		attached, index := serverNetwork(server, network.ID)
		if index < 0 {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", serverNotInNetwork)
		}
		server.PrivateNets = slices.Delete(server.PrivateNets, index, index+1)
		noteMemberChange(l, tx, network, server.ID, leavingMember(network, server.ID, attached))
		serverID, networkID := server.ID, network.ID
		return lab.Plan{
			Resources: []state.ResourceRef{{Type: networkResource, ID: networkID}},
			Steps: func(ctx context.Context) error {
				return l.Machine().DetachServerFromNetwork(ctx, serverID, attached.MAC)
			},
			Undo: undoServer(serverID, func(tx *state.Tx, server *state.Server) {
				if _, err := state.Get(tx, state.Networks, networkID); err == nil {
					server.PrivateNets = slices.Insert(server.PrivateNets, min(index, len(server.PrivateNets)), attached)
				}
			}),
		}, nil
	}
}

// planChangeAliasIPs replaces the server's alias IPs in a network. An
// empty list for a network the server is not in changes nothing and
// succeeds, as on Hetzner; more than five are 422 here, where an attach
// answers 403 (measured 2026-11-08).
func planChangeAliasIPs(l *lab.Lab) planFunc {
	return func(tx *state.Tx, req lab.Request) (lab.Plan, error) {
		var body changeAliasIPsRequest
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return lab.Plan{}, web.ParseFailed()
		}
		check := web.Check{Singular: true}
		if body.Network == nil {
			check.Fail("network", "'network' is required")
		}
		var aliases []netip.Addr
		if body.AliasIPs == nil {
			check.Fail("alias_ips", "'alias_ips' is required")
		} else {
			aliases = parseAliasIPs(&check, *body.AliasIPs)
		}
		if err := check.Err(); err != nil {
			return lab.Plan{}, err
		}
		server, err := callerServer(tx, req.ProjectID, req.ID)
		if err != nil {
			return lab.Plan{}, err
		}
		network, err := callerNetwork(tx, req.ProjectID, *body.Network)
		if err != nil {
			return lab.Plan{}, err
		}
		if len(aliases) > serverAliasIPsMax {
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "invalid_input", serverAliasLimit)
		}
		plan := lab.Plan{Resources: []state.ResourceRef{{Type: networkResource, ID: network.ID}}}
		attached, index := serverNetwork(server, network.ID)
		if index < 0 {
			if len(aliases) == 0 {
				return plan, nil
			}
			return lab.Plan{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", serverNotInNetwork)
		}
		taken := takenAddresses(tx, network, server.ID)
		if err := aliasRefusal(network, netip.MustParseAddr(attached.IP), aliases, taken); err != nil {
			return lab.Plan{}, err
		}
		before := attached.AliasIPs
		server.PrivateNets[index].AliasIPs = addressStrings(aliases)
		serverID, networkID := server.ID, network.ID
		plan.Steps = l.Machine().ChangeAliasIPs
		plan.Undo = undoServer(serverID, func(_ *state.Tx, server *state.Server) {
			if _, index := serverNetwork(server, networkID); index >= 0 {
				server.PrivateNets[index].AliasIPs = before
			}
		})
		return plan, nil
	}
}
