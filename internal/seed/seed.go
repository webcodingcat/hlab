// Package seed builds a server's cloud-init seed: the files of the NoCloud
// ISO (label "cidata") that Hyper-V's servers boot with. On Hetzner,
// cloud-init reads the same data from the metadata service, which it finds
// through the SMBIOS vendor "Hetzner"; Hyper-V cannot set that, so the seed
// carries it. Its functions are pure: the same server always gives the same
// files, which the tests compare with golden files.
package seed

import (
	"encoding/json"
	"net/netip"
	"strconv"
)

// Server is what a seed is built from.
type Server struct {
	ID   int64
	Name string
	MAC  string // of the public NIC
	// IPv4 tells whether the server has a public IPv4; it gets it by DHCP,
	// as on Hetzner, so the seed does not name it.
	IPv4 bool
	IPv6 netip.Prefix // the /64; the server takes its first address
	// Resolvers are the lab's DNS resolvers. They are always in the seed:
	// without them Ubuntu waits 120 s for DNS before it runs the rest of
	// cloud-init.
	Resolvers  []netip.Addr
	PublicKeys []string // the server's SSH keys and the lab's own
	// RootPassword is set for a server without SSH keys, as on Hetzner.
	RootPassword string
	UserData     string
}

// Gateway6 is Hetzner's IPv6 gateway, which every server uses.
var Gateway6 = netip.MustParseAddr("fe80::1")

// Files returns the seed's files by name: meta-data, user-data,
// vendor-data and network-config (version 2).
func Files(server Server) map[string][]byte {
	return map[string][]byte{
		"meta-data":      yamlText(metaData(server)),
		"user-data":      []byte(server.UserData),
		"vendor-data":    []byte(VendorData(server.RootPassword)),
		"network-config": yamlText(networkConfig(server)),
	}
}

// VendorData returns the cloud-config that lets root log in, as Hetzner's
// images do: with the SSH keys, or with the root password for a server
// without keys. The metadata service answers the same.
func VendorData(rootPassword string) string {
	if rootPassword == "" {
		return "#cloud-config\ndisable_root: false\nssh_pwauth: false\n"
	}
	return "#cloud-config\ndisable_root: false\nssh_pwauth: true\n" +
		"chpasswd:\n  expire: false\n  users:\n" +
		"  - {name: root, password: " + strconv.Quote(rootPassword) + ", type: text}\n"
}

// metaData returns NoCloud's meta-data: the instance ID is the server's ID,
// as Hetzner's metadata service reports it.
func metaData(server Server) map[string]any {
	keys := server.PublicKeys
	if keys == nil {
		keys = []string{}
	}
	return map[string]any{
		"instance-id":    strconv.FormatInt(server.ID, 10),
		"local-hostname": server.Name,
		"public-keys":    keys,
	}
}

// networkConfig returns the public NIC's configuration: IPv4 by DHCP (a /32
// with the on-link gateway 172.31.1.1, from the appliance), IPv6 as the
// first address of the /64 with the gateway fe80::1, as on Hetzner.
func networkConfig(server Server) map[string]any {
	nic := map[string]any{
		"match":    map[string]any{"macaddress": server.MAC},
		"set-name": "eth0",
		"dhcp4":    server.IPv4,
		"dhcp6":    false,
	}
	if server.IPv6.IsValid() {
		nic["addresses"] = []string{netip.PrefixFrom(server.IPv6.Masked().Addr().Next(), 64).String()}
		nic["routes"] = []any{map[string]any{"to": "::/0", "via": Gateway6.String()}}
	}
	resolvers := []string{}
	for _, address := range server.Resolvers {
		resolvers = append(resolvers, address.String())
	}
	nic["nameservers"] = map[string]any{"addresses": resolvers}
	return map[string]any{"version": 2, "ethernets": map[string]any{"eth0": nic}}
}

// yamlText encodes a value as JSON, which is YAML too. YAML itself would
// leave a MAC address such as 96:00:00:10:00:00 unquoted, and cloud-init's
// YAML 1.1 parser reads that as a number in base 60. Maps, lists and
// strings always encode.
func yamlText(value any) []byte {
	text, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(text, '\n')
}
