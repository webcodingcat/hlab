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
	// DHCP4 tells whether the public NIC takes an IPv4 by DHCP: the public
	// IPv4, or for a server with IPv6 only the address that reaches the
	// metadata service, as on Hetzner; so the seed names no IPv4.
	DHCP4 bool
	IPv6  netip.Prefix // the /64; the server takes its first address
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
// it. Hyper-V's driver resets the NIC for that, and its carrier goes and
// comes back about 2 s later, often after dhcpcd has its lease; dhcpcd
// ignores the carrier, else it would drop the lease's address meanwhile.
// The NICs that are there when the script runs, at the first boot, get
// their unit started directly: a udev trigger would touch eth0 too. The
// script waits for their leases, up to 30 s each, so that the user-data's
// commands, which run after it, find the private networks up, as hc-utils
// has them up on Hetzner before cloud-init's last stage.
const privateNICsScript = `#!/bin/sh
cat > /etc/systemd/system/hlab-private-net@.service <<'UNIT'
[Unit]
Description=HLab private network on %i
BindsTo=sys-subsystem-net-devices-%i.device
After=sys-subsystem-net-devices-%i.device

[Service]
Type=forking
ExecStartPre=ip link set dev %i mtu 1450
ExecStart=/usr/sbin/dhcpcd --quiet --waitip --ipv4only --noarp --nolink --script=/bin/true %i
ExecStop=/usr/sbin/dhcpcd --exit %i
UNIT
cat > /etc/udev/rules.d/90-hlab-private-net.rules <<'RULE'
SUBSYSTEM=="net", ACTION=="add", ATTR{address}=="86:00:*", TAG+="systemd", ENV{SYSTEMD_WANTS}+="hlab-private-net@%k.service"
RULE
systemctl daemon-reload
udevadm control --reload
for nic in /sys/class/net/*; do
  case "$(cat "$nic/address")" in
    86:00:*) timeout 30 systemctl start "hlab-private-net@${nic##*/}.service" || true ;;
  esac
done
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
// with the on-link gateway 172.31.1.1, from the appliance; for a server
// with IPv6 only one with routes to the metadata service), IPv6 as the
// first address of the /64 with the gateway fe80::1, as on Hetzner. A
// server without public addresses gets none, as Hetzner's metadata gives
// it none (measured 2026-11-09): a public NIC to configure would hold up
// its boot.
func networkConfig(server Server) map[string]any {
	if !server.DHCP4 && !server.IPv6.IsValid() {
		return map[string]any{"version": 2, "ethernets": map[string]any{}}
	}
	nic := map[string]any{
		"match":    map[string]any{"macaddress": server.MAC},
		"set-name": "eth0",
		"dhcp4":    server.DHCP4,
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
