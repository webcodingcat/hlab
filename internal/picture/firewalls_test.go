package picture

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// prefixes parses CIDRs for a rule.
func prefixes(cidrs ...string) []netip.Prefix {
	var out []netip.Prefix
	for _, cidr := range cidrs {
		out = append(out, netip.MustParsePrefix(cidr))
	}
	return out
}

// anywhere is what a rule names for all addresses: 0.0.0.0/0 and ::/0.
var anywhere = prefixes("0.0.0.0/0", "::/0")

// firewallPart returns the firewalls' part of a ruleset: from their first
// map to the chain postrouting.
func firewallPart(t *testing.T, text string) string {
	t.Helper()
	start := strings.Index(text, "\tmap firewall_in4")
	end := strings.Index(text, "\t# the servers reach the Internet")
	if start < 0 || end < start {
		t.Fatalf("the ruleset lacks the firewalls' part:\n%s", text)
	}
	return text[start:end]
}

// TestFirewallRulesetsMatchGoldenFiles builds the ruleset of the test lab
// with the firewalls of each case and compares the firewalls' part with
// testdata/firewall-<case>.golden. Each case follows Hetzner's semantics:
// inbound is closed except what a rule allows, outbound is open until a
// rule names it, the rules of several firewalls add up, GRE over IPv6 is
// dropped, icmp means ICMPv6 too.
func TestFirewallRulesetsMatchGoldenFiles(t *testing.T) {
	cases := map[string][]Firewall{
		"none": nil,
		"ports": {{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
			{Direction: DirectionIn, Protocol: "tcp", Port: "22", Addresses: anywhere},
			{Direction: DirectionIn, Protocol: "tcp", Port: "8000-8100", Addresses: anywhere},
			{Direction: DirectionIn, Protocol: "udp", Port: "53", Addresses: anywhere},
			{Direction: DirectionIn, Protocol: "udp", Port: "any", Addresses: prefixes("10.0.0.0/8")},
		}}},
		"out": {{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
			{Direction: DirectionOut, Protocol: "tcp", Port: "443", Addresses: anywhere},
			{Direction: DirectionOut, Protocol: "udp", Port: "53", Addresses: prefixes("198.18.0.2/32", "2001:db8:0:ffff::2/128")},
		}}},
		"icmp": {{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
			{Direction: DirectionIn, Protocol: "icmp", Addresses: anywhere},
			{Direction: DirectionOut, Protocol: "icmp", Addresses: prefixes("198.19.0.0/16")},
		}}},
		"esp-gre": {{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
			{Direction: DirectionIn, Protocol: "esp", Addresses: prefixes("192.0.2.0/24", "::/0")},
			{Direction: DirectionIn, Protocol: "gre", Addresses: prefixes("0.0.0.0/0", "2001:db8::/32")},
			{Direction: DirectionOut, Protocol: "gre", Addresses: prefixes("2001:db8::/32")},
		}}},
		"addresses": {{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
			{Direction: DirectionIn, Protocol: "tcp", Port: "22", Addresses: prefixes("198.18.0.1/32", "10.0.0.0/8", "2001:db8:0:ffff::1/128")},
			{Direction: DirectionIn, Protocol: "tcp", Port: "80", Addresses: prefixes("2001:db8::/32")},
			{Direction: DirectionOut, Protocol: "tcp", Port: "5432", Addresses: prefixes("198.19.0.2/32")},
		}}},
		"no-rules": {{ID: 1000010, ServerIDs: []int64{1000002}}},
		"several": {
			{ID: 1000012, ServerIDs: []int64{1000002, 1000001}, Rules: []FirewallRule{
				{Direction: DirectionIn, Protocol: "tcp", Port: "22", Addresses: anywhere},
				{Direction: DirectionOut, Protocol: "tcp", Port: "443", Addresses: anywhere},
			}},
			{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
				{Direction: DirectionIn, Protocol: "tcp", Port: "80", Addresses: anywhere},
				{Direction: DirectionIn, Protocol: "tcp", Port: "443", Addresses: anywhere},
			}},
			{ID: 1000011, ServerIDs: []int64{1000003}, Rules: []FirewallRule{
				{Direction: DirectionIn, Protocol: "udp", Port: "51820", Addresses: prefixes("::/0")},
			}},
		},
	}
	for name, firewalls := range cases {
		t.Run(name, func(t *testing.T) {
			lab := testLab()
			lab.Firewalls = firewalls
			checkGolden(t, "firewall-"+name, firewallPart(t, Ruleset(lab).Text))
		})
	}
}

// TestFirewallsFilterTheFloatingIPsOfTheirServers checks that a server's
// Floating IPs lead to its chains, as on Hetzner, where a firewall filters
// all of a server's public addresses.
func TestFirewallsFilterTheFloatingIPsOfTheirServers(t *testing.T) {
	lab := testLab()
	lab.Firewalls = []Firewall{{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{
		{Direction: DirectionOut, Protocol: "tcp", Port: "443", Addresses: anywhere},
	}}}
	text := Ruleset(lab).Text
	for _, want := range []string{
		"198.19.0.1 : jump server_1000001_in", "198.19.0.3 : jump server_1000001_in",
		"2001:db8:1:1::/64 : jump server_1000001_in", "2001:db8:1:4::/64 : jump server_1000001_in",
		"198.19.0.3 : jump server_1000001_out", "2001:db8:1:4::/64 : jump server_1000001_out",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the ruleset lacks %q:\n%s", want, firewallPart(t, text))
		}
	}
	if strings.Contains(text, "198.19.0.5 : jump server_1000001") {
		t.Error("another server's Floating IP leads to the server's chain")
	}
}

func TestFirewallRulesetDoesNotDependOnTheOrderOfFirewalls(t *testing.T) {
	firewalls := []Firewall{
		{ID: 1000011, ServerIDs: []int64{1000001}, Rules: []FirewallRule{{Direction: DirectionIn, Protocol: "tcp", Port: "80", Addresses: anywhere}}},
		{ID: 1000010, ServerIDs: []int64{1000001}, Rules: []FirewallRule{{Direction: DirectionIn, Protocol: "tcp", Port: "22", Addresses: anywhere}}},
	}
	lab := testLab()
	lab.Firewalls = firewalls
	reversed := testLab()
	reversed.Firewalls = slices.Clone(firewalls)
	slices.Reverse(reversed.Firewalls)
	if Ruleset(lab) != Ruleset(reversed) {
		t.Error("the order of the firewalls changes the ruleset")
	}
}
