package dnszone_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/dnszone"
)

const zone = "example.com"

func TestDNSZone(t *testing.T) {
	t.Run("DNS-01/names", testNames)
	t.Run("DNS-01/records", testRecords)
	t.Run("DNS-01/serial", testSerial)
	t.Run("DNS-01/zonefile", testZonefile)
}

// fieldError returns err as *dnszone.FieldError and fails the test if it is
// none or not an invalid_input error.
func fieldError(t *testing.T, err error) *dnszone.FieldError {
	t.Helper()
	if err == nil {
		t.Fatal("got nil, want a *dnszone.FieldError")
	}
	var fe *dnszone.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v (%T) is no *dnszone.FieldError", err, err)
	}
	if fe.Code != "" && fe.Code != "invalid_input" {
		t.Errorf("code %q, want invalid_input", fe.Code)
	}
	return fe
}

func ttl(v int) *int { return &v }

func recs(values ...string) []dnszone.Record {
	out := make([]dnszone.Record, len(values))
	for i, v := range values {
		out[i] = dnszone.Record{Value: v}
	}
	return out
}

// validSets returns one valid RRSet of each of the 16 types of zone
// example.com, with values in presentation format as miekg/dns prints them,
// targets inside the zone relative and outside it absolute.
func validSets() []dnszone.RRSet {
	hex := strings.Repeat("ab", 32)
	return []dnszone.RRSet{
		{Name: "@", Type: "SOA", Records: recs("hydrogen.ns.hetzner.com. dns.hetzner.com. 2024010100 86400 10800 3600000 3600")},
		{Name: "www", Type: "A", TTL: ttl(300), Records: recs("192.0.2.1", "192.0.2.2")},
		{Name: "www", Type: "AAAA", Records: recs("2001:db8::1")},
		{Name: "@", Type: "CAA", Records: recs(`0 issue "letsencrypt.org"`)},
		{Name: "blog", Type: "CNAME", Records: recs("www")},
		{Name: "sub", Type: "DS", Records: recs("12345 13 2 " + strings.ToUpper(hex))},
		{Name: "host", Type: "HINFO", Records: recs(`"Intel" "Ubuntu"`)},
		{Name: "@", Type: "HTTPS", Records: recs(`1 . alpn="h3,h2" ipv4hint="203.0.113.1"`)},
		{Name: "@", Type: "MX", Records: recs("10 mail", "20 mail.example.net.")},
		{Name: "sub", Type: "NS", Records: recs("ns1.example.net.")},
		{Name: "ptr", Type: "PTR", Records: recs("host.example.net.")},
		{Name: "@", Type: "RP", Records: recs("admin.example.net. txt.example.net.")},
		{Name: "_sip._tcp", Type: "SRV", Records: recs("10 5 5060 sip.example.net.")},
		{Name: "_svc", Type: "SVCB", Records: recs(`1 svc.example.net. port="8443" alpn="h2"`)},
		{Name: "_443._tcp.www", Type: "TLSA", Records: recs("3 1 1 " + hex)},
		{Name: "@", Type: "TXT", TTL: ttl(600), Records: recs(`"v=spf1 " "-all"`)},
	}
}

func testNames(t *testing.T) {
	for _, name := range []string{
		"example.com", "example.co.uk", "xn--mnchen-3ya.de",
		"lab.internal", "x.test", "x.home.arpa",
	} {
		t.Run("valid "+name, func(t *testing.T) {
			if err := dnszone.CheckZoneName(name); err != nil {
				t.Errorf("CheckZoneName(%q): %v, want nil", name, err)
			}
		})
	}

	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("b", 60) + ".com"
	if len(long) != 256 {
		t.Fatalf("long name has %d characters, want 256", len(long))
	}
	for _, c := range []struct{ desc, name string }{
		{"subdomain", "sub.example.com"},
		{"public suffix", "com"},
		{"upper case", "Example.com"},
		{"trailing dot", "example.com."},
		{"256 characters", long},
		{"private public suffix", "foo.github.io"},
		{"unknown tld", "example.unknowntld"},
		{"hetzner.internal", "hetzner.internal"},
		{"subdomain under internal", "a.lab.internal"},
		{"local", "x.local"},
		{"reverse zone", "1.168.192.in-addr.arpa"},
	} {
		t.Run("invalid "+c.desc, func(t *testing.T) {
			fe := fieldError(t, dnszone.CheckZoneName(c.name))
			if fe.Field != "name" {
				t.Errorf("CheckZoneName(%q): field %q, want name", c.name, fe.Field)
			}
		})
	}

	for _, c := range []struct {
		zone string
		want bool
	}{
		{"lab.internal", true},
		{"x.test", true},
		{"x.home.arpa", true},
		{"example.com", false},
		{"example.co.uk", false},
		{"xn--mnchen-3ya.de", false},
	} {
		t.Run("private use "+c.zone, func(t *testing.T) {
			if got := dnszone.PrivateUse(c.zone); got != c.want {
				t.Errorf("PrivateUse(%q) = %v, want %v", c.zone, got, c.want)
			}
		})
	}
}

