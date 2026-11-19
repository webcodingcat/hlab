package agent_test

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentapi"
)

const testSOA = "hydrogen.ns.hetzner.com. dns.hetzner.com. 2026111900 86400 10800 3600000 3600"

// zoneSets returns the SOA and NS RRSets every zone has, followed by extra.
func zoneSets(extra ...agentapi.RRSet) []agentapi.RRSet {
	sets := []agentapi.RRSet{
		{Name: "@", Type: "SOA", TTL: 3600, Records: []string{testSOA}},
		{Name: "@", Type: "NS", TTL: 3600, Records: []string{
			"hydrogen.ns.hetzner.com.", "oxygen.ns.hetzner.com.", "helium.ns.hetzner.de.",
		}},
	}
	return append(sets, extra...)
}

// fakeUpstream starts a DNS server on 127.0.0.1 that answers A queries for
// the names in answers and REFUSED for everything else.
func fakeUpstream(t *testing.T, answers map[string]string) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	started := make(chan struct{})
	srv := &dns.Server{
		PacketConn:        pc,
		NotifyStartedFunc: func() { close(started) },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			m := new(dns.Msg)
			m.SetReply(req)
			q := req.Question[0]
			ip, ok := answers[q.Name]
			switch {
			case ok && q.Qtype == dns.TypeA:
				m.Answer = append(m.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
					A:   net.ParseIP(ip),
				})
			case ok:
			default:
				m.Rcode = dns.RcodeRefused
			}
			_ = w.WriteMsg(m)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fake upstream did not start")
	}
	t.Cleanup(func() { _ = srv.Shutdown() })
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

// recordWriter is a dns.ResponseWriter that keeps the written message.
type recordWriter struct{ msg *dns.Msg }

func (w *recordWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("198.19.0.1"), Port: 53}
}
func (w *recordWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("198.19.0.2"), Port: 40000}
}
func (w *recordWriter) WriteMsg(m *dns.Msg) error { w.msg = m; return nil }
func (w *recordWriter) Write(b []byte) (int, error) {
	m := new(dns.Msg)
	if err := m.Unpack(b); err != nil {
		return 0, err
	}
	w.msg = m
	return len(b), nil
}
func (w *recordWriter) Close() error        { return nil }
func (w *recordWriter) TsigStatus() error   { return nil }
func (w *recordWriter) TsigTimersOnly(bool) {}
func (w *recordWriter) Hijack()             {}

// query sends one question to the handler and returns its answer.
func query(t *testing.T, d *agent.DNS, name string, qtype uint16) *dns.Msg {
	t.Helper()
	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(name), qtype)
	req.RecursionDesired = true
	w := &recordWriter{}
	d.ServeDNS(w, req)
	if w.msg == nil {
		t.Fatalf("no answer for %s %s", name, dns.TypeToString[qtype])
	}
	return w.msg
}

// rrText renders the answer section as "<owner> <TYPE> <value>".
func rrText(rrs []dns.RR) []string {
	out := []string{}
	for _, rr := range rrs {
		v := ""
		switch r := rr.(type) {
		case *dns.A:
			v = r.A.String()
		case *dns.AAAA:
			v = r.AAAA.String()
		case *dns.CNAME:
			v = r.Target
		default:
			v = rr.String()
		}
		out = append(out, rr.Header().Name+" "+dns.TypeToString[rr.Header().Rrtype]+" "+v)
	}
	return out
}

// testDNSZones: the agent answers authoritatively from the zones of the
// pushed document and forwards everything else to the upstream servers.
func testDNSZones(t *testing.T) {
	upstream := fakeUpstream(t, map[string]string{
		"ext.example.net.": "192.0.2.50",
		"www.other.org.":   "192.0.2.70",
	})
	cfg := agentapi.Config{
		Upstream: []netip.AddrPort{upstream},
		Zones: []agentapi.Zone{
			{Name: "example.com", TTL: 3600, RRSets: zoneSets(
				agentapi.RRSet{Name: "@", Type: "A", TTL: 3600, Records: []string{"192.0.2.1"}},
				agentapi.RRSet{Name: "www", Type: "A", TTL: 300, Records: []string{"192.0.2.10"}},
				agentapi.RRSet{Name: "*.dev", Type: "A", TTL: 300, Records: []string{"192.0.2.99"}},
				agentapi.RRSet{Name: "api.dev", Type: "A", TTL: 300, Records: []string{"192.0.2.98"}},
				agentapi.RRSet{Name: "alias", Type: "CNAME", TTL: 300, Records: []string{"www"}},
				agentapi.RRSet{Name: "ext", Type: "CNAME", TTL: 300, Records: []string{"ext.example.net."}},
				agentapi.RRSet{Name: "www.sub", Type: "A", TTL: 300, Records: []string{"192.0.2.30"}},
			)},
			{Name: "sub.example.com", TTL: 3600, RRSets: zoneSets(
				agentapi.RRSet{Name: "www", Type: "A", TTL: 300, Records: []string{"192.0.2.20"}},
			)},
		},
	}
	d := &agent.DNS{}
	t.Cleanup(d.Close)
	// Outside the appliance the listen addresses do not exist, so Apply may
	// fail to bind; the queries go to the handler directly.
	_ = d.Apply(context.Background(), cfg)

	cases := []struct {
		name   string
		qname  string
		qtype  uint16
		rcode  int
		answer []string
		soa    string // owner of the SOA expected in the authority section, "" none checked
		auth   bool   // the answer must be authoritative
	}{
		{"www", "www.example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"www.example.com. A 192.0.2.10"}, "", true},
		{"apex", "example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"example.com. A 192.0.2.1"}, "", true},
		{"wildcard", "foo.dev.example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"foo.dev.example.com. A 192.0.2.99"}, "", true},
		{"wildcard_not_for_own_records", "api.dev.example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"api.dev.example.com. A 192.0.2.98"}, "", true},
		{"nxdomain", "unknown.example.com", dns.TypeA, dns.RcodeNameError,
			[]string{}, "example.com.", true},
		{"nodata", "www.example.com", dns.TypeAAAA, dns.RcodeSuccess,
			[]string{}, "example.com.", true},
		{"cname_in_zone", "alias.example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"alias.example.com. CNAME www.example.com.", "www.example.com. A 192.0.2.10"}, "", true},
		{"cname_upstream", "ext.example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"ext.example.com. CNAME ext.example.net.", "ext.example.net. A 192.0.2.50"}, "", false},
		{"more_specific_zone", "www.sub.example.com", dns.TypeA, dns.RcodeSuccess,
			[]string{"www.sub.example.com. A 192.0.2.20"}, "", true},
		{"forwarded", "www.other.org", dns.TypeA, dns.RcodeSuccess,
			[]string{"www.other.org. A 192.0.2.70"}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := query(t, d, c.qname, c.qtype)
			if m.Rcode != c.rcode {
				t.Errorf("rcode = %s, want %s", dns.RcodeToString[m.Rcode], dns.RcodeToString[c.rcode])
			}
			if got := rrText(m.Answer); !slices.Equal(got, c.answer) {
				t.Errorf("answer = %q, want %q", got, c.answer)
			}
			if c.auth && !m.Authoritative {
				t.Error("answer is not authoritative")
			}
			if c.soa != "" {
				var owners []string
				for _, rr := range m.Ns {
					if soa, ok := rr.(*dns.SOA); ok {
						owners = append(owners, soa.Hdr.Name)
					}
				}
				if !slices.Equal(owners, []string{c.soa}) {
					t.Errorf("SOA in authority = %q, want [%q]", owners, c.soa)
				}
			}
		})
	}
}
