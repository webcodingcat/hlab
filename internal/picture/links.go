package picture

import (
	"net/netip"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// Links returns the picture of the appliance's links: its four NICs, their
// addresses, a route to each server's addresses through the public NIC,
// and a route to each assigned Floating IP through its server.
func Links(lab Lab) agentproto.Links {
	links := agentproto.Links{
		Generation: lab.Generation,
		Interfaces: []agentproto.Interface{
			{Name: DeviceLink, MAC: lab.MACSet.MAC(1)},
			{Name: DevicePublic, MAC: lab.MACSet.MAC(2)},
			// without addresses: it carries the VLANs of the private
			// networks (phase 2a)
			{Name: DevicePrivate, MAC: lab.MACSet.MAC(3)},
			// networkd runs DHCP on it
			{Name: DeviceUplink, MAC: lab.MACSet.MAC(4), Networkd: true},
		},
		Addresses: []agentproto.Address{
			{Device: DeviceLink, Prefix: prefixOf(hostAddress(lab.HostLink4, applianceNumber), lab.HostLink4), Announce: true},
			{Device: DeviceLink, Prefix: netip.PrefixFrom(hostAddress(lab.HostLink4, acmeNumber), 32), Announce: true},
			{Device: DeviceLink, Prefix: prefixOf(hostAddress(lab.HostLink6, applianceNumber), lab.HostLink6), Announce: true},
			// the kernel sends a neighbor solicitation only from a link-local
			// address, unless the packet that waits for it comes from the
			// appliance itself; without one, the answers of the servers to
			// the host waited until the host solicited the appliance (5 s)
			{Device: DeviceLink, Prefix: netip.PrefixFrom(linkLocal, 64)},
			{Device: DevicePublic, Prefix: netip.PrefixFrom(Gateway4, 32), Announce: true},
			{Device: DevicePublic, Prefix: netip.PrefixFrom(Gateway6, 64), Announce: true},
			{Device: "lo", Prefix: netip.PrefixFrom(MetadataAddress, 32)},
		},
	}
	for _, device := range []string{DeviceLink, DevicePublic, DevicePrivate, DeviceUplink} {
		links.Sysctls = append(links.Sysctls,
			// a router that sends redirects teaches servers to reach each
			// other directly, past the firewalls
			agentproto.Sysctl{Key: "net/ipv4/conf/" + device + "/send_redirects", Value: "0"})
	}
	for _, device := range []string{DeviceLink, DevicePublic, DevicePrivate} {
		links.Sysctls = append(links.Sysctls,
			// no link-local addresses of the kernel's: the picture names
			// its own, the same in both MAC sets, and the agent removes
			// what the picture does not have
			agentproto.Sysctl{Key: "net/ipv6/conf/" + device + "/addr_gen_mode", Value: "1"})
	}
	for _, server := range lab.servers() {
		if server.IPv4.IsValid() {
			links.Routes = append(links.Routes, agentproto.Route{
				Destination: netip.PrefixFrom(server.IPv4, 32),
				Device:      DevicePublic,
				Source:      Gateway4,
			})
		}
		if server.IPv6.IsValid() {
			links.Routes = append(links.Routes, agentproto.Route{
				Destination: server.IPv6.Masked(),
				Device:      DevicePublic,
			})
		}
	}
	for _, floating := range lab.floatingIPs() {
		if server, ok := lab.server(floating.ServerID); ok {
			links.Routes = append(links.Routes, floatingIPRoute(floating, server))
		}
	}
	return links
}

// floatingIPRoute returns the route to a Floating IP: through its server's
// Primary IP of the same family, where the guest answers ARP or neighbor
// solicitations already, so that a move only changes the route and no
// neighbor's cache holds the old server. The guest configures the Floating
// IP itself, as on Hetzner. A server without a Primary IP of that family
// gets the route on-link, and its guest must answer for the Floating IP.
func floatingIPRoute(floating FloatingIP, server Server) agentproto.Route {
	route := agentproto.Route{Destination: floating.Prefix.Masked(), Device: DevicePublic}
	switch {
	case floating.Prefix.Addr().Is4():
		route.Source = Gateway4
		if server.IPv4.IsValid() {
			route.Gateway = server.IPv4
		}
	case server.IPv6.IsValid():
		// the guest's address in its /64, as Hetzner's guests configure it
		route.Gateway = server.IPv6.Masked().Addr().Next()
	}
	return route
}

// prefixOf returns an address with the prefix length of its network.
func prefixOf(address netip.Addr, network netip.Prefix) netip.Prefix {
	return netip.PrefixFrom(address, network.Bits())
}
