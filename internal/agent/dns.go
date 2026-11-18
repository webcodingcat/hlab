package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"

	"github.com/webcodingcat/hlab/internal/agentapi"
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
)

var apiAddr = netip.MustParseAddr("198.19.0.1")

// DNS is the Applier of the appliance's DNS server: authoritative for
// hetzner.internal and the reverse zones of the public range, forwarding
// everything else to Config.Upstream. The listeners start with the first
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
	soa     dns.RR
	records map[string][]dns.RR // by canonical owner name
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
		writeDNS(w, req, z.answer(req, name))
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

// zoneFor returns the authoritative zone of name, or nil.
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

// answer answers a question for name inside the zone.
func (z *dnsZone) answer(req *dns.Msg, name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = true
	rrs, ok := z.records[name]
	if !ok {
		m.Rcode = dns.RcodeNameError
		m.Ns = []dns.RR{z.soa}
		return m
	}
	qtype := req.Question[0].Qtype
	for _, rr := range rrs {
		if qtype == dns.TypeANY || rr.Header().Rrtype == qtype {
			m.Answer = append(m.Answer, rr)
		}
	}
	if len(m.Answer) == 0 {
		m.Ns = []dns.RR{z.soa}
	}
	return m
}

// forward asks the upstream servers in order and returns the first answer,
// retrying over TCP when a UDP answer is truncated.
func (dd *dnsData) forward(w dns.ResponseWriter, req *dns.Msg) *dns.Msg {
	first := "udp"
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		first = "tcp"
	}
	for _, up := range dd.upstream {
		r, err := exchange(first, req, up)
		if err == nil && r.Truncated && first == "udp" {
			r, err = exchange("tcp", req, up)
		}
		if err != nil {
			slog.Debug("dns upstream failed", "upstream", up, "err", err)
			continue
		}
		r.Id = req.Id
		return r
	}
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeServerFailure)
	m.RecursionAvailable = true
	return m
}

func exchange(network string, req *dns.Msg, upstream string) (*dns.Msg, error) {
	c := &dns.Client{Net: network, Timeout: upstreamTimeout}
	r, _, err := c.Exchange(req, upstream)
	if err == nil && r == nil {
		err = errors.New("empty answer")
	}
	return r, err
}

// buildDNSData builds the answer data of a document. User zones
// (Config.Zones) are not served yet; queries for them are forwarded.
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
		for _, name := range reverseZones(cfg.PublicRange) {
			dd.zones[name] = newZone(name, serial)
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
	z := &dnsZone{records: map[string][]dns.RR{}}
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

func (z *dnsZone) add(rr dns.RR) {
	n := rr.Header().Name
	z.records[n] = append(z.records[n], rr)
}

func hdr(name string, t uint16) dns.RR_Header {
	return dns.RR_Header{Name: name, Rrtype: t, Class: dns.ClassINET, Ttl: dnsTTL}
}

// reverseZones returns the in-addr.arpa zones covering an IPv4 prefix on
// octet boundaries: a /16 is one zone, a /20 sixteen /24 zones. Prefixes
// longer than /24 are served as their whole /24 zone.
func reverseZones(p netip.Prefix) []string {
	p = p.Masked()
	bits := p.Bits()
	zoneBits := min(max((bits+7)/8*8, 8), 24)
	count := 1
	if zoneBits > bits {
		count = 1 << (zoneBits - bits)
	}
	a4 := p.Addr().As4()
	base := uint32(a4[0])<<24 | uint32(a4[1])<<16 | uint32(a4[2])<<8 | uint32(a4[3])
	zones := make([]string, 0, count)
	for i := range count {
		v := base + uint32(i)<<(32-zoneBits) //nolint:gosec // i < 2^24
		var labels []string
		for o := zoneBits/8 - 1; o >= 0; o-- {
			labels = append(labels, strconv.Itoa(int(v>>(24-8*o)&0xff)))
		}
		zones = append(zones, strings.Join(labels, ".")+".in-addr.arpa.")
	}
	return zones
}
