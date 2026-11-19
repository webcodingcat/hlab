//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// VRF table numbers are 1000 plus the network's VLAN (2..4092).
const (
	vrfTableBase = 1000
	minVLAN      = 2
	maxVLAN      = 4092
)

// unreachableMetric is the metric of the unreachable default route in
// every VRF table, as the kernel documentation recommends: a lookup that
// misses the VRF table ends there instead of falling through to the main
// table, so nothing leaks out of a private network.
const unreachableMetric = 4278198272

// VRF is the Applier of private networks on the trunk link (found by its
// MAC): per Network a VLAN subinterface t<vlan> enslaved to the VRF device
// vrf<vlan> (table 1000+vlan) with the network gateway as /32 address,
// /32 routes to the members and their alias IPs, the network's routes via
// their gateway, and no default route. It touches only the links named
// vrf<vlan> and t<vlan> and the routes carrying the agent's protocol in the
// VRF tables. With Metadata set, each VRF device also carries
// 169.254.169.254/32 and gets its own metadata listener on
// 169.254.169.254:80, bound to the VRF device and serving
// Metadata.HandlerFor(vlan); the listener closes with its VRF.
type VRF struct {
	TrunkMAC net.HardwareAddr
	Metadata *Metadata

	listeners map[int]*http.Server // per VLAN
}

// NewVRF returns the Applier of private networks on the link with
// trunkMAC.
func NewVRF(trunkMAC net.HardwareAddr) *VRF { return &VRF{TrunkMAC: trunkMAC} }

// Name implements Applier.
func (v *VRF) Name() string { return "vrf" }

// Apply implements Applier.
func (v *VRF) Apply(_ context.Context, cfg agentapi.Config) error {
	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("vrf: list links: %w", err)
	}
	byName := map[string]netlink.Link{}
	for _, l := range links {
		byName[l.Attrs().Name] = l
	}

	want := map[int]bool{}
	for _, n := range cfg.Networks {
		want[n.VLAN] = true
	}
	for vlan, srv := range v.listeners {
		if !want[vlan] {
			_ = srv.Close()
			delete(v.listeners, vlan)
		}
	}
	// Subinterfaces go before their VRF devices.
	for _, kind := range [][2]string{{"t", "vlan"}, {"vrf", "vrf"}} {
		for _, l := range links {
			vlan, ok := agentVLAN(l.Attrs().Name, kind[0])
			if !ok || want[vlan] || l.Type() != kind[1] {
				continue
			}
			if err := netlink.LinkDel(l); err != nil {
				return fmt.Errorf("vrf: delete link %s: %w", l.Attrs().Name, err)
			}
		}
	}

	var routes []netlink.Route
	if len(cfg.Networks) > 0 {
		trunk, err := v.trunk(links)
		if err != nil {
			return err
		}
		for _, n := range cfg.Networks {
			rs, err := setupNetwork(trunk, byName, n)
			if err == nil {
				err = v.listenMetadata(n.VLAN)
			}
			if err != nil {
				return fmt.Errorf("vrf: network %d (vlan %d): %w", n.ID, n.VLAN, err)
			}
			routes = append(routes, rs...)
		}
	}
	return syncVRFRoutes(routes)
}

// listenMetadata starts the metadata listener of VLAN vlan on
// 169.254.169.254:80 bound to the VRF device vrf<vlan>, unless it runs.
func (v *VRF) listenMetadata(vlan int) error {
	if v.Metadata == nil || v.listeners[vlan] != nil {
		return nil
	}
	dev := "vrf" + strconv.Itoa(vlan)
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, dev)
		}); err != nil {
			return err
		}
		return serr
	}}
	ln, err := lc.Listen(context.Background(), "tcp4", netip.AddrPortFrom(metadataAddr, 80).String())
	if err != nil {
		return fmt.Errorf("metadata listener on %s: %w", dev, err)
	}
	srv := &http.Server{Handler: v.Metadata.HandlerFor(vlan), ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metadata listener", "vrf", dev, "err", err)
		}
	}()
	if v.listeners == nil {
		v.listeners = map[int]*http.Server{}
	}
	v.listeners[vlan] = srv
	return nil
}

// trunk finds the trunk link by MAC and sets it up.
func (v *VRF) trunk(links []netlink.Link) (netlink.Link, error) {
	for _, l := range links {
		if bytes.Equal(l.Attrs().HardwareAddr, v.TrunkMAC) && l.Type() == "device" {
			if err := netlink.LinkSetUp(l); err != nil {
				return nil, fmt.Errorf("vrf: trunk %s up: %w", l.Attrs().Name, err)
			}
			return l, nil
		}
	}
	return nil, fmt.Errorf("vrf: no trunk link with MAC %s", v.TrunkMAC)
}

