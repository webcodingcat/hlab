package picture

import (
	"fmt"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// DHCP returns the configuration of the dnsmasq instances: on the public
// NIC, each server with an IPv4 gets it as a /32 with the on-link gateway
// 172.31.1.1, as on Hetzner; a server with IPv6 only gets its ServiceIPv4
// (serviceLease); each private network has an instance of its own
// (addNetworkDHCP).
func DHCP(lab Lab) agentproto.DHCP {
	resolvers := lab.resolvers()
	var config strings.Builder
	config.WriteString("# dnsmasq for the servers' public NICs, built by hlabd.\n")
	writeLines(&config, dhcpBase(DevicePublic)...)
	writeLines(&config,
		"dhcp-option=option:router,"+Gateway4.String(),
		// the gateway is on-link: a route to it, then the default route,
		// in options 121 and 249 alike (measured 2026-11-09)
		fmt.Sprintf("dhcp-option=121,%s/32,0.0.0.0,0.0.0.0/0,%s", Gateway4, Gateway4),
		fmt.Sprintf("dhcp-option=249,%s/32,0.0.0.0,0.0.0.0/0,%s", Gateway4, Gateway4),
		"dhcp-option=option:dns-server,"+resolvers[0].String(),
	)
	writeLines(&config, serviceLease()...)
	for _, server := range lab.servers() {
		address, tag := server.IPv4, ""
		if !address.IsValid() {
			address, tag = server.ServiceIPv4, "set:"+serviceTag+","
		}
		if !address.IsValid() {
			continue
		}
		// shared-network lets dnsmasq hand out an address that is in no
		// network of the interface: the public NIC holds only 172.31.1.1.
		// It names the NIC by that address, because a name must exist
		// already when dnsmasq reads the file.
		fmt.Fprintf(&config, "# server %d\n", server.ID)
		fmt.Fprintf(&config, "shared-network=%s,%s\n", Gateway4, address)
		// Hetzner's lease lasts a day (measured 2026-11-08)
		fmt.Fprintf(&config, "dhcp-range=%s,static,255.255.255.255,1d\n", address)
		fmt.Fprintf(&config, "dhcp-host=%s,%s%s\n", server.MAC, tag, address)
	}
	dhcp := agentproto.DHCP{
		Generation: lab.Generation,
		Instances:  []agentproto.DHCPInstance{{Device: DevicePublic, Config: config.String()}},
	}
	dhcp.Instances = append(dhcp.Instances, networkDHCP(lab)...)
	return dhcp
}

// serviceTag marks the leases of servers with IPv6 only.
const serviceTag = "ipv6only"

// serviceLease returns the options of a server with IPv6 only, which
// replace the others of the same number: Hetzner's lease routes only to
// 172.31.1.1, the metadata service and its DHCP server, in options 121 and
// 249 alike, and names no DNS server (measured 2026-11-09). The classless
// routes leave the guest without a default route (RFC 3442).
func serviceLease() []string {
	routes := fmt.Sprintf("%s/32,0.0.0.0,%s/32,%s,%s/32,%s",
		Gateway4, MetadataAddress, Gateway4, hetznerDHCP, Gateway4)
	return []string{
		"dhcp-option=tag:" + serviceTag + ",121," + routes,
		"dhcp-option=tag:" + serviceTag + ",249," + routes,
		// without a value: no DNS server
		"dhcp-option=tag:" + serviceTag + ",option:dns-server",
	}
}

// dhcpBase returns the lines every instance starts with: DHCP only on its
// one device, which binds its socket to the device, so that the instances
// of the private networks' VRFs take their own requests.
func dhcpBase(device string) []string {
	return []string{
		"port=0", // DHCP only; the lab's DNS is unbound
		"interface=" + device,
		"bind-dynamic",
		"dhcp-authoritative",
		"leasefile-ro",
		"dhcp-option=option:netmask,255.255.255.255",
	}
}

// writeLines writes lines, each ended by a newline.
func writeLines(config *strings.Builder, lines ...string) {
	for _, line := range lines {
		config.WriteString(line + "\n")
	}
}
