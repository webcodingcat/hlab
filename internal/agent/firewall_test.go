package agent_test

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentapi"
)

// IP protocol numbers as FilterRule.Protocol holds them.
const (
	protoICMP uint8 = 1
	protoTCP  uint8 = 6
	protoUDP  uint8 = 17
)

func pfx(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, v := range s {
		out[i] = netip.MustParsePrefix(v)
	}
	return out
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

// ruleKeys renders filter rules in a sorted, comparable form, so a union
// is compared without depending on the order the rules come in.
func ruleKeys(rules []agent.FilterRule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		ps := make([]string, len(r.Prefixes))
		for j, p := range r.Prefixes {
			ps[j] = p.String()
		}
		slices.Sort(ps)
		out[i] = fmt.Sprintf("proto=%d ports=%d-%d from=%s", r.Protocol, r.FromPort, r.ToPort, strings.Join(ps, ","))
	}
	slices.Sort(out)
	return out
}

func addrKeys(addrs []netip.Addr) []string {
	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}
	slices.Sort(out)
	return out
}

func compile(t *testing.T, cfg agentapi.Config) map[int64]agent.ServerFilter {
	t.Helper()
	filters, err := agent.CompileFirewalls(cfg)
	if err != nil {
		t.Fatalf("CompileFirewalls: %v", err)
	}
	byID := make(map[int64]agent.ServerFilter, len(filters))
	for i, f := range filters {
		if i > 0 && filters[i-1].ServerID >= f.ServerID {
			t.Errorf("filters not ordered by server ID: %d before %d", filters[i-1].ServerID, f.ServerID)
		}
		byID[f.ServerID] = f
	}
	return byID
}

