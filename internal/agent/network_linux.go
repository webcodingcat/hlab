//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// routeProtocol tags the routes the agent creates, so it removes only its
// own routes and never those of the system (static, kernel, dhcp).
const routeProtocol netlink.RouteProtocol = 200

// fixedAddrs are the appliance's addresses besides its transit address:
// the guests' gateway, the metadata service and Hetzner's resolvers.
var fixedAddrs = []netip.Addr{
	netip.MustParseAddr("172.31.1.1"),
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("185.12.64.1"),
	netip.MustParseAddr("185.12.64.2"),
}

// Network is the Applier of the public segment on one interface: fixed
// addresses, IPv4 forwarding, proxy ARP, one /32 route per server and one
// per assigned floating IP.
type Network struct {
	Interface string
}

// NewNetwork returns the Applier of the public segment on iface.
func NewNetwork(iface string) *Network { return &Network{Interface: iface} }

// Name implements Applier.
func (n *Network) Name() string { return "network" }

// Apply implements Applier.
func (n *Network) Apply(_ context.Context, cfg agentapi.Config) error {
	link, err := netlink.LinkByName(n.Interface)
	if err != nil {
		return fmt.Errorf("interface %s: %w", n.Interface, err)
	}
	for _, a := range fixedAddrs {
		if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: hostNet(a)}); err != nil {
			return fmt.Errorf("address %s: %w", a, err)
		}
	}

	// Guests sit on one PVLAN segment and reach each other only through the
	// appliance: it answers ARP for their addresses on the same interface
	// (proxy_arp_pvlan) and must not tell them to talk directly (redirects).
	conf := filepath.Join("/proc/sys/net/ipv4/conf", n.Interface)
	for path, value := range map[string]string{
		"/proc/sys/net/ipv4/ip_forward":              "1",
		"/proc/sys/net/ipv4/conf/all/send_redirects": "0",
		filepath.Join(conf, "send_redirects"):        "0",
		filepath.Join(conf, "proxy_arp"):             "1",
		filepath.Join(conf, "proxy_arp_pvlan"):       "1",
	} {
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			return fmt.Errorf("sysctl: %w", err)
		}
	}

	return n.syncRoutes(link, cfg)
}

// syncRoutes makes the agent's routes on link exactly one /32 per server
// public IPv4 and one onlink /32 per assigned floating IP via its target's
// public IPv4.
func (n *Network) syncRoutes(link netlink.Link, cfg agentapi.Config) error {
	idx := link.Attrs().Index
	// want maps a destination to its gateway; an invalid gateway means the
	// destination is on the link itself.
	want := map[netip.Addr]netip.Addr{}
	for _, s := range cfg.Servers {
		if s.PublicIPv4.Is4() {
			want[s.PublicIPv4] = netip.Addr{}
		}
	}
	for _, f := range cfg.FloatingIPs {
		if f.IP.Is4() && f.Target.Is4() {
			want[f.IP] = f.Target
		}
	}

	have, err := netlink.RouteListFiltered(netlink.FAMILY_V4, &netlink.Route{Protocol: routeProtocol}, netlink.RT_FILTER_PROTOCOL)
	if err != nil {
		return fmt.Errorf("list routes: %w", err)
	}
	for _, r := range have {
		if r.Dst != nil && r.LinkIndex == idx {
			if ones, _ := r.Dst.Mask.Size(); ones == 32 {
				dst, _ := netip.AddrFromSlice(r.Dst.IP.To4())
				gw, _ := netip.AddrFromSlice(r.Gw.To4())
				if g, ok := want[dst]; ok && g == gw {
					continue
				}
			}
		}
		if err := netlink.RouteDel(&r); err != nil {
			return fmt.Errorf("delete route %s: %w", r.Dst, err)
		}
	}

	for a, gw := range want {
		r := &netlink.Route{
			LinkIndex: idx,
			Dst:       hostNet(a),
			Scope:     netlink.SCOPE_LINK,
			Protocol:  routeProtocol,
			Table:     unix.RT_TABLE_MAIN,
		}
		if gw.IsValid() {
			// The floating IP goes to the target server's adapter: the
			// gateway is that server's public IPv4, on the same link.
			r.Scope = netlink.SCOPE_UNIVERSE
			r.Gw = gw.AsSlice()
			r.SetFlag(netlink.FLAG_ONLINK)
		}
		if err := netlink.RouteReplace(r); err != nil {
			return fmt.Errorf("route %s: %w", a, err)
		}
	}
	return nil
}

func hostNet(a netip.Addr) *net.IPNet {
	return &net.IPNet{IP: a.AsSlice(), Mask: net.CIDRMask(32, 32)}
}
