package picture

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Firewall is what the ruleset needs of a Cloud Firewall: its rules and
// the servers it applies to, directly or through a label selector (the
// lab resolves the selectors).
type Firewall struct {
	ID        int64
	Rules     []FirewallRule
	ServerIDs []int64
}

// FirewallRule is one rule of a firewall, as the API checked it.
type FirewallRule struct {
	Direction string // "in" or "out"
	Protocol  string // "tcp", "udp", "icmp", "esp" or "gre"
	Port      string // tcp and udp: "80", "80-85" or "any"; "" for the others
	// Addresses are the rule's source_ips (in) or destination_ips (out).
	Addresses []netip.Prefix
}

// The directions of a firewall rule, as Hetzner names them.
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

// firewallValues are the firewalls' part of the ruleset template.
type firewallValues struct {
	// In4, In6, Out4 and Out6 are the verdict maps' elements: a server's
	// addresses (its Primary IPs and its Floating IPs) to its chain.
	In4, In6, Out4, Out6 []string
	Servers              []chainValues
	Firewalls            []chainValues
}

// chainValues is one chain of the ruleset with its rules.
type chainValues struct {
	Name  string
	Rules []string
}

// firewallParts returns the ruleset's chains and maps of the firewalls:
// one chain per server and direction, which jumps to one chain per
// firewall and direction, and the maps that send a server's packets to
// its chains. A server without a firewall has no chain: everything
// passes, as on Hetzner.
func firewallParts(lab Lab) firewallValues {
	var values firewallValues
	firewalls := slices.Clone(lab.Firewalls)
	slices.SortFunc(firewalls, func(a, b Firewall) int { return cmp.Compare(a.ID, b.ID) })
	for _, firewall := range firewalls {
		for _, direction := range []string{DirectionIn, DirectionOut} {
			// a chain also for rules without a line (GRE over IPv6 only),
			// as the servers' chains jump to it
			if hasRules(firewall, direction) {
				values.Firewalls = append(values.Firewalls, chainValues{Name: firewallChain(firewall.ID, direction), Rules: firewallRules(firewall, direction)})
			}
		}
	}
	for _, server := range lab.servers() {
		var in, out []string
		protected := false
		for _, firewall := range firewalls {
			if !slices.Contains(firewall.ServerIDs, server.ID) {
				continue
			}
			// a firewall without inbound rules still closes the server
			protected = true
			if hasRules(firewall, DirectionIn) {
				in = append(in, "jump "+firewallChain(firewall.ID, DirectionIn))
			}
			if hasRules(firewall, DirectionOut) {
				out = append(out, "jump "+firewallChain(firewall.ID, DirectionOut))
			}
		}
		if !protected {
			continue
		}
		addresses := serverAddresses(lab, server)
		values.Servers = append(values.Servers, serverChain(server.ID, DirectionIn, in))
		addMapElements(&values.In4, &values.In6, addresses, serverChainName(server.ID, DirectionIn))
		// without an outbound rule, outbound traffic is open (Hetzner)
		if len(out) > 0 {
			values.Servers = append(values.Servers, serverChain(server.ID, DirectionOut, out))
			addMapElements(&values.Out4, &values.Out6, addresses, serverChainName(server.ID, DirectionOut))
		}
	}
	return values
}

// hasRules reports whether a firewall has a rule of a direction.
func hasRules(firewall Firewall, direction string) bool {
	return slices.ContainsFunc(firewall.Rules, func(rule FirewallRule) bool { return rule.Direction == direction })
}

// serverChain returns a server's chain of one direction. Answers pass
// first, as Hetzner's firewalls are stateful; then the firewalls' rules;
// what none of them allows is dropped, as Hetzner drops it.
func serverChain(id int64, direction string, jumps []string) chainValues {
	rules := []string{
		"ct state established,related accept",
		// the answers of connections that ran before an update of the
		// appliance, whose conntrack starts empty: a packet with ACK and
		// without SYN opens no connection
		"tcp flags & (syn | ack) == ack accept",
	}
	rules = append(rules, jumps...)
	rules = append(rules, "drop")
	return chainValues{Name: serverChainName(id, direction), Rules: rules}
}

// serverChainName returns the name of a server's chain of one direction.
func serverChainName(id int64, direction string) string {
	return fmt.Sprintf("server_%d_%s", id, direction)
}

// firewallChain returns the name of a firewall's chain of one direction.
func firewallChain(id int64, direction string) string {
	return fmt.Sprintf("firewall_%d_%s", id, direction)
}

// serverAddresses returns a server's public addresses: its Primary IPs
// and its Floating IPs, which the firewalls filter too.
func serverAddresses(lab Lab, server Server) []netip.Prefix {
	var addresses []netip.Prefix
	if server.IPv4.IsValid() {
		addresses = append(addresses, netip.PrefixFrom(server.IPv4, 32))
	}
	if server.IPv6.IsValid() {
		addresses = append(addresses, server.IPv6.Masked())
	}
	for _, floating := range lab.floatingIPs() {
		if floating.ServerID == server.ID {
			addresses = append(addresses, floating.Prefix.Masked())
		}
	}
	return addresses
}

// addMapElements adds the elements that send addresses to a chain to the
// maps of IPv4 and IPv6.
func addMapElements(map4, map6 *[]string, addresses []netip.Prefix, chain string) {
	for _, address := range addresses {
		if address.Addr().Is4() {
			*map4 = append(*map4, address.Addr().String()+" : jump "+chain)
		} else {
			*map6 = append(*map6, address.String()+" : jump "+chain)
		}
	}
}

// firewallRules returns a firewall's rules of one direction in nft's
// words, each accepting what it matches: one line per rule and address
// family that the rule's addresses have.
func firewallRules(firewall Firewall, direction string) []string {
	side := "saddr"
	if direction == DirectionOut {
		side = "daddr"
	}
	var lines []string
	for _, rule := range firewall.Rules {
		if rule.Direction != direction {
			continue
		}
		for _, family := range []string{"ip", "ip6"} {
			var prefixes []string
			everything := false
			for _, address := range rule.Addresses {
				if address.Addr().Is4() != (family == "ip") {
					continue
				}
				prefixes = append(prefixes, address.Masked().String())
				everything = everything || address.Bits() == 0
			}
			if len(prefixes) == 0 {
				continue
			}
			// Hetzner's firewalls drop GRE over IPv6
			if rule.Protocol == "gre" && family == "ip6" {
				continue
			}
			match := family + " " + side + " { " + strings.Join(prefixes, ", ") + " }"
			if everything {
				match = "meta nfproto ipv4"
				if family == "ip6" {
					match = "meta nfproto ipv6"
				}
			}
			lines = append(lines, match+" "+protocolMatch(rule, family)+" accept")
		}
	}
	return lines
}

// protocolMatch returns what matches a rule's protocol and port in one
// address family: Hetzner's icmp means ICMPv6 for IPv6.
func protocolMatch(rule FirewallRule, family string) string {
	switch rule.Protocol {
	case "tcp", "udp":
		if rule.Port == "any" || rule.Port == "" {
			return "meta l4proto " + rule.Protocol
		}
		return rule.Protocol + " dport " + rule.Port
	case "icmp":
		if family == "ip6" {
			return "meta l4proto icmpv6"
		}
		return "meta l4proto icmp"
	default:
		return "meta l4proto " + rule.Protocol
	}
}
