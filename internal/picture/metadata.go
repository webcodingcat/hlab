package picture

import (
	"net/netip"
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
		if server.IPv6.IsValid() {
			sources = append(sources, server.IPv6.Masked())
		}
		if len(sources) == 0 {
			continue // it cannot reach the service (private networks: phase 2a)
		}
		values := metadataValues(lab, cat, server)
		keys := map[string]string{}
		for key, value := range values {
			keys[key] = metadataText(value)
		}
		metadata.Servers = append(metadata.Servers, agentproto.MetadataServer{
			Sources:  sources,
			Metadata: metadataText(values),
			Keys:     keys,
			UserData: server.UserData,
		})
	}
	return metadata
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

// metadataValues returns the metadata of a server by key.
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
		// private networks come with phase 2a
		"private-networks": []any{},
	}
}

// networkConfig returns the public NIC's configuration (cloud-init's
// version 1): IPv4 by DHCP, IPv6 as the first address of the /64.
func networkConfig(lab Lab, server Server) map[string]any {
	resolvers := lab.resolvers()
	var subnets []any
	if server.IPv4.IsValid() {
		subnets = append(subnets, map[string]any{
			"type":            "dhcp",
			"ipv4":            true,
			"dns_nameservers": []string{resolvers[0].String()},
		})
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
