package lab

import (
	"context"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/state"
)

// ChangeFirewall is the steps of a firewall's actions (set_rules,
// apply_to_resources, remove_from_resources): the plan has changed the
// record, and the appliance gets the ruleset with it.
func (m *Machine) ChangeFirewall(ctx context.Context) error {
	return m.UpdatePictures(ctx)
}

// FirewallServers returns the servers a firewall applies to: those named
// directly, then those its label selectors match, each once, in that
// order.
func FirewallServers(firewall *state.Firewall) []int64 {
	var servers []int64
	for _, target := range firewall.AppliedTo {
		for _, id := range append([]int64{target.ServerID}, target.Servers...) {
			if id != 0 && !slices.Contains(servers, id) {
				servers = append(servers, id)
			}
		}
	}
	return servers
}

// firewallFacts returns what the ruleset needs of the firewalls that
// apply to a server. The API has checked their rules, so an address that
// does not parse cannot occur; it would be left out.
func firewallFacts(tx *state.Tx) []picture.Firewall {
	var facts []picture.Firewall
	for _, firewall := range state.All(tx, state.Firewalls) {
		servers := FirewallServers(firewall)
		if len(servers) == 0 {
			continue
		}
		fact := picture.Firewall{ID: firewall.ID, ServerIDs: servers}
		for _, rule := range firewall.Rules {
			addresses := rule.SourceIPs
			if rule.Direction == picture.DirectionOut {
				addresses = rule.DestinationIPs
			}
			fact.Rules = append(fact.Rules, picture.FirewallRule{
				Direction: rule.Direction,
				Protocol:  rule.Protocol,
				Port:      rule.Port,
				Addresses: parsePrefixes(addresses),
			})
		}
		facts = append(facts, fact)
	}
	return facts
}

// parsePrefixes parses CIDRs and leaves out what does not parse.
func parsePrefixes(cidrs []string) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, cidr := range cidrs {
		if prefix, err := netip.ParsePrefix(cidr); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}