// setupNetwork makes the VRF device and the VLAN subinterface of n, sets
// the gateway address and sysctls, and returns the routes n wants.
func setupNetwork(trunk netlink.Link, byName map[string]netlink.Link, n agentapi.Network) ([]netlink.Route, error) {
	if n.VLAN < minVLAN || n.VLAN > maxVLAN {
		return nil, fmt.Errorf("vlan out of range %d..%d", minVLAN, maxVLAN)
	}
	table := vrfTableBase + n.VLAN
	vrfName := "vrf" + strconv.Itoa(n.VLAN)
	subName := "t" + strconv.Itoa(n.VLAN)

	vrf, err := ensureLink(byName, &netlink.Vrf{LinkAttrs: netlink.LinkAttrs{Name: vrfName}, Table: uint32(table)})
	if err != nil {
		return nil, err
	}
	sub, err := ensureLink(byName, &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{Name: subName, ParentIndex: trunk.Attrs().Index},
		VlanId:    n.VLAN,
	})
	if err != nil {
		return nil, err
	}
	if sub.Attrs().MasterIndex != vrf.Attrs().Index {
		if err := netlink.LinkSetMasterByIndex(sub, vrf.Attrs().Index); err != nil {
			return nil, fmt.Errorf("enslave %s to %s: %w", subName, vrfName, err)
		}
	}
	if err := netlink.AddrReplace(sub, &netlink.Addr{IPNet: hostNet(n.Gateway)}); err != nil {
		return nil, fmt.Errorf("address %s on %s: %w", n.Gateway, subName, err)
	}
	// The metadata listener of the VRF answers on this address.
	if err := netlink.AddrReplace(vrf, &netlink.Addr{IPNet: hostNet(metadataAddr)}); err != nil {
		return nil, fmt.Errorf("address %s on %s: %w", metadataAddr, vrfName, err)
	}

	// Members reach each other only through the gateway: the subinterface
	// forwards, answers ARP for peers on the same link and sends no
	// redirects; the strict reverse path check would drop that same-link
	// forward path.
	conf := filepath.Join("/proc/sys/net/ipv4/conf", subName)
	for key, value := range map[string]string{
		"forwarding":       "1",
		"proxy_arp":        "1",
		"proxy_arp_pvlan":  "1",
		"send_redirects":   "0",
		"accept_redirects": "0",
		"rp_filter":        "0",
	} {
		if err := os.WriteFile(filepath.Join(conf, key), []byte(value), 0o644); err != nil {
			return nil, fmt.Errorf("sysctl: %w", err)
		}
	}
	for _, l := range []netlink.Link{vrf, sub} {
		if err := netlink.LinkSetUp(l); err != nil {
			return nil, fmt.Errorf("link %s up: %w", l.Attrs().Name, err)
		}
	}

	idx := sub.Attrs().Index
	routes := []netlink.Route{{
		Dst:      &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
		Type:     unix.RTN_UNREACHABLE,
		Priority: unreachableMetric,
		Protocol: routeProtocol,
		Table:    table,
	}}
	for _, m := range n.Members {
		for _, a := range append([]netip.Addr{m.IP}, m.AliasIPs...) {
			if !a.Is4() {
				continue
			}
			routes = append(routes, netlink.Route{
				LinkIndex: idx,
				Dst:       hostNet(a),
				Type:      unix.RTN_UNICAST,
				Scope:     netlink.SCOPE_LINK,
				Protocol:  routeProtocol,
				Table:     table,
			})
		}
	}
	for _, r := range n.Routes {
		if !r.Destination.Addr().Is4() || !r.Gateway.Is4() {
			continue
		}
		route := netlink.Route{
			LinkIndex: idx,
			Dst:       &net.IPNet{IP: r.Destination.Masked().Addr().AsSlice(), Mask: net.CIDRMask(r.Destination.Bits(), 32)},
			Gw:        r.Gateway.AsSlice(),
			Type:      unix.RTN_UNICAST,
			Protocol:  routeProtocol,
			Table:     table,
		}
		route.SetFlag(netlink.FLAG_ONLINK)
		routes = append(routes, route)
	}
	return routes, nil
}

// ensureLink returns the link named like want, adding it if it is missing.
func ensureLink(byName map[string]netlink.Link, want netlink.Link) (netlink.Link, error) {
	name := want.Attrs().Name
	if l, ok := byName[name]; ok {
		return l, nil
	}
	if err := netlink.LinkAdd(want); err != nil {
		return nil, fmt.Errorf("add link %s: %w", name, err)
	}
	l, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("link %s: %w", name, err)
	}
	byName[name] = l
	return l, nil
}

// syncVRFRoutes makes the agent's routes in the VRF tables exactly want.
func syncVRFRoutes(want []netlink.Route) error {
	keep := map[string]bool{}
	for _, r := range want {
		keep[routeKey(r)] = true
	}
	have, err := netlink.RouteListFiltered(netlink.FAMILY_V4,
		&netlink.Route{Protocol: routeProtocol, Table: unix.RT_TABLE_UNSPEC},
		netlink.RT_FILTER_PROTOCOL|netlink.RT_FILTER_TABLE)
	if err != nil {
		return fmt.Errorf("vrf: list routes: %w", err)
	}
	for _, r := range have {
		if r.Table < vrfTableBase+minVLAN || r.Table > vrfTableBase+maxVLAN || keep[routeKey(r)] {
			continue
		}
		if err := netlink.RouteDel(&r); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("vrf: delete route %s table %d: %w", r.Dst, r.Table, err)
		}
	}
	for i := range want {
		if err := netlink.RouteReplace(&want[i]); err != nil {
			return fmt.Errorf("vrf: route %s table %d: %w", want[i].Dst, want[i].Table, err)
		}
	}
	return nil
}

// routeKey identifies a route by table, type, destination, gateway and
// metric.
func routeKey(r netlink.Route) string {
	return fmt.Sprintf("%d|%d|%s|%s|%d", r.Table, r.Type, r.Dst, r.Gw, r.Priority)
}

// agentVLAN reports the VLAN of a link name made by the VRF applier:
// kind followed by a VLAN number without leading zeros.
func agentVLAN(name, kind string) (int, bool) {
	s, ok := strings.CutPrefix(name, kind)
	if !ok || s == "" || s[0] == '0' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < minVLAN || n > maxVLAN || strconv.Itoa(n) != s {
		return 0, false
	}
	return n, true
}
