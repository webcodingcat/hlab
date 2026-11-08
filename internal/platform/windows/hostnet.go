//go:build windows

package windows

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/windows/pshost"
)

//go:embed hostnet.ps1
var hostNetScript string

// nrptComment marks the NRPT rules of the lab, so that HostDNS finds them
// again, also those of a crashed HLab.
const nrptComment = "hlab"

// newHostShell returns the PowerShell process for hostnet.ps1. HostNet and
// HostDNS share it: they run rarely, and one process is enough.
func newHostShell(logger *slog.Logger) *pshost.Host {
	return pshost.New(hostNetScript, pshost.Options{Processes: 1, Logger: logger})
}

// HostNet is the host's side of the lab on Windows: the Hyper-V switches and
// the addresses and routes of the host's NIC on the internal one. It needs
// administrator rights.
type HostNet struct {
	shell *pshost.Host

	mu   sync.Mutex
	link *platform.LinkSpec // what SetUp set up, for TearDown
}

// NewHostNet returns the host network; logger receives stray PowerShell
// output.
func NewHostNet(logger *slog.Logger) *HostNet {
	return &HostNet{shell: newHostShell(logger)}
}

// Close stops the PowerShell process.
func (n *HostNet) Close() error {
	return n.shell.Close()
}

// usedRange is a route or an address of the host, as hostnet.ps1 lists them.
type usedRange struct {
	Prefix    string `json:"prefix"`
	Interface string `json:"interface"`
}

// CheckRanges fails with ErrInUse if a route or an address outside the lab's
// switches overlaps one of the ranges.
func (n *HostNet) CheckRanges(ctx context.Context, ranges []netip.Prefix) error {
	var used []usedRange
	if err := call(ctx, n.shell, "used-ranges", map[string]string{"switch_prefix": platform.SwitchPrefix}, &used); err != nil {
		return err
	}
	return overlapping(ranges, used)
}

// overlapping fails with ErrInUse, naming the range and the interface, if a
// used range overlaps a wanted one. Default routes overlap everything and do
// not count.
func overlapping(wanted []netip.Prefix, used []usedRange) error {
	for _, entry := range used {
		prefix, err := netip.ParsePrefix(entry.Prefix)
		if err != nil || prefix.Bits() == 0 {
			continue // not an address range of ours to worry about
		}
		for _, lab := range wanted {
			if lab.Overlaps(prefix.Masked()) {
				return fmt.Errorf("the lab's range %s overlaps %s on %q: %w", lab, entry.Prefix, entry.Interface, platform.ErrInUse)
			}
		}
	}
	return nil
}

// routeArgs is a route as hostnet.ps1 takes it.
type routeArgs struct {
	Prefix  string `json:"prefix"`
	NextHop string `json:"next_hop"`
}

// SetUp creates the internal switch with the host's NIC and the private
// switches, or takes them over, and makes the NIC's addresses and routes
// those of the link.
func (n *HostNet) SetUp(ctx context.Context, link platform.LinkSpec) error {
	addresses := []string{}
	for _, address := range link.Addresses {
		addresses = append(addresses, address.String())
	}
	routes := []routeArgs{}
	for _, route := range link.Routes {
		gateway, err := gatewayFor(route, link.Gateways)
		if err != nil {
			return err
		}
		routes = append(routes, routeArgs{Prefix: route.String(), NextHop: gateway.String()})
	}
	privateSwitches := append([]string{}, link.PrivateSwitches...)
	n.mu.Lock()
	defer n.mu.Unlock()
	// remembered first: TearDown removes what a SetUp that failed half way
	// created
	n.link = &link
	return call(ctx, n.shell, "set-up", map[string]any{
		"switch": link.Switch, "private_switches": privateSwitches,
		"addresses": addresses, "routes": routes,
	}, nil)
}

// gatewayFor returns the gateway of the route's address family.
func gatewayFor(route netip.Prefix, gateways []netip.Addr) (netip.Addr, error) {
	for _, gateway := range gateways {
		if gateway.Is4() == route.Addr().Is4() {
			return gateway, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("the route %s has no gateway of its address family", route)
}

// TearDown removes the switches of the last SetUp, and with the internal
// one the host's NIC, its addresses and routes.
func (n *HostNet) TearDown(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.link == nil {
		return nil
	}
	switches := append([]string{n.link.Switch}, n.link.PrivateSwitches...)
	if err := call(ctx, n.shell, "tear-down", map[string]any{"switches": switches}, nil); err != nil {
		return err
	}
	n.link = nil
	return nil
}

// HostDNS is the NRPT rules that send the host's lookups below the lab's
// domains to the lab's resolver. It needs administrator rights.
type HostDNS struct {
	shell *pshost.Host
}

// NewHostDNS returns the host's DNS rules; logger receives stray PowerShell
// output.
func NewHostDNS(logger *slog.Logger) *HostDNS {
	return &HostDNS{shell: newHostShell(logger)}
}

// Close stops the PowerShell process.
func (d *HostDNS) Close() error {
	return d.shell.Close()
}

// nrptRule is an NRPT rule as hostnet.ps1 takes it.
type nrptRule struct {
	Namespace string   `json:"namespace"`
	Servers   []string `json:"servers"`
}

// Apply makes the rules the lab's whole set of NRPT rules. A namespace with
// a leading dot covers the domain and every name below it.
func (d *HostDNS) Apply(ctx context.Context, rules []platform.DNSRule) error {
	wanted := []nrptRule{}
	for _, rule := range rules {
		servers := []string{}
		for _, server := range rule.Servers {
			servers = append(servers, server.String())
		}
		wanted = append(wanted, nrptRule{Namespace: "." + strings.TrimPrefix(rule.Domain, "."), Servers: servers})
	}
	return call(ctx, d.shell, "dns-apply", map[string]any{"rules": wanted, "comment": nrptComment}, nil)
}

// Clear removes every NRPT rule of the lab.
func (d *HostDNS) Clear(ctx context.Context) error {
	return d.Apply(ctx, nil)
}
