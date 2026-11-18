// Package ipam hands out the public IPv4 addresses of the public range.
// Allocation starts at the range's fifth address (the first four are the
// transit network between host and appliance), continues after the last
// address handed out and wraps to the start only at the end of the range,
// skipping addresses in use, so a released address is not reused at once.
package ipam

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/webcodingcat/hlab/internal/store"
)

const (
	// lastKey holds the last address handed out.
	lastKey = "ipam.public.last"
	// usedPrefix + address marks an address in use.
	usedPrefix = "ipam.public.used."
	// firstOffset skips the transit /30 at the start of the range.
	firstOffset = 4
)

// ErrFull reports that every address of the range is in use.
var ErrFull = errors.New("ipam: no free public IPv4 address")

// NextPublic marks the next free address of r in use and returns it.
func NextPublic(tx store.Tx, r netip.Prefix) (netip.Addr, error) {
	r = r.Masked()
	if !r.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("ipam: %s is not an IPv4 range", r)
	}
	first := r.Addr()
	for range firstOffset {
		first = first.Next()
	}
	last := broadcast(r).Prev() // the broadcast address is never handed out
	if !r.Contains(first) || last.Less(first) {
		return netip.Addr{}, ErrFull
	}

	start := first
	if v, err := store.GetKV(tx, lastKey); err == nil {
		if a, perr := netip.ParseAddr(v); perr == nil && r.Contains(a) && !a.Less(first) && a.Less(last) {
			start = a.Next()
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return netip.Addr{}, err
	}

	a := start
	for {
		used, err := inUse(tx, a)
		if err != nil {
			return netip.Addr{}, err
		}
		if !used {
			if err := store.SetKV(tx, usedPrefix+a.String(), "1"); err != nil {
				return netip.Addr{}, err
			}
			if err := store.SetKV(tx, lastKey, a.String()); err != nil {
				return netip.Addr{}, err
			}
			return a, nil
		}
		if a == last {
			a = first
		} else {
			a = a.Next()
		}
		if a == start {
			return netip.Addr{}, ErrFull
		}
	}
}

// Release frees a; releasing a free address is no error.
func Release(tx store.Tx, a netip.Addr) error {
	return store.DeleteKV(tx, usedPrefix+a.String())
}

func inUse(tx store.Tx, a netip.Addr) (bool, error) {
	_, err := store.GetKV(tx, usedPrefix+a.String())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// broadcast returns the last address of r.
func broadcast(r netip.Prefix) netip.Addr {
	b := r.Addr().As4()
	host := 32 - r.Bits()
	for i := 3; i >= 0 && host > 0; i-- {
		n := min(host, 8)
		b[i] |= byte(1<<n - 1)
		host -= n
	}
	return netip.AddrFrom4(b)
}
