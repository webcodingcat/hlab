package picture

import (
	"cmp"
	"net/netip"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v4"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/seed"
)

// availabilityZones are the zones Hetzner's metadata service reports for
// its locations; other locations report "<location>-dc1". Measured on real
// servers after the datacenters' removal (compat/requirements.md).
var availabilityZones = map[string]string{
	"fsn1": "fsn1-dc14",
	"nbg1": "nbg1-dc3",
	"hel1": "hel1-dc2",
}

// Metadata returns what the metadata service answers each server: the keys
// that cloud-init's Hetzner data source reads at boot.
func Metadata(lab Lab, cat *catalog.Catalog) agentproto.Metadata {
	metadata := agentproto.Metadata{Generation: lab.Generation, Servers: []agentproto.MetadataServer{}}
	for _, server := range lab.servers() {
		var sources []netip.Prefix
		if server.IPv4.IsValid() {
			sources = append(sources, netip.PrefixFrom(server.IPv4, 32))
		}
		// a server with IPv6 only comes with its service address, as on
		// Hetzner, where its IPv6 has no route to the service
		if server.ServiceIPv4.IsValid() {
			sources = append(sources, netip.PrefixFrom(server.ServiceIPv4, 32))
		}
		if server.IPv6.IsValid() {
			sources = append(sources, server.IPv6.Masked())
		}
		private := privateSources(lab, server)
		if len(sources) == 0 && len(private) == 0 {
			continue // it cannot reach the service
		}
		values := metadataValues(lab, cat, server)
		keys := map[string]string{}
		for key, value := range values {
			keys[key] = metadataText(value)
		}
		// a key of its own, [] without a network, and not in the summary
		// (measured 2026-11-09)
		keys["private-networks"] = metadataText(privateNetworks(lab, server))
		metadata.Servers = append(metadata.Servers, agentproto.MetadataServer{
			Sources:  sources,
			Private:  private,
			Metadata: metadataText(values),
			Keys:     keys,
			UserData: server.UserData,
		})
	}
	return metadata
}

// privateSources returns a server's addresses in the private networks,
// each with its network's VRF, where a server without public addresses
// reaches the metadata service (measured 2026-11-09); a server with them
// has no route there.
func privateSources(lab Lab, server Server) []agentproto.PrivateSource {
	var sources []agentproto.PrivateSource
	for _, network := range lab.networks() {
		for _, member := range network.Members {
			if member.ServerID == server.ID && member.PrivateOnly {
				sources = append(sources, agentproto.PrivateSource{VRF: NetworkVRF(network.ID), Address: member.IP})
			}
		}
	}
	return sources
}

// ServerKeys returns the SSH keys a server's root takes: its own and the
// lab's, for `hlab ssh`. The metadata service and the seed both give them.
func ServerKeys(lab Lab, server Server) []string {
	keys := append([]string{}, server.PublicKeys...)
	if lab.LabKey != "" {
		keys = append(keys, lab.LabKey)
	}
	return keys
}

// metadataValues returns the metadata of a server by key, as the summary
// /hetzner/v1/metadata lists them.
func metadataValues(lab Lab, cat *catalog.Catalog, server Server) map[string]any {
	keys := ServerKeys(lab, server)
	zone, found := availabilityZones[server.Location]
	if !found {
		zone = server.Location + "-dc1"
	}
	region := "eu-central"
	if location, found := cat.LocationByName(server.Location); found {
		region = location.NetworkZone
	}
	publicIPv4 := ""
	if server.IPv4.IsValid() {
		publicIPv4 = server.IPv4.String()
	}
	return map[string]any{
		"hostname":          server.Name,
		"instance-id":       server.ID,
		"public-ipv4":       publicIPv4,
		"local-ipv4":        "",
		"availability-zone": zone,
		"region":            region,
		"public-keys":       keys,
		"vendor_data":       seed.VendorData(server.RootPassword),
		"network-config":    networkConfig(lab, server),
	}
}

