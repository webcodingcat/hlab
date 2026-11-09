package lab

import (
	"context"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/state"
)

// ChangeFloatingIP is the steps of a Floating IP's assign and unassign:
// the plan has changed the record, the appliance gets the pictures with
// the IP's new route.
func (m *Machine) ChangeFloatingIP(ctx context.Context) error {
	return m.UpdatePictures(ctx)
}

// floatingIPFacts returns what the pictures need of the assigned Floating
// IPs: an IPv4 as a /32, an IPv6 as its /64.
func floatingIPFacts(tx *state.Tx) []picture.FloatingIP {
	var facts []picture.FloatingIP
	for _, floating := range state.All(tx, state.FloatingIPs) {
		if floating.ServerID == 0 {
			continue
		}
		prefix, err := netip.ParsePrefix(floating.IP)
		if address, addrErr := netip.ParseAddr(floating.IP); addrErr == nil {
			prefix, err = netip.PrefixFrom(address, address.BitLen()), nil
		}
		if err == nil {
			facts = append(facts, picture.FloatingIP{Prefix: prefix, ServerID: floating.ServerID})
		}
	}
	return facts
}
