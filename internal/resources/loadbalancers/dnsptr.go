package loadbalancers

import (
	"net/http"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// dnsPtrRoutes registers change_dns_ptr.
func (m *Module) dnsPtrRoutes(r resource.Registrar) {
	r.Handle("POST /load_balancers/{id}/actions/change_dns_ptr", http.HandlerFunc(m.changeDNSPtr))
}

// changeDNSPtr sets or resets a reverse DNS entry of the load balancer's
// public IPv4 (its /32) or of an address in its IPv6 /64; the action pushes
// the agent document.
func (m *Module) changeDNSPtr(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	ip, ptr, ok := servers.DecodeDNSPtr(w, req)
	if !ok {
		return
	}
	m.change(w, req, id, "change_dns_ptr", func(_ store.Tx, r *Record) ([]actions.Ref, error) {
		owner := r.IPv6Net()
		if a, err := netip.ParseAddr(ip); err == nil && a.Is4() {
			owner = netip.PrefixFrom(r.IPv4(), 32)
		}
		entries, err := rdns.Change(r.DNSPtr, owner, ip, ptr)
		if err != nil {
			return nil, err
		}
		r.DNSPtr = entries
		return nil, nil
	})
}
