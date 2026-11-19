package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/dnszone"
)

// dnsListen are the addresses the DNS server binds: the transit address
// (host and WSL2 through NRPT) and Hetzner's resolver addresses (guests).
// Never the wildcard: systemd-resolved holds 127.0.0.53/54.
var dnsListen = []string{"198.19.0.1:53", "185.12.64.1:53", "185.12.64.2:53"}

// defaultUpstream is used when the document names no forwarders.
var defaultUpstream = []string{"1.1.1.1:53", "9.9.9.9:53"}

const (
	internalZone    = "hetzner.internal."
	apiName         = "api.hetzner.internal."
	dnsTTL          = 60
	upstreamTimeout = 2 * time.Second
	maxCNAMEChain   = 8
)

var apiAddr = netip.MustParseAddr("198.19.0.1")

// DNS is the Applier of the appliance's DNS server: authoritative for
// hetzner.internal, the reverse zones of the public range and the user zones
// of Config.Zones, forwarding everything else to Config.Upstream. The listeners start with the first
// apply; later applies swap the answer data in place.
type DNS struct {
	data atomic.Pointer[dnsData]

	mu      sync.Mutex
	servers []*dns.Server
}

// dnsData is one immutable snapshot of what the server answers.
type dnsData struct {
	zones    map[string]*dnsZone // by canonical zone name
	upstream []string
}

type dnsZone struct {
	name    string // canonical zone name
	soa     dns.RR
	records map[string][]dns.RR // by canonical owner name
	exists  map[string]bool     // owners and their ancestors in the zone (empty non-terminals)
}

// Name implements Applier.
func (d *DNS) Name() string { return "dns" }

// Apply implements Applier. It needs the listen addresses on an interface,
// so it runs after the network applier.
func (d *DNS) Apply(_ context.Context, cfg agentapi.Config) error {
	d.data.Store(buildDNSData(cfg))

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.servers != nil {
		return nil
	}
	var servers []*dns.Server
	for _, addr := range dnsListen {
		pc, err := net.ListenPacket("udp4", addr)
		if err != nil {
			shutdownAll(servers)
			return fmt.Errorf("dns: %w", err)
		}
		servers = append(servers, &dns.Server{PacketConn: pc, Handler: d})
		ln, err := net.Listen("tcp4", addr)
		if err != nil {
			shutdownAll(servers)
			return fmt.Errorf("dns: %w", err)
		}
		servers = append(servers, &dns.Server{Listener: ln, Handler: d})
	}
	for _, s := range servers {
		go func() {
			if err := s.ActivateAndServe(); err != nil {
				slog.Error("dns server stopped", "err", err)
			}
		}()
	}
	d.servers = servers
	slog.Info("dns server started", "listen", dnsListen)
	return nil
}

// Close stops the DNS server.
func (d *DNS) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	shutdownAll(d.servers)
	d.servers = nil
}

// shutdownAll stops servers; servers that never started are closed through
// their socket.
func shutdownAll(servers []*dns.Server) {
	for _, s := range servers {
		if err := s.Shutdown(); err != nil {
			if s.PacketConn != nil {
				_ = s.PacketConn.Close()
			}
			if s.Listener != nil {
				_ = s.Listener.Close()
			}
		}
	}
}

// ServeDNS implements dns.Handler.
func (d *DNS) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	data := d.data.Load()
	if len(req.Question) != 1 {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeFormatError)
		writeDNS(w, req, m)
		return
	}
	name := dns.CanonicalName(req.Question[0].Name)
	if z := data.zoneFor(name); z != nil {
		writeDNS(w, req, data.answer(req, z, name))
		return
	}
	writeDNS(w, req, data.forward(w, req))
}

func writeDNS(w dns.ResponseWriter, req, m *dns.Msg) {
	if _, ok := w.RemoteAddr().(*net.UDPAddr); ok {
		size := dns.MinMsgSize
		if opt := req.IsEdns0(); opt != nil {
			size = int(opt.UDPSize())
		}
		m.Truncate(size)
	}
	if err := w.WriteMsg(m); err != nil {
		slog.Debug("dns write", "err", err)
	}
}

