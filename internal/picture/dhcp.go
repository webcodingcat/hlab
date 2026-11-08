package picture

import (
	"fmt"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// DHCP returns the configuration of the dnsmasq instances: on the public
// NIC, each server with an IPv4 gets it as a /32 with the on-link gateway
// 172.31.1.1, as on Hetzner; each private network has an instance of its
// own (addNetworkDHCP).
func DHCP(lab Lab) agentproto.DHCP {
	resolvers := lab.resolvers()
	var config strings.Builder
	config.WriteString("# dnsmasq for the servers' public NICs, built by hlabd.\n")
	writeLines(&config, dhcpBase(DevicePublic)...)
	writeLines(&config,
		"dhcp-option=option:router,"+Gateway4.String(),
		// the gateway is on-link: a route to it, then the default route
		fmt.Sprintf("dhcp-option=121,%s/32,0.0.0.0,0.0.0.0/0,%s", Gateway4, Gateway4),
		"dhcp-option=option:dns-server,"+resolvers[0].String(),
	)
	for _, server := range lab.servers() {
		if !server.IPv4.IsValid() {
			continue
		}
		// shared-network lets dnsmasq hand out an address that is in no
		// network of the interface: the public NIC holds only 172.31.1.1.
		// It names the NIC by that address, because a name must exist
		// already when dnsmasq reads the file.
		fmt.Fprintf(&config, "# server %d\n", server.ID)
		fmt.Fprintf(&config, "shared-network=%s,%s\n", Gateway4, server.IPv4)
		fmt.Fprintf(&config, "dhcp-range=%s,static,255.255.255.255,infinite\n", server.IPv4)
		fmt.Fprintf(&config, "dhcp-host=%s,%s\n", server.MAC, server.IPv4)
	}
	dhcp := agentproto.DHCP{
		Generation: lab.Generation,
		Instances:  []agentproto.DHCPInstance{{Device: DevicePublic, Config: config.String()}},
	}
	dhcp.Instances = append(dhcp.Instances, networkDHCP(lab)...)
	return dhcp
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
