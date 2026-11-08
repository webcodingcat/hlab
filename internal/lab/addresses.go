package lab

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/state"
)

// The ranges of the Primary and Floating IPs, which share them: IPv4 addresses from
// 198.19.0.0/16, and one /64 per IPv6 from 2001:db8:1::/48.
var (
	publicIPv4Range = netip.MustParsePrefix("198.19.0.0/16")
	publicIPv6Range = netip.MustParsePrefix("2001:db8:1::/48")
)

// ErrNoAddress reports that a range has no free address left.
var ErrNoAddress = errors.New("the lab has no free public address left")

// NewAddress returns the next free public address of a type: an IPv4
// address, or an IPv6 /64 as "2001:db8:1:5::/64". Addresses go out in
// order; one that was released comes back only once the pool has gone
// round. IPv4 addresses ending in .0 or .255 are left out, which some
// tools take for a network or broadcast address. The caller puts the
// Primary or Floating IP that holds it in the same Tx.
func NewAddress(tx *state.Tx, ipType state.IPType) (string, error) {
	used := map[string]bool{}
	for _, ip := range state.All(tx, state.PrimaryIPs) {
		used[ip.IP] = true
	}
	for _, ip := range state.All(tx, state.FloatingIPs) {
		used[ip.IP] = true
	}
	pools := &tx.Lab().Pools
	next, size := &pools.NextIPv4, 1<<(32-publicIPv4Range.Bits())
	if ipType == state.IPv6 {
		next, size = &pools.NextIPv6, 1<<(64-publicIPv6Range.Bits())
	}
	for range size {
		offset := *next % size
		*next = offset + 1
		address, ok := poolAddress(ipType, offset)
		if ok && !used[address] {
			return address, nil
		}
	}
	return "", fmt.Errorf("%s: %w", ipType, ErrNoAddress)
}

// poolAddress returns the address at an offset into a type's range; ok is
// false for one that is not handed out.
func poolAddress(ipType state.IPType, offset int) (address string, ok bool) {
	if ipType == state.IPv6 {
		if offset == 0 {
			return "", false // 2001:db8:1::/64, the range's own first network
		}
		bytes := publicIPv6Range.Addr().As16()
		bytes[6], bytes[7] = byte(offset>>8), byte(offset)
		return netip.PrefixFrom(netip.AddrFrom16(bytes), 64).String(), true
	}
	if last := offset & 0xff; last == 0 || last == 0xff {
		return "", false
	}
	bytes := publicIPv4Range.Addr().As4()
	bytes[2], bytes[3] = byte(offset>>8), byte(offset)
	return netip.AddrFrom4(bytes).String(), true
}