// zoneFor returns the most specific authoritative zone of name, or nil.
func (dd *dnsData) zoneFor(name string) *dnsZone {
	for n := name; n != ""; {
		if z, ok := dd.zones[n]; ok {
			return z
		}
		i := strings.IndexByte(n, '.')
		if i < 0 || i == len(n)-1 {
			return nil
		}
		n = n[i+1:]
	}
	return nil
}

// answer answers a question for name inside zone z. A CNAME is followed
// inside the served zones and, for a target outside them, through the
// upstream servers, at most maxCNAMEChain steps; the chain comes first.
func (dd *dnsData) answer(req *dns.Msg, z *dnsZone, name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = true
	qtype := req.Question[0].Qtype
	for step := 1; ; step++ {
		rrs, found := z.lookup(name)
		if !found {
			m.Rcode = dns.RcodeNameError
			m.Ns = []dns.RR{z.soa}
			return m
		}
		var cname dns.RR
		for _, rr := range rrs {
			switch {
			case qtype == dns.TypeANY || rr.Header().Rrtype == qtype:
				m.Answer = append(m.Answer, rr)
			case rr.Header().Rrtype == dns.TypeCNAME:
				cname = rr
			}
		}
		if cname == nil {
			if len(m.Answer) == 0 {
				m.Ns = []dns.RR{z.soa}
			}
			return m
		}
		m.Answer = append(m.Answer, cname)
		if step >= maxCNAMEChain {
			return m
		}
		name = dns.CanonicalName(cname.(*dns.CNAME).Target)
		if z = dd.zoneFor(name); z == nil {
			dd.chase(m, name, qtype)
			return m
		}
	}
}

// chase asks the upstream servers for a CNAME target outside the served
// zones and appends their answer; when they fail, m keeps the chain so far.
func (dd *dnsData) chase(m *dns.Msg, target string, qtype uint16) {
	q := new(dns.Msg)
	q.SetQuestion(target, qtype)
	q.RecursionDesired = true
	r, err := dd.ask("udp", q)
	if err != nil {
		return
	}
	switch r.Rcode {
	case dns.RcodeSuccess:
		m.Answer = append(m.Answer, r.Answer...)
	case dns.RcodeNameError:
		m.Rcode = dns.RcodeNameError
		m.Ns = r.Ns
	}
}

// lookup returns the records of name, synthesized from a wildcard when name
// does not exist (RFC 4592: only the wildcard below the closest existing
// ancestor answers); found is false when neither exists.
func (z *dnsZone) lookup(name string) (rrs []dns.RR, found bool) {
	if z.exists[name] {
		return z.records[name], true
	}
	for n := name; n != z.name; {
		i := strings.IndexByte(n, '.')
		if i < 0 || i == len(n)-1 {
			return nil, false
		}
		n = n[i+1:]
		if !z.exists[n] {
			continue
		}
		wild, ok := z.records["*."+n]
		if !ok {
			return nil, false
		}
		out := make([]dns.RR, 0, len(wild))
		for _, rr := range wild {
			c := dns.Copy(rr)
			c.Header().Name = name
			out = append(out, c)
		}
		return out, true
	}
	return nil, false
}

// forward asks the upstream servers for req and returns their answer, or
// SERVFAIL when none answers.
func (dd *dnsData) forward(w dns.ResponseWriter, req *dns.Msg) *dns.Msg {
	first := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		first = "tcp"
	}
	r, err := dd.ask(first, req)
	if err != nil {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeServerFailure)
		m.RecursionAvailable = true
		return m
	}
	r.Id = req.Id
	return r
}

// ask asks the upstream servers in order and returns the first answer,
// retrying over TCP when a UDP answer is truncated.
func (dd *dnsData) ask(first string, req *dns.Msg) (*dns.Msg, error) {
	err := errors.New("no upstream server")
	for _, up := range dd.upstream {
		var r *dns.Msg
		r, err = exchange(first, req, up)
		if err == nil && r.Truncated && first == "udp" {
			r, err = exchange("tcp", req, up)
		}
		if err != nil {
			slog.Debug("dns upstream failed", "upstream", up, "err", err)
			continue
		}
		return r, nil
	}
	return nil, err
}

