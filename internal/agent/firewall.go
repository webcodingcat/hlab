package agent

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// IP protocol numbers of the firewall protocols.
const (
	protoICMP uint8 = 1
	protoTCP  uint8 = 6
	protoUDP  uint8 = 17
	protoGRE  uint8 = 47
	protoESP  uint8 = 50
)

var firewallProtocols = map[string]uint8{
	"tcp":  protoTCP,
	"udp":  protoUDP,
	"icmp": protoICMP,
	"esp":  protoESP,
	"gre":  protoGRE,
}

// FilterRule is one allow rule of a server filter.
type FilterRule struct {
	Protocol         uint8          // IPPROTO_TCP, _UDP, _ICMP, _ESP, _GRE
	FromPort, ToPort uint16         // tcp/udp; 0,0 = any port
	Prefixes         []netip.Prefix // IPv4 only, masked; a rule whose prefixes were all IPv6 is dropped
}

// ServerFilter is what the firewall applier enforces for one server.
type ServerFilter struct {
	ServerID    int64
	Addrs       []netip.Addr // public IPv4 plus the floating IPs routed to it
	In, Out     []FilterRule // union of the rules of all its firewalls
	RestrictOut bool         // some applied firewall has an "out" rule
}

// CompileFirewalls returns one filter per server with a public IPv4 and at
// least one firewall, ordered by server ID. Networks never contribute.
// Unknown firewall IDs are ignored.
func CompileFirewalls(cfg agentapi.Config) ([]ServerFilter, error) {
	byID := make(map[int64]agentapi.Firewall, len(cfg.Firewalls))
	for _, fw := range cfg.Firewalls {
		byID[fw.ID] = fw
	}
	var out []ServerFilter
	for _, s := range cfg.Servers {
		if !s.PublicIPv4.Is4() || len(s.FirewallIDs) == 0 {
			continue
		}
		f := ServerFilter{ServerID: s.ID, Addrs: []netip.Addr{s.PublicIPv4}}
		for _, fip := range cfg.FloatingIPs {
			if fip.IP.Is4() && fip.Target == s.PublicIPv4 {
				f.Addrs = append(f.Addrs, fip.IP)
			}
		}
		ids := slices.Clone(s.FirewallIDs)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		for _, id := range ids {
			fw, ok := byID[id]
			if !ok {
				continue
			}
			for _, r := range fw.Rules {
				if r.Direction == "out" {
					f.RestrictOut = true
				}
				fr, ok, err := compileRule(r)
				if err != nil {
					return nil, fmt.Errorf("firewall %d: %w", fw.ID, err)
				}
				if !ok {
					continue
				}
				switch r.Direction {
				case "in":
					f.In = append(f.In, fr)
				case "out":
					f.Out = append(f.Out, fr)
				}
			}
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b ServerFilter) int {
		switch {
		case a.ServerID < b.ServerID:
			return -1
		case a.ServerID > b.ServerID:
			return 1
		}
		return 0
	})
	return out, nil
}

// compileRule converts one rule; ok is false when the rule has no IPv4
// prefix left and so matches nothing on the appliance.
func compileRule(r agentapi.Rule) (FilterRule, bool, error) {
	proto, known := firewallProtocols[r.Protocol]
	if !known {
		return FilterRule{}, false, fmt.Errorf("unknown protocol %q", r.Protocol)
	}
	fr := FilterRule{Protocol: proto}
	if proto == protoTCP || proto == protoUDP {
		from, to, err := parsePorts(r.Port)
		if err != nil {
			return FilterRule{}, false, err
		}
		fr.FromPort, fr.ToPort = from, to
	}
	ips := r.SourceIPs
	if r.Direction == "out" {
		ips = r.DestinationIPs
	}
	for _, p := range ips {
		if p.Addr().Is4() {
			fr.Prefixes = append(fr.Prefixes, p.Masked())
		}
	}
	return fr, len(fr.Prefixes) > 0, nil
}

// parsePorts parses "any", "N" or "A-B"; any gives 0, 0.
func parsePorts(port string) (uint16, uint16, error) {
	if port == "any" {
		return 0, 0, nil
	}
	lo, hi, isRange := strings.Cut(port, "-")
	if !isRange {
		hi = lo
	}
	from, err1 := strconv.ParseUint(lo, 10, 16)
	to, err2 := strconv.ParseUint(hi, 10, 16)
	if err1 != nil || err2 != nil || from == 0 || from > to {
		return 0, 0, fmt.Errorf("invalid port %q", port)
	}
	return uint16(from), uint16(to), nil
}
