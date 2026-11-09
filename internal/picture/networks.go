package picture

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// PrivateMTU is the MTU of Hetzner's private networks, which the servers
// get by DHCP.
const PrivateMTU = 1450

// vrfTableBase is added to a network's VLAN for its VRF's routing table,
// which keeps the tables clear of the kernel's own (253 to 255).
const vrfTableBase = 10000

// Network is what the pictures need of a private network: its VLAN, its
// range, its routes and the servers in it; its name and subnets for the
// metadata.
type Network struct {
	ID      int64
	Name    string
	VLAN    int
	IPRange netip.Prefix
	Subnets []netip.Prefix
	Routes  []NetworkRoute
	Members []NetworkMember
}

// NetworkRoute is a route of a network: Destination goes to Gateway, an
// address in the network.
type NetworkRoute struct {
	Destination netip.Prefix
	Gateway     netip.Addr
}

// NetworkMember is a server's NIC in a network: its MAC, its address and
// its alias IPs, which the guest configures itself, and its number among
// the server's private NICs (1 to 3), which the metadata names.
// PrivateOnly is set for a server without public addresses: it reaches
// the metadata service through the network.
type NetworkMember struct {
	ServerID    int64
	Interface   int
	MAC         string
	IP          netip.Addr
	AliasIPs    []netip.Addr
	PrivateOnly bool
}

// NetworkDevice returns the name of a network's VLAN device on the
// appliance's private NIC.
func NetworkDevice(vlan int) string {
	return DevicePrivate + "." + strconv.Itoa(vlan)
}

// vrfPrefix starts the name of each network's VRF.
const vrfPrefix = "vrf-net-"

// NetworkVRF returns the name of a network's VRF. Network IDs start at
// 1 000 000, so the name has 15 characters, Linux's longest, until IDs
// reach 10 000 000.
func NetworkVRF(id int64) string {
	return vrfPrefix + strconv.FormatInt(id, 10)
}

// NetworkGateway returns a network's gateway: the first address of its
// range, as on Hetzner.
func NetworkGateway(ipRange netip.Prefix) netip.Addr {
	return ipRange.Masked().Addr().Next()
}

// networks returns the networks sorted by ID, each with its members sorted
// by server, so that a picture does not depend on the order the lab lists
// them in.
func (l Lab) networks() []Network {
	networks := slices.Clone(l.Networks)
	slices.SortFunc(networks, func(a, b Network) int { return cmp.Compare(a.ID, b.ID) })
	for i, network := range networks {
		networks[i].Members = slices.Clone(network.Members)
		slices.SortFunc(networks[i].Members, func(a, b NetworkMember) int { return cmp.Compare(a.ServerID, b.ServerID) })
	}
	return networks
}

// addNetworkLinks adds each network's VRF and VLAN to the links picture:
// the gateway on the VLAN, a route to each address of a server in the
// network and to its alias IPs, the network's routes, and a blackhole for
// the rest, so that nothing leaves the VRF for the main table. Only what
// is handed out is routed: an address of the range that no server holds
// answers nothing, as on Hetzner.
func addNetworkLinks(links *agentproto.Links, lab Lab) {
	for _, network := range lab.networks() {
		device, vrf := NetworkDevice(network.VLAN), NetworkVRF(network.ID)
		table := uint32(vrfTableBase + network.VLAN)
		gateway := NetworkGateway(network.IPRange)
		links.VRFs = append(links.VRFs, agentproto.VRF{Name: vrf, Table: table})
		links.VLANs = append(links.VLANs, agentproto.VLAN{Name: device, Parent: DevicePrivate, ID: network.VLAN, VRF: vrf})
		links.Addresses = append(links.Addresses,
			agentproto.Address{Device: device, Prefix: netip.PrefixFrom(gateway, 32), Announce: true},
			// the metadata service in the network's VRF, for a server
			// without public addresses (networkDHCP)
			agentproto.Address{Device: device, Prefix: netip.PrefixFrom(MetadataAddress, 32)})
		links.Sysctls = append(links.Sysctls,
			agentproto.Sysctl{Key: "net/ipv4/conf/" + device + "/send_redirects", Value: "0"},
			agentproto.Sysctl{Key: "net/ipv6/conf/" + device + "/disable_ipv6", Value: "1"})
		for _, member := range network.Members {
			for _, address := range append([]netip.Addr{member.IP}, member.AliasIPs...) {
				links.Routes = append(links.Routes, agentproto.Route{
					Destination: netip.PrefixFrom(address, 32), Device: device, Source: gateway, Table: table,
				})
			}
		}
		defaultRoute := false
		for _, route := range network.Routes {
			// Hetzner takes a gateway that no server holds yet
			links.Routes = append(links.Routes, agentproto.Route{
				Destination: route.Destination.Masked(), Device: device, Gateway: route.Gateway,
				OnLink: true, Table: table,
			})
			defaultRoute = defaultRoute || route.Destination.Bits() == 0
		}
		if !defaultRoute {
			links.Routes = append(links.Routes, agentproto.Route{
				Destination: netip.PrefixFrom(netip.IPv4Unspecified(), 0), Blackhole: true, Table: table,
			})
		}
	}
}

// networkDHCP returns a dnsmasq instance for each private network: a
// server gets its address as a /32 with the gateway on-link and the
// network's range through the gateway, and the MTU 1450; no router and no
// name servers, which dnsmasq would send as its own address otherwise
// (Hetzner's lease, measured 2026-11-08). The shared-network names the VLAN device, which
// exists when dnsmasq starts.
func networkDHCP(lab Lab) []agentproto.DHCPInstance {
	var instances []agentproto.DHCPInstance
	for _, network := range lab.networks() {
		device, gateway := NetworkDevice(network.VLAN), NetworkGateway(network.IPRange)
		var config strings.Builder
		fmt.Fprintf(&config, "# dnsmasq for the private network %d, built by hlabd.\n", network.ID)
		writeLines(&config, dhcpBase(device)...)
		routes := fmt.Sprintf("%s/32,0.0.0.0,%s,%s", gateway, network.IPRange.Masked(), gateway)
		// a server without public addresses reaches the metadata service
		// through the gateway (measured 2026-11-09)
		metadataRoutes := fmt.Sprintf("%s,%s/32,%s", routes, MetadataAddress, gateway)
		writeLines(&config,
			"dhcp-option=option:router",
			"dhcp-option=option:dns-server",
			fmt.Sprintf("dhcp-option=option:mtu,%d", PrivateMTU),
			"dhcp-option=tag:!private-only,121,"+routes,
			// Microsoft's code of the same routes, which Hetzner sends too
			"dhcp-option=tag:!private-only,249,"+routes,
			"dhcp-option=tag:private-only,121,"+metadataRoutes,
			"dhcp-option=tag:private-only,249,"+metadataRoutes,
		)
		for _, member := range network.Members {
			fmt.Fprintf(&config, "# server %d\n", member.ServerID)
			fmt.Fprintf(&config, "shared-network=%s,%s\n", device, member.IP)
			// Hetzner's lease lasts a day (measured 2026-11-08)
			fmt.Fprintf(&config, "dhcp-range=%s,static,255.255.255.255,1d\n", member.IP)
			if member.PrivateOnly {
				fmt.Fprintf(&config, "dhcp-host=%s,set:private-only,%s\n", member.MAC, member.IP)
			} else {
				fmt.Fprintf(&config, "dhcp-host=%s,%s\n", member.MAC, member.IP)
			}
		}
		instances = append(instances, agentproto.DHCPInstance{Device: device, Config: config.String()})
	}
	return instances
}
