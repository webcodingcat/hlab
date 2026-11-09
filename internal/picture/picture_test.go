package picture

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/catalog"
)

var update = flag.Bool("update", false, "write the golden files anew")

// testLab returns a lab with three servers that cover the cases: both
// addresses with SSH keys, IPv4 only without Internet and with a password
// and user data, IPv6 only. Its Floating IPs cover a server with a Primary
// IP of their family, one without (on-link), and one without Internet.
// Both are listed out of order on purpose.
func testLab() Lab {
	lab := DefaultLab()
	lab.Generation = 7
	lab.MACSet = MACSetB
	lab.LabKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILabKey hlab"
	lab.Servers = []Server{
		{
			ID: 1000003, Name: "only-six", Location: "ash", MAC: "96:00:00:0f:42:43",
			IPv6: netip.MustParsePrefix("2001:db8:1:3::/64"), Internet: true,
		},
		{
			ID: 1000001, Name: "web", Location: "fsn1", MAC: "96:00:00:0f:42:41",
			IPv4: netip.MustParseAddr("198.19.0.1"), IPv6: netip.MustParsePrefix("2001:db8:1:1::/64"),
			Internet: true, PublicKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIUser user@example"},
		},
		{
			ID: 1000002, Name: "db", Location: "hel1", MAC: "96:00:00:0f:42:42",
			IPv4: netip.MustParseAddr("198.19.0.2"), RootPassword: `p"ss`,
			UserData: "#cloud-config\npackages: [nginx]\n",
		},
		// without public addresses: only in a private network
		{ID: 1000004, Name: "private", Location: "fsn1", MAC: "96:00:00:0f:42:44"},
	}
	lab.FloatingIPs = []FloatingIP{
		{Prefix: netip.MustParsePrefix("2001:db8:1:5::/64"), ServerID: 1000002},
		{Prefix: netip.MustParsePrefix("198.19.0.4/32"), ServerID: 1000003},
		{Prefix: netip.MustParsePrefix("198.19.0.3/32"), ServerID: 1000001},
		{Prefix: netip.MustParsePrefix("2001:db8:1:4::/64"), ServerID: 1000001},
		{Prefix: netip.MustParsePrefix("198.19.0.5/32"), ServerID: 1000002},
	}
	// two networks of the same range, listed out of order: one with two
	// servers, an alias IP and two routes, one through a gateway that no
	// server holds; one with a default route and a server without public
	// addresses
	lab.Networks = []Network{
		{ID: 1000021, Name: "backup", VLAN: 3, IPRange: netip.MustParsePrefix("10.0.0.0/16"),
			Subnets: []netip.Prefix{netip.MustParsePrefix("10.0.1.0/24")},
			Routes:  []NetworkRoute{{Destination: netip.MustParsePrefix("0.0.0.0/0"), Gateway: netip.MustParseAddr("10.0.0.5")}},
			Members: []NetworkMember{
				{ServerID: 1000004, Interface: 1, MAC: "86:00:00:00:00:04", IP: netip.MustParseAddr("10.0.1.2"), PrivateOnly: true},
			}},
		{ID: 1000020, Name: "app", VLAN: 2, IPRange: netip.MustParsePrefix("10.0.0.0/16"),
			Subnets: []netip.Prefix{netip.MustParsePrefix("10.0.1.0/24"), netip.MustParsePrefix("10.0.2.0/24")},
			Routes: []NetworkRoute{
				{Destination: netip.MustParsePrefix("10.100.0.0/24"), Gateway: netip.MustParseAddr("10.0.1.2")},
				{Destination: netip.MustParsePrefix("192.168.0.0/16"), Gateway: netip.MustParseAddr("10.0.9.9")},
			},
			Members: []NetworkMember{
				{ServerID: 1000001, Interface: 1, MAC: "86:00:00:00:00:01", IP: netip.MustParseAddr("10.0.1.2"),
					AliasIPs: []netip.Addr{netip.MustParseAddr("10.0.1.20"), netip.MustParseAddr("10.0.2.20")}},
				{ServerID: 1000002, Interface: 1, MAC: "86:00:00:00:00:02", IP: netip.MustParseAddr("10.0.1.3")},
			}},
	}
	return lab
}

// checkGolden compares got with testdata/<name>.golden; with -update it
// writes the file instead.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (go test -update writes it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s (go test -update writes it anew):\n%s", name, path, got)
	}
}

func TestLinksMatchGoldenFile(t *testing.T) {
	links := Links(testLab())
	if err := links.Check(); err != nil {
		t.Fatalf("the picture fails its own check: %v", err)
	}
	text, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "links", string(text)+"\n")
}

func TestRulesetMatchesGoldenFile(t *testing.T) {
	ruleset := Ruleset(testLab())
	if ruleset.Generation != 7 {
		t.Errorf("generation %d, want 7", ruleset.Generation)
	}
	checkGolden(t, "ruleset", ruleset.Text)
}

func TestRulesetWithoutServersHasAnEmptySet(t *testing.T) {
	text := Ruleset(DefaultLab()).Text
	if strings.Contains(text, "elements") {
		t.Errorf("nft refuses an empty elements list, but the ruleset has one:\n%s", text)
	}
}

func TestDHCPMatchesGoldenFile(t *testing.T) {
	dhcp := DHCP(testLab())
	if err := dhcp.Check(); err != nil {
		t.Fatalf("the picture fails its own check: %v", err)
	}
	var text strings.Builder
	for _, instance := range dhcp.Instances {
		fmt.Fprintf(&text, "### %s\n%s", instance.Device, instance.Config)
	}
	checkGolden(t, "dhcp", text.String())
}

// The metadata of each server: the summary, a key of it, the private
// networks (a key of their own, not in the summary, as on Hetzner,
// measured 2026-11-09) and the user data.
func TestMetadataMatchesGoldenFile(t *testing.T) {
	metadata := Metadata(testLab(), catalog.Builtin())
	if err := metadata.Check(); err != nil {
		t.Fatalf("the picture fails its own check: %v", err)
	}
	var text strings.Builder
	for _, server := range metadata.Servers {
		fmt.Fprintf(&text, "### sources %v private %v\n%s", server.Sources, server.Private, server.Metadata)
		fmt.Fprintf(&text, "### hostname: %s\n", server.Keys["hostname"])
		fmt.Fprintf(&text, "### private-networks\n%s", server.Keys["private-networks"])
		fmt.Fprintf(&text, "### userdata\n%s", server.UserData)
	}
	checkGolden(t, "metadata", text.String())
}

func TestPicturesDoNotDependOnTheOrderOfServersFloatingIPsAndNetworks(t *testing.T) {
	lab := testLab()
	reversed := testLab()
	slices.Reverse(reversed.Servers)
	slices.Reverse(reversed.FloatingIPs)
	slices.Reverse(reversed.Networks)
	for i := range reversed.Networks {
		slices.Reverse(reversed.Networks[i].Members)
	}
	if Ruleset(lab) != Ruleset(reversed) || !reflect.DeepEqual(DHCP(lab), DHCP(reversed)) {
		t.Error("the order of the servers or networks changes the ruleset or DHCP")
	}
	if !reflect.DeepEqual(Links(lab), Links(reversed)) {
		t.Error("the order of the servers, Floating IPs or networks changes the links")
	}
}

func TestMACSets(t *testing.T) {
	if got := MACSetA.MAC(1); got != "02:48:4c:0a:00:01" {
		t.Errorf("set A, NIC 1: %s", got)
	}
	if got := MACSetB.MAC(4); got != "02:48:4c:0b:00:04" {
		t.Errorf("set B, NIC 4: %s", got)
	}
}
