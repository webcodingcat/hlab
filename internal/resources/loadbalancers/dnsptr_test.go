package loadbalancers_test

import (
	"net/netip"
	"testing"

	"github.com/webcodingcat/hlab/internal/rdns"
)

func strOf(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

// testDNSPtr: reverse DNS of the load balancer's IPv4 and IPv6.
func testDNSPtr(t *testing.T) {
	e := newEnv(t)
	lb := createLB(t, e, lbBody("rdns"))
	v4, v6 := lb.ipv4(), lb.ipv6()

	runLBAction(t, e, lb.ID, "change_dns_ptr", map[string]any{"ip": v4, "dns_ptr": "lb.example.com"})
	if got := mustLB(t, e, lb.ID).PublicNet.IPv4.DNSPtr; strOf(got) != "lb.example.com" {
		t.Errorf("ipv4 dns_ptr %s, want lb.example.com", strOf(got))
	}
	runLBAction(t, e, lb.ID, "change_dns_ptr", map[string]any{"ip": v4, "dns_ptr": nil})
	if got, want := mustLB(t, e, lb.ID).PublicNet.IPv4.DNSPtr, rdns.Default(netip.MustParseAddr(v4)); strOf(got) != want {
		t.Errorf("ipv4 dns_ptr after reset %s, want the default %s", strOf(got), want)
	}

	runLBAction(t, e, lb.ID, "change_dns_ptr", map[string]any{"ip": v6, "dns_ptr": "lb6.example.com"})
	if got := mustLB(t, e, lb.ID).PublicNet.IPv6.DNSPtr; strOf(got) != "lb6.example.com" {
		t.Errorf("ipv6 dns_ptr %s, want lb6.example.com", strOf(got))
	}

	wantFieldError(t, lbAction(e, lb.ID, "change_dns_ptr", map[string]any{"ip": "198.19.250.250", "dns_ptr": "x.example.com"}),
		"ip", "change_dns_ptr on an IP the LB does not hold")
}
