package zones_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resources/zones"
)

// startDNS serves h on a UDP port of 127.0.0.1 until the test ends.
func startDNS(t *testing.T, h dns.HandlerFunc) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: h}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.ActivateAndServe()
	}()
	t.Cleanup(func() {
		_ = srv.Shutdown()
		<-done
	})
	return netip.MustParseAddrPort(pc.LocalAddr().String())
}

// closedPort returns a UDP address of 127.0.0.1 nothing listens on.
func closedPort(t *testing.T) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := netip.MustParseAddrPort(pc.LocalAddr().String())
	_ = pc.Close()
	return addr
}

// fakeLookup answers per zone from a map that the test may change.
type fakeLookup struct {
	mu      sync.Mutex
	answers map[string]error // nil error: true; errNotPublic: false
}

var errNotPublic = errors.New("not public")

func (f *fakeLookup) set(zone string, answer error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[zone] = answer
}

func (f *fakeLookup) lookup(_ context.Context, zone string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch err := f.answers[zone]; {
	case err == nil:
		return true, nil
	case errors.Is(err, errNotPublic):
		return false, nil
	default:
		return false, err
	}
}

// waitFor waits until cond holds and fails the test after the deadline.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within %s", what, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func testShadow(t *testing.T) {
	addr := startDNS(t, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		switch q := r.Question[0]; q.Name {
		case "public.example.":
			soa, err := dns.NewRR("public.example. 3600 IN SOA ns1.public.example. hostmaster.public.example. 1 7200 3600 1209600 3600")
			if err != nil {
				t.Errorf("SOA record: %v", err)
			}
			m.Answer = append(m.Answer, soa)
		default:
			m.Rcode = dns.RcodeNameError
		}
		_ = w.WriteMsg(m)
	})
	ctx := context.Background()
	lookup := zones.PublicLookup([]netip.AddrPort{addr}, 2*time.Second)
	if ok, err := lookup(ctx, "public.example"); !ok || err != nil {
		t.Errorf("lookup of a zone answered with SOA: %v, %v, want true", ok, err)
	}
	if ok, err := lookup(ctx, "missing.example"); ok || err != nil {
		t.Errorf("lookup of a zone answered NXDOMAIN: %v, %v, want false", ok, err)
	}
	dead := zones.PublicLookup([]netip.AddrPort{closedPort(t)}, 200*time.Millisecond)
	if _, err := dead(ctx, "public.example"); err == nil {
		t.Errorf("lookup without an answering server: no error")
	}

	fl := &fakeLookup{answers: map[string]error{}}
	s := zones.NewShadow(fl.lookup)
	listed := func(want ...zones.ZoneRef) func() bool {
		return func() bool {
			got := s.List()
			return reflect.DeepEqual(got, want) || len(got) == 0 && len(want) == 0
		}
	}
	com := zones.ZoneRef{Zone: "example.com", Project: "p"}
	netRef := zones.ZoneRef{Zone: "example.net", Project: "q"}
	s.Check("example.net", "q")
	s.Check("example.com", "p")
	waitFor(t, "two public zones listed sorted by zone", listed(com, netRef))
	fl.set("example.net", errNotPublic)
	s.Check("example.net", "q")
	waitFor(t, "zone whose lookup is false dropped", listed(com))
	fl.set("example.org", nil)
	s.Check("example.org", "q")
	waitFor(t, "public example.org listed", listed(com, zones.ZoneRef{Zone: "example.org", Project: "q"}))
	fl.set("example.org", errors.New("no server answered"))
	s.Check("example.org", "q")
	waitFor(t, "zone whose lookup failed dropped", listed(com))
	s.Forget("example.com")
	waitFor(t, "forgotten zone dropped", listed())

	e := newZoneEnv(t)
	createZone(t, e, map[string]any{"name": "lab.test", "mode": "primary"})
	createZone(t, e, map[string]any{"name": "example.org", "mode": "secondary",
		"primary_nameservers": []map[string]any{{"address": "192.0.2.53"}}})
	createZone(t, e, map[string]any{"name": "example.com", "mode": "primary"})
	waitFor(t, "Check of the public primary zone", func() bool {
		return slices.Contains(e.shadow.checked(), com)
	})
	if got := e.shadow.checked(); !reflect.DeepEqual(got, []zones.ZoneRef{com}) {
		t.Errorf("shadow checks %+v, want only %+v", got, com)
	}
}
