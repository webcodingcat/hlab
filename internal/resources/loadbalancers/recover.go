package loadbalancers

import (
	"context"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/store"
)

// Recover implements resource.Recoverer: Q14 moved the formal IPv6 /64 of a
// load balancer without an IPv6 primary IP from 2001:db8:<0x80000000|id> to
// 2001:db8:<0xA0000000|id>. Every dns_ptr entry such a load balancer still
// holds in the old /64 moves to the new one with the same interface ID. Load
// balancers with an IPv6 primary IP stay untouched; a second run finds
// nothing in the old /64 and changes nothing.
func (m *Module) Recover(_ context.Context) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.Hlab.IPv6Net != "" {
				continue
			}
			old := netip.PrefixFrom(formalIPv6(0x80000000, r.ID), 64).Masked()
			cur := r.IPv6Net().Addr().As16()
			changed := false
			for i, e := range r.DNSPtr {
				a, err := netip.ParseAddr(e.IP)
				if err != nil || !old.Contains(a) {
					continue
				}
				b := a.As16()
				copy(b[:8], cur[:8])
				r.DNSPtr[i].IP = netip.AddrFrom16(b).String()
				changed = true
			}
			if changed {
				if err := put(tx, r); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