// privateNetwork is one entry of the metadata's private-networks, with
// Hetzner's keys in Hetzner's order (measured 2026-11-08).
type privateNetwork struct {
	IP           string   `yaml:"ip"`
	AliasIPs     []string `yaml:"alias_ips"`
	InterfaceNum int      `yaml:"interface_num"`
	MACAddress   string   `yaml:"mac_address"`
	NetworkID    int64    `yaml:"network_id"`
	NetworkName  string   `yaml:"network_name"`
	Network      string   `yaml:"network"`
	Subnet       string   `yaml:"subnet"`
	Gateway      string   `yaml:"gateway"`
}

// privateNetworks returns a server's private networks as the metadata
// lists them, by their interface number. Hetzner's order changes from one
// read to the next; a fixed one keeps the picture stable.
func privateNetworks(lab Lab, server Server) []privateNetwork {
	entries := []privateNetwork{}
	for _, network := range lab.networks() {
		for _, member := range network.Members {
			if member.ServerID != server.ID {
				continue
			}
			entry := privateNetwork{
				IP:           member.IP.String(),
				AliasIPs:     []string{},
				InterfaceNum: member.Interface,
				MACAddress:   member.MAC,
				NetworkID:    network.ID,
				NetworkName:  network.Name,
				Network:      network.IPRange.String(),
				Gateway:      NetworkGateway(network.IPRange).String(),
			}
			for _, alias := range member.AliasIPs {
				entry.AliasIPs = append(entry.AliasIPs, alias.String())
			}
			for _, subnet := range network.Subnets {
				if subnet.Contains(member.IP) {
					entry.Subnet = subnet.String()
				}
			}
			entries = append(entries, entry)
		}
	}
	slices.SortFunc(entries, func(a, b privateNetwork) int { return cmp.Compare(a.InterfaceNum, b.InterfaceNum) })
	return entries
}

// networkConfig returns the public NIC's configuration (cloud-init's
// version 1): IPv4 by DHCP, also for a server with IPv6 only, IPv6 as the
// first address of the /64; none for a server without public addresses
// (measured 2026-11-09).
func networkConfig(lab Lab, server Server) map[string]any {
	if !server.IPv4.IsValid() && !server.IPv6.IsValid() {
		return map[string]any{"version": 1, "config": []any{}}
	}
	resolvers := lab.resolvers()
	var subnets []any
	switch {
	case server.IPv4.IsValid():
		subnets = append(subnets, map[string]any{
			"type":            "dhcp",
			"ipv4":            true,
			"dns_nameservers": []string{resolvers[0].String()},
		})
	case server.ServiceIPv4.IsValid():
		// the lease only reaches the metadata service: no DNS server
		// (measured 2026-11-09)
		subnets = append(subnets, map[string]any{"type": "dhcp", "ipv4": true})
	}
	if server.IPv6.IsValid() {
		subnets = append(subnets, map[string]any{
			"type":            "static",
			"ipv6":            true,
			"address":         netip.PrefixFrom(server.IPv6.Masked().Addr().Next(), 64).String(),
			"gateway":         Gateway6.String(),
			"dns_nameservers": []string{resolvers[1].String()},
		})
	}
	return map[string]any{
		"version": 1,
		"config": []any{map[string]any{
			"type": "physical",
			"name": "eth0",
			// quoted: a YAML 1.1 parser, cloud-init's among them, reads
			// an unquoted MAC of digits (96:00:00:10:00:00) as a number
			"mac_address": &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: server.MAC},
			"subnets":     subnets,
		}},
	}
}

// metadataText returns a value as the service answers it: strings and
// numbers as they are, everything else as YAML.
func metadataText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case int64:
		return strconv.FormatInt(value, 10)
	}
	text, err := yaml.Dump(value)
	if err != nil {
		panic(err) // maps, lists and strings always encode
	}
	return string(text)
}
