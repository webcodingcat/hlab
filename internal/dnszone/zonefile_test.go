package dnszone_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/dnszone"
)

const (
	soaLine = "@\tIN\tSOA\thydrogen.ns.hetzner.com. dns.hetzner.com. 2024010100 86400 10800 3600000 3600\n"
	nsLines = "@\tIN\t10800\tNS\thydrogen.ns.hetzner.com.\n" +
		"@\tIN\t10800\tNS\toxygen.ns.hetzner.com.\n" +
		"@\tIN\t10800\tNS\thelium.ns.hetzner.de.\n"
)

// effectiveTTL is the TTL a set has in a zone with default zoneTTL.
func effectiveTTL(set dnszone.RRSet, zoneTTL int) int {
	if set.TTL == nil {
		return zoneTTL
	}
	return *set.TTL
}

// describe renders sets with their effective TTLs for comparison.
func describe(sets []dnszone.RRSet, zoneTTL int) string {
	var b strings.Builder
	for _, s := range sets {
		fmt.Fprintf(&b, "%s %s %d", s.Name, s.Type, effectiveTTL(s, zoneTTL))
		for _, r := range s.Records {
			fmt.Fprintf(&b, " [%s ;%s]", r.Value, r.Comment)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func findSet(sets []dnszone.RRSet, name, typ string) *dnszone.RRSet {
	for i := range sets {
		if sets[i].Name == name && sets[i].Type == typ {
			return &sets[i]
		}
	}
	return nil
}

func values(set *dnszone.RRSet) []string {
	var out []string
	for _, r := range set.Records {
		out = append(out, r.Value)
	}
	return out
}

func testZonefile(t *testing.T) {
	t.Run("parse", func(t *testing.T) {
		text := "$ORIGIN example.com.\n" +
			"$TTL 3600\n" +
			"; a comment line\n" +
			soaLine +
			"@\tIN\t10800\tNS\thydrogen.ns.hetzner.com. ; Some comment.\n" +
			"@\t10800\tIN\tNS\toxygen.ns.hetzner.com.\n" +
			"example.com.\tIN\t10800\tNS\thelium.ns.hetzner.de.\n" +
			"www\t300\tIN\tA\t192.0.2.1\n" +
			"www.example.com.\tIN\t300\tA\t192.0.2.2 ; second\n" +
			"mail\tIN\tMX\t10 mail\n"
		zoneTTL, sets, err := dnszone.ParseZonefile(zone, text)
		if err != nil {
			t.Fatalf("ParseZonefile: %v", err)
		}
		if zoneTTL != 3600 {
			t.Errorf("ttl %d, want 3600", zoneTTL)
		}
		for _, c := range []struct {
			name, typ string
			ttl       int
			values    []string
		}{
			{"@", "SOA", 3600, []string{"hydrogen.ns.hetzner.com. dns.hetzner.com. 2024010100 86400 10800 3600000 3600"}},
			{"@", "NS", 10800, []string{"hydrogen.ns.hetzner.com.", "oxygen.ns.hetzner.com.", "helium.ns.hetzner.de."}},
			{"www", "A", 300, []string{"192.0.2.1", "192.0.2.2"}},
			{"mail", "MX", 3600, []string{"10 mail"}},
		} {
			set := findSet(sets, c.name, c.typ)
			if set == nil {
				t.Errorf("no set %s %s in %s", c.name, c.typ, describe(sets, zoneTTL))
				continue
			}
			if got := effectiveTTL(*set, zoneTTL); got != c.ttl {
				t.Errorf("%s %s: ttl %d, want %d", c.name, c.typ, got, c.ttl)
			}
			if got := values(set); strings.Join(got, "|") != strings.Join(c.values, "|") {
				t.Errorf("%s %s: values %q, want %q", c.name, c.typ, got, c.values)
			}
			for _, r := range set.Records {
				if r.Comment != "" {
					t.Errorf("%s %s: comment %q kept, want it dropped", c.name, c.typ, r.Comment)
				}
			}
		}
		if len(sets) != 4 {
			t.Errorf("got %d sets, want 4:\n%s", len(sets), describe(sets, zoneTTL))
		}
	})

	head := "$ORIGIN example.com.\n$TTL 3600\n"
	for _, c := range []struct {
		desc string
		text string
		line int // 0: no line to name
	}{
		{"wrong origin", "$ORIGIN example.org.\n$TTL 3600\n" + soaLine + nsLines, 1},
		{"non-IN class", head + soaLine + nsLines + "www\t300\tCH\tA\t192.0.2.1\n", 7},
		{"missing SOA", head + nsLines, 0},
		{"missing Hetzner NS", head + soaLine +
			"@\tIN\t10800\tNS\thydrogen.ns.hetzner.com.\n" +
			"@\tIN\t10800\tNS\tns1.example.net.\n", 0},
		{"unquoted TXT", head + soaLine + nsLines + "txt\tIN\tTXT\thello\n", 7},
	} {
		t.Run("invalid "+c.desc, func(t *testing.T) {
			_, _, err := dnszone.ParseZonefile(zone, c.text)
			fe := fieldError(t, err)
			if fe.Field != "zonefile" {
				t.Errorf("field %q, want zonefile (message %q)", fe.Field, fe.Message)
			}
			if c.line > 0 {
				re := regexp.MustCompile(fmt.Sprintf(`(?i)line:? %d\b`, c.line))
				if !re.MatchString(fe.Message) {
					t.Errorf("message %q does not name line %d", fe.Message, c.line)
				}
			}
		})
	}

	t.Run("round trip", func(t *testing.T) {
		sets := dnszone.DefaultSets(zone, 2024010100)
		for _, s := range validSets() {
			if s.Type != "SOA" {
				sets = append(sets, s)
			}
		}
		text := dnszone.FormatZonefile(zone, 3600, sets)
		zoneTTL, got, err := dnszone.ParseZonefile(zone, text)
		if err != nil {
			t.Fatalf("ParseZonefile of FormatZonefile output: %v\n%s", err, text)
		}
		if zoneTTL != 3600 {
			t.Errorf("ttl %d, want 3600", zoneTTL)
		}
		for _, s := range sets {
			g := findSet(got, s.Name, s.Type)
			if g == nil {
				t.Errorf("set %s %s missing after round trip:\n%s", s.Name, s.Type, text)
				continue
			}
			want := describe([]dnszone.RRSet{s}, 3600)
			if d := describe([]dnszone.RRSet{*g}, zoneTTL); d != want {
				t.Errorf("round trip changed set:\n got %s want %s", d, want)
			}
		}
		if len(got) != len(sets) {
			t.Errorf("got %d sets, want %d:\n%s", len(got), len(sets), describe(got, zoneTTL))
		}
	})
}
