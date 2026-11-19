package loadbalancers_test

import (
	"crypto/tls"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/store"
)

// pushedLB returns the load balancer id of doc, if there.
func pushedLB(doc agentapi.Config, id int64) (agentapi.LoadBalancer, bool) {
	for _, l := range doc.LoadBalancers {
		if l.ID == id {
			return l, true
		}
	}
	return agentapi.LoadBalancer{}, false
}

func targetKeys(ts []agentapi.LBTarget) []string {
	out := make([]string, len(ts))
	for i, tg := range ts {
		out[i] = fmt.Sprintf("%s %s via %d", tg.Key, tg.IP, tg.ViaNetworkID)
	}
	slices.Sort(out)
	return out
}

// testDocument: the pushed document of a load balancer.
func testDocument(t *testing.T) {
	e := newEnv(t)
	n := createNetwork(t, e, "n", "10.0.0.0/16", "10.0.1.0/24")
	var vlan int
	if err := e.Store.Tx(func(tx store.Tx) error {
		r, err := networks.Load(tx, e.Project.ID, n)
		vlan = r.Hlab.VLAN
		return err
	}); err != nil {
		t.Fatalf("load network: %v", err)
	}
	certID, certPEM, _ := uploadCert(t, e.Env, "doc-cert")

	public := createServer(t, e, "public", "fsn1", nil)
	private := createServer(t, e, "private", "fsn1", map[string]any{"networks": []int64{n}})
	labeled := createServer(t, e, "labeled", "fsn1", map[string]any{"labels": map[string]string{"role": "db"}})

	body := lbBody("doc")
	body["algorithm"] = map[string]any{"type": "least_connections"}
	lb := createLB(t, e, body)
	runLBAction(t, e, lb.ID, "attach_to_network", map[string]any{"network": n, "ip": "10.0.1.50"})
	runLBAction(t, e, lb.ID, "add_service", httpServiceBody("https", 443, 80, map[string]any{"certificates": []int64{certID}}))
	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(public.ID, false))
	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(private.ID, true))
	runLBAction(t, e, lb.ID, "add_target", selectorTargetBody("role=db"))
	runLBAction(t, e, lb.ID, "add_target", ipTargetBody("203.0.113.20"))

	wantTargets := []string{
		fmt.Sprintf("server:%d %s via 0", public.ID, public.PublicNet.IPv4.IP),
		fmt.Sprintf("server:%d %s via %d", private.ID, private.privateIP(n), n),
		fmt.Sprintf("server:%d %s via 0", labeled.ID, labeled.PublicNet.IPv4.IP),
		"ip:203.0.113.20 203.0.113.20 via 0",
	}
	slices.Sort(wantTargets)
	doc := e.waitDoc(t, "the load balancer's four targets", func(doc agentapi.Config) bool {
		l, ok := pushedLB(doc, lb.ID)
		return ok && len(l.Targets) == len(wantTargets)
	})
	got, _ := pushedLB(doc, lb.ID)
	if got.PublicIPv4.String() != lb.ipv4() {
		t.Errorf("PublicIPv4 %s, want %s", got.PublicIPv4, lb.ipv4())
	}
	if len(got.Private) != 1 || got.Private[0].NetworkID != n || got.Private[0].VLAN != vlan || got.Private[0].IP != netip.MustParseAddr("10.0.1.50") {
		t.Errorf("Private %+v, want network %d VLAN %d IP 10.0.1.50", got.Private, n, vlan)
	}
	if got.Algorithm != "least_connections" {
		t.Errorf("Algorithm %q, want least_connections", got.Algorithm)
	}
	if g := targetKeys(got.Targets); !slices.Equal(g, wantTargets) {
		t.Errorf("Targets\n%s\nwant\n%s", strings.Join(g, "\n"), strings.Join(wantTargets, "\n"))
	}
	if len(got.Services) != 1 || got.Services[0].ListenPort != 443 || got.Services[0].HTTP == nil {
		t.Fatalf("Services %+v, want the https service on 443", got.Services)
	}
	h := got.Services[0].HTTP
	if len(h.CertPEM) != 1 || len(h.KeyPEM) != 1 || !strings.Contains(h.CertPEM[0], strings.TrimSpace(certPEM)) {
		t.Fatalf("service certificates %d/%d, want the uploaded certificate and its key", len(h.CertPEM), len(h.KeyPEM))
	}
	if _, err := tls.X509KeyPair([]byte(h.CertPEM[0]), []byte(h.KeyPEM[0])); err != nil {
		t.Errorf("pushed certificate and key do not form a pair: %v", err)
	}

	// A load balancer with the public interface off has no public IPv4.
	off := createLB(t, e, lbBody("off"))
	runLBAction(t, e, off.ID, "attach_to_network", map[string]any{"network": n})
	runLBAction(t, e, off.ID, "disable_public_interface", nil)
	e.waitDoc(t, "a load balancer without public IPv4", func(doc agentapi.Config) bool {
		l, ok := pushedLB(doc, off.ID)
		return ok && !l.PublicIPv4.IsValid()
	})
}

// testDocumentPTR: the reverse DNS of a load balancer's IPv4 in the
// document.
func testDocumentPTR(t *testing.T) {
	e := newEnv(t)
	named := createLB(t, e, lbBody("named"))
	plain := createLB(t, e, lbBody("plain"))
	runLBAction(t, e, named.ID, "change_dns_ptr", map[string]any{"ip": named.ipv4(), "dns_ptr": "lb.example.com"})

	a, b := netip.MustParseAddr(named.ipv4()), netip.MustParseAddr(plain.ipv4())
	doc := e.waitDoc(t, "the PTR of the named load balancer", func(doc agentapi.Config) bool {
		return doc.PTR[a] == "lb.example.com"
	})
	if got, want := doc.PTR[b], rdns.Default(b); got != want {
		t.Errorf("PTR of %s = %q, want the default %q", b, got, want)
	}
}
