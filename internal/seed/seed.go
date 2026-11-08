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
// vendor-data and network-config (version 2). The vendor-data is
// Hetzner's (VendorData) with the lab's private NICs (seedVendorData).
func Files(server Server) map[string][]byte {
	return map[string][]byte{
		"meta-data":      yamlText(metaData(server)),
		"user-data":      []byte(server.UserData),
		"vendor-data":    []byte(seedVendorData(server.RootPassword)),
		"network-config": yamlText(networkConfig(server)),
	}
}

// privateNICsScript configures the NICs in private networks, MAC 86:00:…,
// by DHCP, also those attached later while the guest runs. Hetzner's
// images do that with their hc-utils: a udev rule starts a unit per NIC,
// which runs dhcpcd for IPv4 without hooks (measured 2026-11-08 on
// Hetzner's Debian 13). The official cloud images lack it, and cloud-init
// configures only the NICs its network-config names. The script does the
// same, so that it works beside ifupdown (Debian) and networkd (Ubuntu).
// Hetzner's NIC brings the MTU 1450 itself, which a Hyper-V NIC does not,
// and dhcpcd without hooks does not take it from the lease: the unit sets
// it. A trigger catches the NICs that are there at the first boot.
const privateNICsScript = `#!/bin/sh
cat > /etc/systemd/system/hlab-private-net@.service <<'UNIT'
[Unit]
Description=HLab private network on %i
BindsTo=sys-subsystem-net-devices-%i.device
After=sys-subsystem-net-devices-%i.device

[Service]
Type=forking
ExecStartPre=ip link set dev %i mtu 1450
ExecStart=/usr/sbin/dhcpcd --quiet --waitip --ipv4only --noarp --script=/bin/true %i
ExecStop=/usr/sbin/dhcpcd --exit %i
UNIT
cat > /etc/udev/rules.d/90-hlab-private-net.rules <<'RULE'
SUBSYSTEM=="net", ACTION=="add", ATTR{address}=="86:00:*", TAG+="systemd", ENV{SYSTEMD_WANTS}+="hlab-private-net@%k.service"
RULE
systemctl daemon-reload
udevadm control --reload
udevadm trigger --subsystem-match=net --action=add
`

// seedVendorData returns the seed's vendor-data: Hetzner's cloud-config
// and the script for the private NICs, as a MIME multipart, so that the
// user-data's runcmd does not replace the script, as it would replace a
// runcmd of the vendor-data's cloud-config.
func seedVendorData(rootPassword string) string {
	const boundary = "==HLAB=="
	return "Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\nMIME-Version: 1.0\n\n" +
		"--" + boundary + "\nContent-Type: text/cloud-config; charset=\"us-ascii\"\n\n" +
		VendorData(rootPassword) + "\n" +
		"--" + boundary + "\nContent-Type: text/x-shellscript; charset=\"us-ascii\"\n\n" +
		privateNICsScript + "\n" +
		"--" + boundary + "--\n"
}

// VendorData returns the cloud-config that lets root log in, as Hetzner's
// images do: with the SSH keys, or with the root password for a server
// without keys. The metadata service answers it as Hetzner's vendor_data;
// the seed adds the private NICs (seedVendorData).
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