func exchange(network string, req *dns.Msg, upstream string) (*dns.Msg, error) {
	c := &dns.Client{Net: network, Timeout: upstreamTimeout}
	r, _, err := c.Exchange(req, upstream)
	if err == nil && r == nil {
		err = errors.New("empty answer")
	}
	return r, err
}

// buildDNSData builds the answer data of a document.
func buildDNSData(cfg agentapi.Config) *dnsData {
	dd := &dnsData{zones: map[string]*dnsZone{}}
	for _, u := range cfg.Upstream {
		if u.IsValid() {
			dd.upstream = append(dd.upstream, u.String())
		}
	}
	if len(dd.upstream) == 0 {
		dd.upstream = defaultUpstream
	}
	serial := uint32(cfg.Generation) //nolint:gosec // generations are small positive counters

	internal := newZone(internalZone, serial)
	internal.add(&dns.A{Hdr: hdr(apiName, dns.TypeA), A: apiAddr.AsSlice()})
	dd.zones[internalZone] = internal

	if cfg.PublicRange.Addr().Is4() {
		for _, name := range agentapi.ReverseZones(cfg.PublicRange) {
			dd.zones[name] = newZone(name, serial)
		}
	}
	for _, uz := range cfg.Zones {
		if z := userZone(uz); z != nil {
			dd.zones[z.name] = z
		}
	}
	for addr, target := range cfg.PTR {
		if !addr.Is4() || target == "" {
			continue
		}
		owner, err := dns.ReverseAddr(addr.String())
		if err != nil {
			continue
		}
		if z := dd.zoneFor(owner); z != nil {
			z.add(&dns.PTR{Hdr: hdr(owner, dns.TypePTR), Ptr: dns.Fqdn(strings.ToLower(target))})
		}
	}
	return dd
}

// newZone returns a zone with SOA and NS; api.hetzner.internal is its name
// server.
func newZone(name string, serial uint32) *dnsZone {
	z := &dnsZone{name: name, records: map[string][]dns.RR{}, exists: map[string]bool{}}
	z.soa = &dns.SOA{
		Hdr:     hdr(name, dns.TypeSOA),
		Ns:      apiName,
		Mbox:    "hostmaster." + internalZone,
		Serial:  serial,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  dnsTTL,
	}
	z.add(z.soa)
	z.add(&dns.NS{Hdr: hdr(name, dns.TypeNS), Ns: apiName})
	return z
}

// userZone builds a zone of the document; its SOA comes from the apex SOA
// RRSet. Records that do not parse are skipped; a zone without SOA is not
// served.
func userZone(uz agentapi.Zone) *dnsZone {
	name := dns.CanonicalName(uz.Name)
	z := &dnsZone{name: name, records: map[string][]dns.RR{}, exists: map[string]bool{}}
	for _, set := range uz.RRSets {
		ds := dnszone.RRSet{Name: set.Name, Type: set.Type}
		if set.TTL > 0 {
			ds.TTL = &set.TTL
		}
		for _, v := range set.Records {
			ds.Records = append(ds.Records, dnszone.Record{Value: v})
		}
		rrs, err := dnszone.ToRR(strings.TrimSuffix(name, "."), uz.TTL, ds)
		if err != nil {
			slog.Warn("dns record skipped", "zone", uz.Name, "err", err)
			continue
		}
		for _, rr := range rrs {
			rr.Header().Name = dns.CanonicalName(rr.Header().Name)
			if rr.Header().Rrtype == dns.TypeSOA && rr.Header().Name == name {
				z.soa = rr
			}
			z.add(rr)
		}
	}
	if z.soa == nil {
		slog.Warn("dns zone without SOA not served", "zone", uz.Name)
		return nil
	}
	return z
}

// add adds rr and marks its owner and the owner's ancestors up to the apex
// as existing.
func (z *dnsZone) add(rr dns.RR) {
	n := rr.Header().Name
	z.records[n] = append(z.records[n], rr)
	for !z.exists[n] {
		z.exists[n] = true
		i := strings.IndexByte(n, '.')
		if n == z.name || i < 0 || i == len(n)-1 {
			return
		}
		n = n[i+1:]
	}
}

func hdr(name string, t uint16) dns.RR_Header {
	return dns.RR_Header{Name: name, Rrtype: t, Class: dns.ClassINET, Ttl: dnsTTL}
}
