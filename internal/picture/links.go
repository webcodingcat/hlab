package picture

import (
	"net/netip"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// Links returns the picture of the appliance's links: its four NICs, their
// addresses, and a route to each server's addresses through the public NIC.
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
			// no link-local addresses of the kernel's: fe80::1 is the only
			// one the servers need, and the agent removes what the picture
			// does not have
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
	return links
}

// prefixOf returns an address with the prefix length of its network.
func prefixOf(address netip.Addr, network netip.Prefix) netip.Prefix {
	return netip.PrefixFrom(address, network.Bits())
}
