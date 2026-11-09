// Package picture builds the data plane's pictures: the complete wanted
// configuration of each part of the appliance, which the agent applies as a
// whole. Each picture is a pure function of a Lab: the same Lab always gives
// the same picture, which the tests compare as text with golden files.
package picture

import (
	"cmp"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/seed"
)

// Hetzner's fixed addresses, which every server sees.
var (
	// Gateway4 is the on-link gateway of the public IPv4 /32.
	Gateway4 = netip.MustParseAddr("172.31.1.1")
	// Gateway6 is the gateway of the public IPv6 /64, as the seed names it.
	Gateway6 = seed.Gateway6
	// linkLocal is the appliance's link-local address on the host link.
	linkLocal = netip.MustParseAddr("fe80::2")
	// MetadataAddress is the metadata service.
	MetadataAddress = netip.MustParseAddr("169.254.169.254")
	// ServiceRange4 holds the IPv4 addresses that a server with IPv6 only
	// gets by DHCP, only to reach the metadata service and DHCP (measured
	// 2026-11-09). They live only on the public NIC behind the appliance.
	ServiceRange4 = netip.MustParsePrefix("100.64.0.0/10")
	// hetznerDHCP is Hetzner's DHCP server, dhcp2.your-server.de, to which
	// that lease routes too; nothing in the lab answers there.
	hetznerDHCP = netip.MustParseAddr("188.40.24.200")
)

// The appliance's NICs, in the order of the VM's NICs (1 to 4).
const (
	DeviceLink    = "link"    // the host link
	DevicePublic  = "public"  // the servers' public NICs, through a private VLAN
	DevicePrivate = "private" // the private networks, as a VLAN trunk (phase 2a)
	DeviceUplink  = "uplink"  // the Internet, through Hyper-V's Default Switch
)

// MACSet is one of the appliance's two sets of MAC addresses. An update
// starts the new appliance with the other set: a stopped VM with the same
// static MACs would disturb the running one.
type MACSet string

// The two sets.
const (
	MACSetA MACSet = "A"
	MACSetB MACSet = "B"
)

// MAC returns the MAC address of the appliance's NIC number nic (1 to 4).
func (s MACSet) MAC(nic int) string {
	set := "0a"
	if s == MACSetB {
		set = "0b"
	}
	return "02:48:4c:" + set + ":00:0" + string(rune('0'+nic))
}

// Other returns the other set, the one the next update takes.
func (s MACSet) Other() MACSet {
	if s == MACSetB {
		return MACSetA
	}
	return MACSetB
}

// Lab is what the pictures are built from: plain values that the lab takes
// from its state and settings. The servers come with phase 1b-4; until
// then the lab has none.
type Lab struct {
	// Generation numbers the pictures; every picture built from this Lab
	// carries it.
	Generation uint64
	MACSet     MACSet
	// Range4 and Range6 are the lab's whole address ranges: the host routes
	// them to the appliance, and they never leave the lab.
	Range4 netip.Prefix
	Range6 netip.Prefix
	// HostLink4 and HostLink6 are the host link's networks: the host is the
	// first address, the appliance the second; the third address of
	// HostLink4 is the appliance's source for ACME checks.
	HostLink4 netip.Prefix
	HostLink6 netip.Prefix
	// LabKey is the lab's public SSH key for `hlab ssh`; "" without one.
	LabKey  string
	Servers []Server
	// FloatingIPs are the Floating IPs assigned to one of Servers.
	FloatingIPs []FloatingIP
	// Firewalls are the Cloud Firewalls that apply to at least one of
	// Servers.
	Firewalls []Firewall
	// Networks are the private networks, each with the servers in it.
	Networks []Network
}

// FloatingIP is what the pictures need of an assigned Floating IP.
type FloatingIP struct {
	Prefix   netip.Prefix // an IPv4 /32 or an IPv6 /64
	ServerID int64
}

// Server is what the pictures need of a server.
type Server struct {
	ID       int64
	Name     string
	Location string // its name, such as "fsn1"
	MAC      string // of its public NIC
	IPv4     netip.Addr
	IPv6     netip.Prefix // the /64
	// ServiceIPv4 is the address of ServiceRange4 that a server with IPv6
	// only gets by DHCP; invalid for any other server.
	ServiceIPv4 netip.Addr
	// Internet: the server and its project may reach the Internet.
	Internet   bool
	PublicKeys []string
	// RootPassword is set for a server without SSH keys, as on Hetzner.
	RootPassword string
	UserData     string
}

// DefaultLab returns a Lab with the default address plan and no servers.
func DefaultLab() Lab {
	return Lab{
		MACSet:    MACSetA,
		Range4:    netip.MustParsePrefix("198.18.0.0/15"),
		Range6:    netip.MustParsePrefix("2001:db8::/32"),
		HostLink4: netip.MustParsePrefix("198.18.0.0/29"),
		HostLink6: netip.MustParsePrefix("2001:db8:0:ffff::/64"),
	}
}

// The numbers of the addresses in the host link's networks.
const (
	hostNumber      = 1 // the host: hlabd's APIs
	applianceNumber = 2 // the appliance: its endpoints, DNS among them
	acmeNumber      = 3 // the appliance's source for ACME checks (IPv4 only)
)

// hostAddress returns the address number n of a host link network.
func hostAddress(network netip.Prefix, n int) netip.Addr {
	address := network.Addr()
	for range n {
		address = address.Next()
	}
	return address
}

// floatingIPs returns the Floating IPs sorted by prefix, so that a picture
// does not depend on the order the lab lists them in.
func (l Lab) floatingIPs() []FloatingIP {
	floating := slices.Clone(l.FloatingIPs)
	slices.SortFunc(floating, func(a, b FloatingIP) int { return a.Prefix.Addr().Compare(b.Prefix.Addr()) })
	return floating
}

// server returns the server of an ID; ok is false if Servers lacks it.
func (l Lab) server(id int64) (server Server, ok bool) {
	index := slices.IndexFunc(l.Servers, func(server Server) bool { return server.ID == id })
	if index < 0 {
		return Server{}, false
	}
	return l.Servers[index], true
}

// servers returns the servers sorted by ID, so that a picture does not
// depend on the order the lab lists them in.
func (l Lab) servers() []Server {
	servers := slices.Clone(l.Servers)
	slices.SortFunc(servers, func(a, b Server) int { return cmp.Compare(a.ID, b.ID) })
	return servers
}

// resolvers returns the lab's DNS resolvers: the appliance's addresses.
func (l Lab) resolvers() []netip.Addr {
	return l.ApplianceAddresses()
}

// HostAddresses returns the host's addresses on the host link, with the
// link's prefix length: IPv4 first.
func (l Lab) HostAddresses() []netip.Prefix {
	return []netip.Prefix{
		netip.PrefixFrom(hostAddress(l.HostLink4, hostNumber), l.HostLink4.Bits()),
		netip.PrefixFrom(hostAddress(l.HostLink6, hostNumber), l.HostLink6.Bits()),
	}
}

// ApplianceAddresses returns the appliance's addresses on the host link, the
// host's gateways into the lab: IPv4 first.
func (l Lab) ApplianceAddresses() []netip.Addr {
	return []netip.Addr{
		hostAddress(l.HostLink4, applianceNumber),
		hostAddress(l.HostLink6, applianceNumber),
	}
}
