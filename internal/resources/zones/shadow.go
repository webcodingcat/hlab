package zones

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Lookup reports whether zone resolves publicly.
type Lookup func(ctx context.Context, zone string) (bool, error)

// fallbackServers are asked when no host DNS servers are given.
var fallbackServers = []netip.AddrPort{
	netip.MustParseAddrPort("1.1.1.1:53"),
	netip.MustParseAddrPort("9.9.9.9:53"),
}

// PublicLookup asks servers (else 1.1.1.1:53, 9.9.9.9:53) in order for the
// SOA of zone with timeout per server. It is true when one answers NOERROR
// with an SOA or NS answer for zone itself, false on NXDOMAIN or NODATA, and
// an error when none answers.
func PublicLookup(servers []netip.AddrPort, timeout time.Duration) Lookup {
	if len(servers) == 0 {
		servers = fallbackServers
	}
	servers = slices.Clone(servers)
	return func(ctx context.Context, zone string) (bool, error) {
		name := dns.Fqdn(zone)
		q := new(dns.Msg)
		q.SetQuestion(name, dns.TypeSOA)
		client := &dns.Client{Net: "udp", Timeout: timeout}
		var errs []error
		for _, srv := range servers {
			r, _, err := client.ExchangeContext(ctx, q, srv.String())
			if err != nil {
				errs = append(errs, err)
				continue
			}
			switch r.Rcode {
			case dns.RcodeNameError:
				return false, nil
			case dns.RcodeSuccess:
				for _, rr := range r.Answer {
					t := rr.Header().Rrtype
					if (t == dns.TypeSOA || t == dns.TypeNS) && strings.EqualFold(rr.Header().Name, name) {
						return true, nil
					}
				}
				return false, nil
			default:
				errs = append(errs, fmt.Errorf("%s answered %s", srv, dns.RcodeToString[r.Rcode]))
			}
		}
		return false, fmt.Errorf("no DNS server answered for %s: %w", zone, errors.Join(errs...))
	}
}

// Shadow keeps which primary zones shadow a public domain; it implements
// ShadowWatch.
type Shadow struct {
	lookup Lookup

	mu     sync.Mutex
	gen    map[string]uint64 // per zone: the generation of its newest Check
	next   uint64
	listed map[string]string // zone -> project of zones found public
}

// NewShadow returns a Shadow that checks zones with lookup.
func NewShadow(lookup Lookup) *Shadow {
	return &Shadow{lookup: lookup, gen: map[string]uint64{}, listed: map[string]string{}}
}

// Check starts a background lookup of zone; its result replaces that of
// earlier calls. The lookup's own timeouts bound it.
func (s *Shadow) Check(zone, project string) {
	s.mu.Lock()
	s.next++
	g := s.next
	s.gen[zone] = g
	s.mu.Unlock()
	go func() {
		public, err := s.lookup(context.Background(), zone)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.gen[zone] != g {
			return
		}
		if public && err == nil {
			s.listed[zone] = project
		} else {
			delete(s.listed, zone)
		}
	}()
}

// Forget drops zone and discards the result of a running check.
func (s *Shadow) Forget(zone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.gen, zone)
	delete(s.listed, zone)
}

// List returns the zones that shadow a public domain, sorted by zone.
func (s *Shadow) List() []ZoneRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ZoneRef, 0, len(s.listed))
	for z, p := range s.listed {
		out = append(out, ZoneRef{Zone: z, Project: p})
	}
	slices.SortFunc(out, func(a, b ZoneRef) int { return cmp.Compare(a.Zone, b.Zone) })
	return out
}
