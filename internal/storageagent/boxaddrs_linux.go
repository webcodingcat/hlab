//go:build linux

package storageagent

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/transit"
)

// applyBoxAddrs puts the IP of each box of doc not in skipped as a /32 on
// the interface carrying the VM's own address (transit Addrs.Storage) and
// removes the /32s of boxes no longer in doc; the VM's own address is never
// touched. It returns the boxes whose address could not be added, and a
// failure that concerns no single box.
func applyBoxAddrs(doc agentapi.Storage, skipped map[string]bool) (map[string]error, error) {
	own := transit.For(doc.PublicRange).Storage
	link, err := linkOf(own)
	if err != nil {
		return nil, err
	}
	list, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	var have []netip.Prefix
	for _, a := range list {
		if p, ok := prefixOf(a.IPNet); ok {
			have = append(have, p)
		}
	}
	var errs []error
	for _, p := range staleBoxAddrs(own, doc.PublicRange, have, doc.Boxes) {
		if err := netlink.AddrDel(link, &netlink.Addr{IPNet: ipNet(p)}); err != nil && !errors.Is(err, unix.EADDRNOTAVAIL) {
			errs = append(errs, fmt.Errorf("remove address %s: %w", p, err))
		}
	}
	failed := map[string]error{}
	for _, b := range doc.Boxes {
		if skipped[b.User] {
			continue
		}
		if !b.IP.Is4() || !doc.PublicRange.Contains(b.IP) || b.IP == own {
			failed[b.User] = fmt.Errorf("invalid box address %s", b.IP)
			continue
		}
		p := netip.PrefixFrom(b.IP, 32)
		if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: ipNet(p)}); err != nil {
			failed[b.User] = fmt.Errorf("add address %s: %w", p, err)
		}
	}
	return failed, errors.Join(errs...)
}

// linkOf returns the interface carrying the IPv4 address own.
func linkOf(own netip.Addr) (netlink.Link, error) {
	list, err := netlink.AddrList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("list addresses: %w", err)
	}
	for _, a := range list {
		if p, ok := prefixOf(a.IPNet); ok && p.Addr() == own {
			link, err := netlink.LinkByIndex(a.LinkIndex)
			if err != nil {
				return nil, fmt.Errorf("interface of %s: %w", own, err)
			}
			return link, nil
		}
	}
	return nil, fmt.Errorf("no interface carries the storage address %s", own)
}

// prefixOf converts an IPv4 net.IPNet.
func prefixOf(n *net.IPNet) (netip.Prefix, bool) {
	if n == nil {
		return netip.Prefix{}, false
	}
	a, ok := netip.AddrFromSlice(n.IP.To4())
	if !ok {
		return netip.Prefix{}, false
	}
	bits, _ := n.Mask.Size()
	return netip.PrefixFrom(a, bits), true
}

// ipNet converts an IPv4 prefix to a net.IPNet.
func ipNet(p netip.Prefix) *net.IPNet {
	return &net.IPNet{IP: net.IP(p.Addr().AsSlice()), Mask: net.CIDRMask(p.Bits(), 32)}
}
