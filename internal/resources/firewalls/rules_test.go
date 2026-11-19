package firewalls_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resources/firewalls"
)

// testRules is FW-01/rules: CheckRules accepts Hetzner's rule shapes,
// normalises them and refuses each invalid field.
func testRules(t *testing.T) {
	t.Run("accepts", func(t *testing.T) {
		web := ptr("web")
		given := []firewalls.Rule{
			{Description: web, Direction: "in", Protocol: "tcp", Port: ptr("80"), SourceIPs: []string{"10.0.0.0/8", "1.2.3.0/24", "2001:DB8:0:0::/32"}},
			{Direction: "in", Protocol: "udp", Port: ptr("1000-2000"), SourceIPs: []string{"0.0.0.0/0"}},
			{Direction: "in", Protocol: "tcp", Port: ptr("any"), SourceIPs: []string{"::/0"}},
			{Direction: "out", Protocol: "icmp", DestinationIPs: []string{"192.168.1.0/24"}},
			{Direction: "out", Protocol: "esp", DestinationIPs: []string{"0.0.0.0/0", "fe80:0000::1/128"}},
			{Direction: "in", Protocol: "gre", SourceIPs: []string{"10.1.2.3/32"}},
		}
		want := []firewalls.Rule{
			{Description: web, Direction: "in", Protocol: "tcp", Port: ptr("80"), SourceIPs: []string{"10.0.0.0/8", "1.2.3.0/24", "2001:db8::/32"}, DestinationIPs: []string{}},
			inRule("udp", ptr("1000-2000"), "0.0.0.0/0"),
			inRule("tcp", ptr("any"), "::/0"),
			outRule("icmp", nil, "192.168.1.0/24"),
			outRule("esp", nil, "0.0.0.0/0", "fe80::1/128"),
			inRule("gre", nil, "10.1.2.3/32"),
		}
		got, err := firewalls.CheckRules(given)
		if err != nil {
			t.Fatalf("CheckRules: %v, want the rules accepted", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CheckRules =\n%s\nwant\n%s", show(got), show(want))
		}
	})

	tcp := func(port string) firewalls.Rule {
		return firewalls.Rule{Direction: "in", Protocol: "tcp", Port: ptr(port), SourceIPs: []string{"0.0.0.0/0"}}
	}
	src := func(ips ...string) firewalls.Rule {
		return firewalls.Rule{Direction: "in", Protocol: "tcp", Port: ptr("22"), SourceIPs: ips}
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.%d.0/24", i)
	}
	valid := tcp("443")
	cases := []struct {
		name  string
		rule  firewalls.Rule
		rules []firewalls.Rule // replaces [valid, rule] when set
		field string
	}{
		{name: "unknown direction", rule: firewalls.Rule{Direction: "both", Protocol: "tcp", Port: ptr("80"), SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].direction"},
		{name: "unknown protocol", rule: firewalls.Rule{Direction: "in", Protocol: "sctp", Port: ptr("80"), SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].protocol"},
		{name: "tcp without port", rule: firewalls.Rule{Direction: "in", Protocol: "tcp", SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].port"},
		{name: "udp without port", rule: firewalls.Rule{Direction: "in", Protocol: "udp", SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].port"},
		{name: "port on icmp", rule: firewalls.Rule{Direction: "in", Protocol: "icmp", Port: ptr("80"), SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].port"},
		{name: "port 0", rule: tcp("0"), field: "rules[1].port"},
		{name: "port 65536", rule: tcp("65536"), field: "rules[1].port"},
		{name: "reversed range", rule: tcp("2000-1000"), field: "rules[1].port"},
		{name: "port 8a", rule: tcp("8a"), field: "rules[1].port"},
		{name: "in without source_ips", rule: firewalls.Rule{Direction: "in", Protocol: "tcp", Port: ptr("80"), DestinationIPs: []string{"0.0.0.0/0"}}, field: "rules[1].source_ips"},
		{name: "out without destination_ips", rule: firewalls.Rule{Direction: "out", Protocol: "tcp", Port: ptr("80"), SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].destination_ips"},
		{name: "address without mask", rule: src("10.0.0.1"), field: "rules[1].source_ips"},
		{name: "host bits set", rule: src("10.0.0.5/24"), field: "rules[1].source_ips"},
		{name: "101 IPs", rule: src(many...), field: "rules[1].source_ips"},
		{name: "51 rules", rules: repeat(valid, 51), field: "rules"},
		{name: "description of 256 characters", rule: firewalls.Rule{Description: ptr(strings.Repeat("d", 256)), Direction: "in", Protocol: "tcp", Port: ptr("80"), SourceIPs: []string{"0.0.0.0/0"}}, field: "rules[1].description"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules := c.rules
			if rules == nil {
				rules = []firewalls.Rule{valid, c.rule}
			}
			_, err := firewalls.CheckRules(rules)
			var fe *api.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("CheckRules: %v, want a field error on %s", err, c.field)
			}
			if (fe.Code != "" && fe.Code != "invalid_input") || fe.Field != c.field {
				t.Errorf("CheckRules: code %q field %q, want invalid_input on %s", fe.Code, fe.Field, c.field)
			}
		})
	}
}

func repeat(r firewalls.Rule, n int) []firewalls.Rule {
	out := make([]firewalls.Rule, n)
	for i := range out {
		out[i] = r
	}
	return out
}

func show(rules []firewalls.Rule) string {
	var b strings.Builder
	for _, r := range rules {
		port, desc := "<nil>", "<nil>"
		if r.Port != nil {
			port = *r.Port
		}
		if r.Description != nil {
			desc = *r.Description
		}
		fmt.Fprintf(&b, "  %s %s port=%s desc=%s src=%#v dst=%#v\n", r.Direction, r.Protocol, port, desc, r.SourceIPs, r.DestinationIPs)
	}
	return b.String()
}
