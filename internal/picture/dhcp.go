package picture

import (
	"fmt"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// DHCP returns dnsmasq's configuration: each server with an IPv4 gets it on
// its public NIC as a /32 with the on-link gateway 172.31.1.1, as on Hetzner.
func DHCP(lab Lab) agentproto.DHCP {
	resolvers := lab.resolvers()
	var config strings.Builder
	config.WriteString("# dnsmasq for the servers' public NICs, built by hlabd.\n")
	for _, line := range []string{
		"port=0", // DHCP only; the lab's DNS is unbound
		"interface=" + DevicePublic,
		"bind-dynamic",
		"dhcp-authoritative",
		"leasefile-ro",
		"dhcp-option=option:netmask,255.255.255.255",
		"dhcp-option=option:router," + Gateway4.String(),
		// the gateway is on-link: a route to it, then the default route
		fmt.Sprintf("dhcp-option=121,%s/32,0.0.0.0,0.0.0.0/0,%s", Gateway4, Gateway4),
		"dhcp-option=option:dns-server," + resolvers[0].String(),
	} {
		config.WriteString(line + "\n")
	}
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
	return agentproto.DHCP{Generation: lab.Generation, Config: config.String()}
}
