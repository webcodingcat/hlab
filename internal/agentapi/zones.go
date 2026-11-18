package agentapi

import (
	"net/netip"
	"strconv"
	"strings"
)

// ReverseZones returns the in-addr.arpa zones covering an IPv4 prefix on
// octet boundaries, as fully qualified names: a /16 is one zone, a /20
// sixteen /24 zones. Prefixes longer than /24 are served as their whole /24
// zone. hlabd points NRPT rules at them and the agent serves them.
func ReverseZones(p netip.Prefix) []string {
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