func testRecords(t *testing.T) {
	sets := validSets()
	types := map[string]bool{}
	for _, set := range sets {
		types[set.Type] = true
		t.Run("valid "+set.Type, func(t *testing.T) {
			if err := dnszone.CheckRRSet(zone, set); err != nil {
				t.Errorf("CheckRRSet(%+v): %v, want nil", set, err)
			}
		})
	}
	if len(types) != 16 {
		t.Fatalf("valid sets cover %d types, want 16", len(types))
	}
	t.Run("valid together", func(t *testing.T) {
		if err := dnszone.CheckConflicts(sets); err != nil {
			t.Errorf("CheckConflicts(valid sets): %v, want nil", err)
		}
	})

	a := func(name string, t *int, values ...string) dnszone.RRSet {
		return dnszone.RRSet{Name: name, Type: "A", TTL: t, Records: recs(values...)}
	}
	many := make([]string, 51)
	for i := range many {
		many[i] = "192.0.2." + strconv.Itoa(i+1)
	}
	for _, c := range []struct {
		desc  string
		set   dnszone.RRSet
		field string
	}{
		{"unknown type", dnszone.RRSet{Name: "www", Type: "FOO", Records: recs("x")}, "type"},
		{"upper case name", a("WWW", nil, "192.0.2.1"), "name"},
		{"name ending in a dot", a("www.", nil, "192.0.2.1"), "name"},
		{"name ending in the zone name", a("www.example.com", nil, "192.0.2.1"), "name"},
		{"ttl 59", a("www", ttl(59), "192.0.2.1"), "ttl"},
		{"empty records", a("www", nil), "records"},
		{"duplicate values", a("www", nil, "192.0.2.1", "192.0.2.1"), "records"},
		{"51 records", a("www", nil, many...), "records"},
		{"A with IPv6", a("www", nil, "2001:db8::1"), "records[0].value"},
		{"MX without priority", dnszone.RRSet{Name: "@", Type: "MX", Records: recs("mail.example.net.")}, "records[0].value"},
		{"unquoted TXT", dnszone.RRSet{Name: "@", Type: "TXT", Records: recs("hello world")}, "records[0].value"},
		{"TXT string of 256", dnszone.RRSet{Name: "@", Type: "TXT", Records: recs(`"` + strings.Repeat("a", 256) + `"`)}, "records[0].value"},
	} {
		t.Run("invalid "+c.desc, func(t *testing.T) {
			fe := fieldError(t, dnszone.CheckRRSet(zone, c.set))
			if fe.Field != c.field {
				t.Errorf("field %q, want %q (message %q)", fe.Field, c.field, fe.Message)
			}
		})
	}

	cname := func(name string, values ...string) dnszone.RRSet {
		return dnszone.RRSet{Name: name, Type: "CNAME", Records: recs(values...)}
	}
	for _, c := range []struct {
		desc   string
		sets   []dnszone.RRSet
		prefix string
	}{
		{"CNAME beside A", []dnszone.RRSet{a("www", nil, "192.0.2.1"), cname("www", "blog")}, "(www, CNAME) conflicts with (www, A)"},
		{"two CNAME records", []dnszone.RRSet{cname("blog", "www", "web")}, "(blog, CNAME) conflicts with (blog, "},
		{"CNAME at apex", append(dnszone.DefaultSets(zone, 2024010100), cname("@", "www")), "(@, CNAME) conflicts with (@, "},
	} {
		t.Run("conflict "+c.desc, func(t *testing.T) {
			fe := fieldError(t, dnszone.CheckConflicts(c.sets))
			if fe.Field != "type" {
				t.Errorf("field %q, want type", fe.Field)
			}
			if !strings.HasPrefix(fe.Message, c.prefix) {
				t.Errorf("message %q, want it to start with %q", fe.Message, c.prefix)
			}
		})
	}
}

func testSerial(t *testing.T) {
	day := time.Date(2024, 1, 2, 15, 4, 5, 0, time.UTC)
	for _, c := range []struct {
		desc string
		old  uint32
		want uint32
	}{
		{"new zone", 0, 2024010200},
		{"new day", 2024010105, 2024010200},
		{"same day", 2024010200, 2024010201},
		{"same day again", 2024010207, 2024010208},
	} {
		t.Run(c.desc, func(t *testing.T) {
			if got := dnszone.NextSerial(c.old, day); got != c.want {
				t.Errorf("NextSerial(%d, %s) = %d, want %d", c.old, day, got, c.want)
			}
		})
	}

	t.Run("default sets", func(t *testing.T) {
		sets := dnszone.DefaultSets(zone, 2024010200)
		var soa, ns *dnszone.RRSet
		for i := range sets {
			switch {
			case sets[i].Name == "@" && sets[i].Type == "SOA":
				soa = &sets[i]
			case sets[i].Name == "@" && sets[i].Type == "NS":
				ns = &sets[i]
			default:
				t.Errorf("unexpected default set %s %s", sets[i].Name, sets[i].Type)
			}
		}
		if soa == nil || len(soa.Records) != 1 {
			t.Fatalf("SOA set %+v, want one SOA record at @", soa)
		}
		if f := strings.Fields(soa.Records[0].Value); len(f) != 7 || f[2] != "2024010200" {
			t.Errorf("SOA value %q, want 7 fields with serial 2024010200", soa.Records[0].Value)
		}
		if ns == nil {
			t.Fatal("no NS set at @")
		}
		var got []string
		for _, r := range ns.Records {
			got = append(got, strings.TrimSuffix(r.Value, "."))
		}
		if strings.Join(got, ",") != strings.Join(dnszone.Nameservers, ",") {
			t.Errorf("NS records %v, want %v", got, dnszone.Nameservers)
		}
		if len(dnszone.Nameservers) != 3 {
			t.Errorf("Nameservers %v, want three", dnszone.Nameservers)
		}
	})
}
