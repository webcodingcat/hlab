package lab

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// The VLANs the lab hands out to private networks, one each: 1 is the
// switch's default, 4095 is reserved.
const (
	firstVLAN = 2
	lastVLAN  = 4094
)

// ErrNoVLAN reports that every VLAN is taken: the lab holds at most 4093
// private networks.
var ErrNoVLAN = errors.New("the lab has no free VLAN left for a private network")

// NewVLAN returns the next free VLAN for a new network. VLANs go out in
// order; one that was released comes back only once the pool has gone
// round, so that a server's NIC that a failed detach left behind does not
// land in a new network at once. The caller puts the network that holds
// it in the same Tx.
func NewVLAN(tx *state.Tx) (int, error) {
	used := map[int]bool{}
	for _, network := range state.All(tx, state.Networks) {
		used[network.VLAN] = true
	}
	pools := &tx.Lab().Pools
	size := lastVLAN - firstVLAN + 1
	for range size {
		offset := pools.NextVLAN % size
		pools.NextVLAN = offset + 1
		if vlan := firstVLAN + offset; !used[vlan] {
			return vlan, nil
		}
	}
	return 0, ErrNoVLAN
}

// NewPrivateMAC returns the MAC address of a server's new NIC in a private
// network: 86:00 and 32 bits of a counter, so that no two NICs ever share
// one, even across networks.
func NewPrivateMAC(tx *state.Tx) string {
	pools := &tx.Lab().Pools
	pools.NextPrivateMAC++
	n := pools.NextPrivateMAC
	return fmt.Sprintf("86:00:%02x:%02x:%02x:%02x", byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// ChangeNetwork is the steps of a change to a network's subnets or range
// (add_subnet, delete_subnet, change_ip_range): the plan has changed the
// record, and the appliance gets the pictures with it. Hetzner's action
// ends at once (measured 2026-11-08).
func (m *Machine) ChangeNetwork(ctx context.Context) error {
	return m.UpdatePictures(ctx)
}

// ChangeNetworkRoutes is the steps of add_route and delete_route: the
// appliance gets the pictures with the network's new routes; with servers
// in the network, Hetzner takes its time to tell them.
func (m *Machine) ChangeNetworkRoutes(ctx context.Context, networkID int64) error {
	if err := m.UpdatePictures(ctx); err != nil {
		return err
	}
	members := false
	_ = m.lab.View(func(tx *state.Tx) error {
		members = len(state.IDs(tx, state.Servers, func(server *state.Server) bool {
			return slices.ContainsFunc(server.PrivateNets, func(nic state.ServerPrivateNet) bool { return nic.NetworkID == networkID })
		})) > 0
		return nil
	})
	if !members {
		return nil
	}
	return m.lab.Sleep(ctx, NetworkRoutes)
}

// AttachServerToNetwork is the steps of attach_to_network: the plan has
// recorded the server's NIC in the network; the NIC goes into the VM, also
// one that runs, and the appliance gets the pictures with the server's
// address. If the pictures fail, the NIC goes again, and the action's undo
// takes the record back.
func (m *Machine) AttachServerToNetwork(ctx context.Context, serverID, networkID int64) error {
	var nic system.PrivateNIC
	err := m.lab.View(func(tx *state.Tx) error {
		server, err := state.Get(tx, state.Servers, serverID)
		if err != nil {
			return err
		}
		for _, found := range privateNICs(tx, server) {
			if found.NetworkID == networkID {
				nic = found.PrivateNIC
				return nil
			}
		}
		return fmt.Errorf("server %d is not in network %d: %w", serverID, networkID, state.ErrNotFound)
	})
	if err != nil {
		return err
	}
	vm := ServerVM(serverID)
	if err := m.system.AttachPrivateNIC(ctx, vm, nic); err != nil {
		return err
	}
	if err := m.UpdatePictures(ctx); err != nil {
		if ctx.Err() == nil {
			err = errors.Join(err, m.system.DetachPrivateNIC(ctx, vm, nic.MAC))
		}
		return err
	}
	return m.lab.Sleep(ctx, NICAttach)
}

// DetachServerFromNetwork is the steps of detach_from_network: the plan has
// taken the server's NIC out of the record; the appliance gets the pictures
// without it first, then the NIC leaves the VM. If that fails, the action's
// undo puts the record back, and the pictures after it the routes. Hetzner
// ends the action at once on a running server and takes its time on one
// that is off.
func (m *Machine) DetachServerFromNetwork(ctx context.Context, serverID int64, mac string) error {
	if err := m.UpdatePictures(ctx); err != nil {
		return err
	}
	vm := ServerVM(serverID)
	if err := m.system.DetachPrivateNIC(ctx, vm, mac); err != nil {
		return err
	}
	runs, err := m.system.ServerRuns(ctx, vm)
	if err != nil || runs {
		return err
	}
	return m.lab.Sleep(ctx, NICDetachOff)
}

// ServerNIC is a server's NIC in a private network, by its MAC.
type ServerNIC struct {
	ServerID int64
	MAC      string
}

// DetachNICsLater takes the NICs of a deleted network out of their VMs in
// the background: the network's delete has taken them out of the records
// and answers at once, as Hetzner does. The appliance gets the pictures
// without the network first. It ends with the lab's context, and Close
// waits for it; a failure is logged: the NIC stays in the VM, without a
// network, until the server goes.
func (m *Machine) DetachNICsLater(nics []ServerNIC) {
	m.lab.mu.Lock()
	defer m.lab.mu.Unlock()
	if m.lab.closed {
		return
	}
	m.lab.background.Go(func() {
		ctx := m.lab.ctx
		if err := m.UpdatePictures(ctx); err != nil && ctx.Err() == nil {
			m.lab.logger.Warn("sending the pictures after a network's delete; the watchdog sends them again", "error", err)
		}
		for _, nic := range nics {
			if err := m.system.DetachPrivateNIC(ctx, ServerVM(nic.ServerID), nic.MAC); err != nil && ctx.Err() == nil {
				m.lab.logger.Warn("removing a deleted network's NIC", "server", nic.ServerID, "error", err)
			}
		}
	})
}

// ChangeAliasIPs is the steps of change_alias_ips: the plan has changed the
// record, the appliance routes the new alias IPs to the server, and then it
// waits Hetzner's time. The guest configures them itself, as on Hetzner.
func (m *Machine) ChangeAliasIPs(ctx context.Context) error {
	if err := m.UpdatePictures(ctx); err != nil {
		return err
	}
	return m.lab.Sleep(ctx, AliasIPs)
}

// networkNIC is a server's NIC in a network, as the system needs it.
type networkNIC struct {
	NetworkID int64
	system.PrivateNIC
}

// privateNICs returns a server's NICs in private networks with their
// networks' VLANs; a network that is gone leaves its NIC out.
func privateNICs(tx *state.Tx, server *state.Server) []networkNIC {
	var nics []networkNIC
	for _, attached := range server.PrivateNets {
		network, err := state.Get(tx, state.Networks, attached.NetworkID)
		if err != nil {
			continue
		}
		nics = append(nics, networkNIC{
			NetworkID:  attached.NetworkID,
			PrivateNIC: system.PrivateNIC{MAC: attached.MAC, VLAN: network.VLAN},
		})
	}
	return nics
}

// systemNICs returns a server's NICs in private networks for the system.
func systemNICs(tx *state.Tx, server *state.Server) []system.PrivateNIC {
	var nics []system.PrivateNIC
	for _, nic := range privateNICs(tx, server) {
		nics = append(nics, nic.PrivateNIC)
	}
	return nics
}

// networkFacts returns what the pictures need of the private networks:
// each with its routes and the servers in it. The API has checked the
// addresses, so one that does not parse cannot occur; it would be left
// out.
func networkFacts(tx *state.Tx) []picture.Network {
	var facts []picture.Network
	index := map[int64]int{}
	for _, network := range state.All(tx, state.Networks) {
		fact := picture.Network{ID: network.ID, Name: network.Name, VLAN: network.VLAN}
		fact.IPRange, _ = netip.ParsePrefix(network.IPRange)
		for _, subnet := range network.Subnets {
			if prefix, err := netip.ParsePrefix(subnet.IPRange); err == nil {
				fact.Subnets = append(fact.Subnets, prefix)
			}
		}
		for _, route := range network.Routes {
			destination, destErr := netip.ParsePrefix(route.Destination)
			gateway, gwErr := netip.ParseAddr(route.Gateway)
			if destErr == nil && gwErr == nil {
				fact.Routes = append(fact.Routes, picture.NetworkRoute{Destination: destination, Gateway: gateway})
			}
		}
		index[network.ID] = len(facts)
		facts = append(facts, fact)
	}
	for _, server := range state.All(tx, state.Servers) {
		privateOnly := server.IPv4 == 0 && server.IPv6 == 0
		for n, attached := range server.PrivateNets {
			i, found := index[attached.NetworkID]
			ip, err := netip.ParseAddr(attached.IP)
			if !found || err != nil {
				continue
			}
			// the NIC's number counts the server's networks in the order
			// they were attached
			member := picture.NetworkMember{ServerID: server.ID, Interface: n + 1, MAC: attached.MAC, IP: ip, PrivateOnly: privateOnly}
			for _, alias := range attached.AliasIPs {
				if address, err := netip.ParseAddr(alias); err == nil {
					member.AliasIPs = append(member.AliasIPs, address)
				}
			}
			facts[i].Members = append(facts[i].Members, member)
		}
	}
	return facts
}
