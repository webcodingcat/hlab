// Package firewalls serves Hetzner's firewalls of a project: rules in
// Hetzner's shape, applied to servers directly or by label selector and
// enforced by the agent on the appliance's public interface.
package firewalls

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
)

// Limits of a firewall's rules as at Hetzner.
const (
	maxRules       = 50
	maxIPsPerRule  = 100
	maxDescription = 255
)

// Rule is one firewall rule in Hetzner's shape.
type Rule struct {
	Description    *string  `json:"description"`
	Direction      string   `json:"direction"`       // "in" | "out"
	Protocol       string   `json:"protocol"`        // tcp, udp, icmp, esp, gre
	Port           *string  `json:"port"`            // "80", "1000-2000", "any"; null for icmp/esp/gre
	SourceIPs      []string `json:"source_ips"`      // "in"; [] for "out"
	DestinationIPs []string `json:"destination_ips"` // "out"; [] for "in"
}

// CheckRules validates rules as at Hetzner and returns them normalised:
// order kept (rules and IPs), the other direction's list [], IPv6 prefixes
// in RFC 5952 form, port and description echoed as given. Errors are
// *api.FieldError with field "rules[i].<field>" (or "rules" for the count).
func CheckRules(rules []Rule) ([]Rule, error) {
	if len(rules) > maxRules {
		return nil, &api.FieldError{Field: "rules", Message: fmt.Sprintf("at most %d rules allowed", maxRules)}
	}
	out := make([]Rule, 0, len(rules))
	for i, r := range rules {
		n, err := checkRule(r)
		if err != nil {
			err.Field = fmt.Sprintf("rules[%d].%s", i, err.Field)
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// checkRule validates one rule; the error's Field is relative to the rule.
func checkRule(r Rule) (Rule, *api.FieldError) {
	if r.Description != nil && utf8.RuneCountInString(*r.Description) > maxDescription {
		return Rule{}, &api.FieldError{Field: "description", Message: fmt.Sprintf("must be at most %d characters", maxDescription)}
	}
	if r.Direction != "in" && r.Direction != "out" {
		return Rule{}, &api.FieldError{Field: "direction", Message: "must be in or out"}
	}
	switch r.Protocol {
	case "tcp", "udp":
		if r.Port == nil {
			return Rule{}, &api.FieldError{Field: "port", Message: "port is required for protocol " + r.Protocol}
		}
		if !validPort(*r.Port) {
			return Rule{}, &api.FieldError{Field: "port", Message: "must be a port 1-65535, a range lower-upper or any"}
		}
	case "icmp", "esp", "gre":
		if r.Port != nil {
			return Rule{}, &api.FieldError{Field: "port", Message: "port is not allowed for protocol " + r.Protocol}
		}
	default:
		return Rule{}, &api.FieldError{Field: "protocol", Message: "must be one of tcp, udp, icmp, esp, gre"}
	}
	n := Rule{Description: r.Description, Direction: r.Direction, Protocol: r.Protocol, Port: r.Port}
	var err *api.FieldError
	if r.Direction == "in" {
		n.SourceIPs, err = checkIPs("source_ips", r.SourceIPs)
		n.DestinationIPs = []string{}
	} else {
		n.SourceIPs = []string{}
		n.DestinationIPs, err = checkIPs("destination_ips", r.DestinationIPs)
	}
	if err != nil {
		return Rule{}, err
	}
	return n, nil
}

// validPort accepts "any", "N" or "A-B" with 1 <= A <= B <= 65535, digits only.
func validPort(p string) bool {
	if p == "any" {
		return true
	}
	lo, hi, isRange := strings.Cut(p, "-")
	if !isRange {
		hi = lo
	}
	a, okA := portNumber(lo)
	b, okB := portNumber(hi)
	return okA && okB && a <= b
}

func portNumber(s string) (int, bool) {
	if s == "" || len(s) > 5 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= 65535
}

// checkIPs validates the CIDR list of a rule's direction: non-empty, at
// most maxIPsPerRule, each with a mask and no host bits. IPv6 prefixes come
// back in RFC 5952 form.
func checkIPs(field string, ips []string) ([]string, *api.FieldError) {
	if len(ips) == 0 {
		return nil, &api.FieldError{Field: field, Message: "at least one CIDR is required"}
	}
	if len(ips) > maxIPsPerRule {
		return nil, &api.FieldError{Field: field, Message: fmt.Sprintf("at most %d CIDRs allowed", maxIPsPerRule)}
	}
	out := make([]string, 0, len(ips))
	for _, s := range ips {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, &api.FieldError{Field: field, Message: fmt.Sprintf("%q is not a CIDR", s)}
		}
		if p.Masked() != p {
			return nil, &api.FieldError{Field: field, Message: fmt.Sprintf("%q has host bits set", s)}
		}
		if p.Addr().Is4() {
			out = append(out, s)
		} else {
			out = append(out, p.String())
		}
	}
	return out, nil
}

// AgentRules converts stored rules to agentapi rules.
func AgentRules(rules []Rule) []agentapi.Rule {
	out := make([]agentapi.Rule, 0, len(rules))
	for _, r := range rules {
		a := agentapi.Rule{
			Direction:      r.Direction,
			Protocol:       r.Protocol,
			SourceIPs:      prefixes(r.SourceIPs),
			DestinationIPs: prefixes(r.DestinationIPs),
		}
		if r.Port != nil {
			a.Port = *r.Port
		}
		out = append(out, a)
	}
	return out
}

// prefixes parses stored (validated) CIDRs.
func prefixes(ips []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(ips))
	for _, s := range ips {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}
