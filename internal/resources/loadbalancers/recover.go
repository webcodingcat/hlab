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
// nothing in the old /64 and changes nothing. A target, service or network
// action interrupted by a stop has been marked error, so every pending
// state it left is dropped; GET and the agent follow the committed state.
func (m *Module) Recover(_ context.Context) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			changed := r.Hlab.Pending != nil
			r.Hlab.Pending = nil
			if r.Hlab.IPv6Net == "" {
				changed = moveFormalPTRs(&r) || changed
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

// moveFormalPTRs moves the dns_ptr entries of r in its old formal /64 to
// the new one and reports whether it moved one.
func moveFormalPTRs(r *Record) bool {
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
	return changed
}