// testCompileFirewallsIn: a server with two firewalls gets the union of
// their in rules with parsed ports and IPv4 prefixes, its addresses are its
// public IPv4 plus the floating IP routed to it; a server without firewall
// has no filter; a rule with only IPv6 prefixes is dropped.
func testCompileFirewallsIn(t *testing.T) {
	cfg := agentapi.Config{
		Servers: []agentapi.Server{
			{ID: 10, PublicIPv4: addr("198.19.0.10"), FirewallIDs: []int64{1, 2}},
			{ID: 11, PublicIPv4: addr("198.19.0.11")},
		},
		FloatingIPs: []agentapi.FloatingIP{
			{IP: addr("198.19.1.1"), Target: addr("198.19.0.10")},
			{IP: addr("198.19.1.2"), Target: addr("198.19.0.11")},
			{IP: addr("198.19.1.3")},
		},
		Firewalls: []agentapi.Firewall{
			{ID: 1, Rules: []agentapi.Rule{
				{Direction: "in", Protocol: "tcp", Port: "80", SourceIPs: pfx("0.0.0.0/0", "::/0")},
				{Direction: "in", Protocol: "icmp", SourceIPs: pfx("192.0.2.0/24")},
			}},
			{ID: 2, Rules: []agentapi.Rule{
				{Direction: "in", Protocol: "udp", Port: "1000-2000", SourceIPs: pfx("10.0.0.0/8", "2001:db8::/32")},
				{Direction: "in", Protocol: "tcp", Port: "any", SourceIPs: pfx("203.0.113.7/32")},
				{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: pfx("2001:db8::/32", "::/0")},
			}},
		},
	}
	got := compile(t, cfg)

	if f, ok := got[11]; ok {
		t.Errorf("server without firewall has a filter: %+v", f)
	}
	f, ok := got[10]
	if !ok {
		t.Fatalf("server 10 with two firewalls has no filter; got %+v", got)
	}
	if want := []string{"198.19.0.10", "198.19.1.1"}; !slices.Equal(addrKeys(f.Addrs), want) {
		t.Errorf("Addrs = %v, want %v", addrKeys(f.Addrs), want)
	}
	want := ruleKeys([]agent.FilterRule{
		{Protocol: protoTCP, FromPort: 80, ToPort: 80, Prefixes: pfx("0.0.0.0/0")},
		{Protocol: protoICMP, Prefixes: pfx("192.0.2.0/24")},
		{Protocol: protoUDP, FromPort: 1000, ToPort: 2000, Prefixes: pfx("10.0.0.0/8")},
		{Protocol: protoTCP, FromPort: 0, ToPort: 0, Prefixes: pfx("203.0.113.7/32")},
	})
	if g := ruleKeys(f.In); !slices.Equal(g, want) {
		t.Errorf("In =\n%s\nwant\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
	for _, r := range f.In {
		if len(r.Prefixes) == 0 {
			t.Errorf("rule without prefixes kept (would allow all sources): %+v", r)
		}
		for _, p := range r.Prefixes {
			if !p.Addr().Is4() {
				t.Errorf("IPv6 prefix %s kept in %+v", p, r)
			}
		}
	}
	if f.RestrictOut || len(f.Out) != 0 {
		t.Errorf("RestrictOut = %v, Out = %v; want false and none without out rules", f.RestrictOut, f.Out)
	}
}

// testCompileFirewallsOut: without any out rule RestrictOut is false; when
// one of the server's firewalls has an out rule it is true and Out holds the
// union of the out rules of all its firewalls.
func testCompileFirewallsOut(t *testing.T) {
	cfg := agentapi.Config{
		Servers: []agentapi.Server{
			{ID: 20, PublicIPv4: addr("198.19.0.20"), FirewallIDs: []int64{1}},
			{ID: 21, PublicIPv4: addr("198.19.0.21"), FirewallIDs: []int64{1, 2, 3}},
		},
		Firewalls: []agentapi.Firewall{
			{ID: 1, Rules: []agentapi.Rule{
				{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: pfx("0.0.0.0/0")},
			}},
			{ID: 2, Rules: []agentapi.Rule{
				{Direction: "out", Protocol: "tcp", Port: "443", DestinationIPs: pfx("0.0.0.0/0")},
			}},
			{ID: 3, Rules: []agentapi.Rule{
				{Direction: "out", Protocol: "udp", Port: "53", DestinationIPs: pfx("1.1.1.1/32")},
			}},
		},
	}
	got := compile(t, cfg)

	open, ok := got[20]
	if !ok {
		t.Fatalf("server 20 has no filter; got %+v", got)
	}
	if open.RestrictOut || len(open.Out) != 0 {
		t.Errorf("server without out rules: RestrictOut = %v, Out = %v; want false and none", open.RestrictOut, open.Out)
	}

	restricted, ok := got[21]
	if !ok {
		t.Fatalf("server 21 has no filter; got %+v", got)
	}
	if !restricted.RestrictOut {
		t.Error("server with an out rule: RestrictOut = false, want true")
	}
	want := ruleKeys([]agent.FilterRule{
		{Protocol: protoTCP, FromPort: 443, ToPort: 443, Prefixes: pfx("0.0.0.0/0")},
		{Protocol: protoUDP, FromPort: 53, ToPort: 53, Prefixes: pfx("1.1.1.1/32")},
	})
	if g := ruleKeys(restricted.Out); !slices.Equal(g, want) {
		t.Errorf("Out =\n%s\nwant\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
	if want := ruleKeys([]agent.FilterRule{{Protocol: protoTCP, FromPort: 22, ToPort: 22, Prefixes: pfx("0.0.0.0/0")}}); !slices.Equal(ruleKeys(restricted.In), want) {
		t.Errorf("In = %v, want %v", ruleKeys(restricted.In), want)
	}
}

// testCompileFirewallsPrivate: a firewalled server that is also a member of
// a private network is filtered on its public IPv4 and floating IP only; a
// server only in a private network gets no filter.
func testCompileFirewallsPrivate(t *testing.T) {
	cfg := agentapi.Config{
		Servers: []agentapi.Server{
			{ID: 30, PublicIPv4: addr("198.19.0.30"), FirewallIDs: []int64{1}},
			{ID: 31, FirewallIDs: []int64{1}},
		},
		FloatingIPs: []agentapi.FloatingIP{
			{IP: addr("198.19.1.30"), Target: addr("198.19.0.30")},
		},
		Networks: []agentapi.Network{{
			ID:      5,
			VLAN:    2,
			IPRange: netip.MustParsePrefix("10.0.0.0/16"),
			Gateway: addr("10.0.0.1"),
			Members: []agentapi.Member{
				{ServerID: 30, IP: addr("10.0.0.2"), AliasIPs: []netip.Addr{addr("10.0.0.5")}},
				{ServerID: 31, IP: addr("10.0.0.3")},
			},
		}},
		Firewalls: []agentapi.Firewall{
			{ID: 1, Rules: []agentapi.Rule{
				{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: pfx("0.0.0.0/0")},
			}},
		},
	}
	got := compile(t, cfg)

	f, ok := got[30]
	if !ok {
		t.Fatalf("server 30 has no filter; got %+v", got)
	}
	if want := []string{"198.19.0.30", "198.19.1.30"}; !slices.Equal(addrKeys(f.Addrs), want) {
		t.Errorf("Addrs = %v, want only the public IPv4 and floating IP %v", addrKeys(f.Addrs), want)
	}
	if f, ok := got[31]; ok {
		t.Errorf("server only in a private network has a filter: %+v", f)
	}
	if len(got) != 1 {
		t.Errorf("got %d filters, want 1: %+v", len(got), got)
	}
}
