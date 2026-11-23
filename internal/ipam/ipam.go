// Package ipam hands out the public IPv4 addresses of the public range.
// Allocation starts at the range's fifth address (the first four are the
// transit network between host and appliance), continues after the last
// address handed out and wraps to the start only at the end of the range,
// skipping addresses in use and the storage VM's address, so a released
// address is not reused at once.
package ipam

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/transit"
)

const (
	// lastKey holds the last address handed out.
	lastKey = "ipam.public.last"
	// usedPrefix + address marks an address in use.
	usedPrefix = "ipam.public.used."
	// rangeKey holds the public range recorded at the last cloud start.
	rangeKey = "ipam.public.range"
	// firstOffset skips the transit /30 at the start of the range.
	firstOffset = 4
)

// ErrFull reports that every address of the range is in use.
var ErrFull = errors.New("ipam: no free public IPv4 address")

// NextPublic marks the next free address of r in use and returns it.
func NextPublic(tx store.Tx, r netip.Prefix) (netip.Addr, error) {
	r = r.Masked()
	first := r.Addr()
	for range firstOffset {
		first = first.Next()
	}
	last := transit.Broadcast(r).Prev() // the broadcast address is never handed out
	storage := transit.For(r).Storage
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
		used := a == storage
		if !used {
			var err error
			if used, err = inUse(tx, a); err != nil {
				return netip.Addr{}, err
			}
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

// Outside returns the public IPv4s in use outside r and the range recorded
// by the last RecordRange (invalid if none).
func Outside(tx store.Tx, r netip.Prefix) (used []netip.Addr, recorded netip.Prefix, err error) {
	r = r.Masked()
	keys, err := store.KVKeys(tx, usedPrefix)
	if err != nil {
		return nil, netip.Prefix{}, err
	}
	for _, k := range keys {
		a, perr := netip.ParseAddr(strings.TrimPrefix(k, usedPrefix))
		if perr != nil {
			return nil, netip.Prefix{}, fmt.Errorf("ipam: invalid key %q: %w", k, perr)
		}
		if !r.Contains(a) {
			used = append(used, a)
		}
	}
	v, err := store.GetKV(tx, rangeKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, netip.Prefix{}, err
	default:
		if p, perr := netip.ParsePrefix(v); perr == nil {
			recorded = p
		}
	}
	return used, recorded, nil
}

// RecordRange records r as the public range the state's addresses belong to.
func RecordRange(tx store.Tx, r netip.Prefix) error {
	return store.SetKV(tx, rangeKey, r.Masked().String())
}

func inUse(tx store.Tx, a netip.Addr) (bool, error) {
	_, err := store.GetKV(tx, usedPrefix+a.String())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
