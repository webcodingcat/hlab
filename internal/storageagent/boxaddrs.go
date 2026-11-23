package storageagent

import (
	"net/netip"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// staleBoxAddrs returns the box addresses among have, the IPv4 prefixes on
// the storage VM's interface, that no box of boxes holds any more: the
// /32s inside public other than own, the VM's own address.
//
//nolint:unused // used on Linux
func staleBoxAddrs(own netip.Addr, public netip.Prefix, have []netip.Prefix, boxes []agentapi.Box) []netip.Prefix {
	wanted := map[netip.Addr]bool{}
	for _, b := range boxes {
		wanted[b.IP] = true
	}
	var stale []netip.Prefix
	for _, p := range have {
		a := p.Addr()
		if p.Bits() == 32 && a.Is4() && a != own && public.Contains(a) && !wanted[a] {
			stale = append(stale, p)
		}
	}
	return stale
}
