//go:build linux

package agent

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"

	"github.com/vishvananda/netlink"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// routeProtocol marks the routes the agent sets, so that it tells them from
// the kernel's and networkd's (on the uplink) and removes only its own.
const routeProtocol netlink.RouteProtocol = 72

// applyLinks makes the appliance's links match the picture. The steps
// follow what each needs: a NIC's name before its VLANs, the VLANs before
// their sysctls, the sysctls before a link goes up, addresses before the
// routes whose source they are. It is not atomic: when a step fails, the
// steps before it stay applied, and hlabd sends the picture again.
func (a *Agent) applyLinks(links agentproto.Links) error {
	if err := renameInterfaces(links.Interfaces); err != nil {
		return err
	}
	if err := applyVRFs(links.VRFs); err != nil {
		return err
	}
	if err := applyVLANs(links.VLANs); err != nil {
		return err
	}
	for _, sysctl := range links.Sysctls {
		path := filepath.Join("/proc/sys", sysctl.Key)
		if err := os.WriteFile(path, []byte(sysctl.Value), 0o644); err != nil {
			return fmt.Errorf("sysctl %s: %w", sysctl.Key, err)
		}
	}
	// the devices the agent manages, which it sets up: all but the NICs of
	// networkd, and lo too. systemd sets lo up before the agent starts, but
	// a fresh network namespace has it down, and the kernel gives lo its
	// loopback addresses only when it comes up, before any other address
	managed := []string{"lo"}
	for _, nic := range links.Interfaces {
		if !nic.Networkd {
			managed = append(managed, nic.Name)
		}
	}
	for _, vrf := range links.VRFs {
		managed = append(managed, vrf.Name)
	}
	for _, vlan := range links.VLANs {
		managed = append(managed, vlan.Name)
	}
	for _, name := range managed {
		link, err := netlink.LinkByName(name)
		if err == nil {
			err = netlink.LinkSetUp(link)
		}
		if err != nil {
			return fmt.Errorf("setting %s up: %w", name, err)
		}
	}
	if err := applyAddresses(managed, links.Addresses); err != nil {
		return err
	}
	if err := applyRoutes(links.Routes); err != nil {
		return err
	}
	a.announcer.set(links.Addresses)
	return nil
}

// renameInterfaces finds each NIC by its MAC address and gives it its name.
// A NIC must be down to be renamed; one that was up goes up again at once.
// The agent's own NICs go up with the other links anyway, but networkd's
// uplink would stay down: networkd does not undo what another process did.
func renameInterfaces(nics []agentproto.Interface) error {
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	byMAC := map[string]netlink.Link{}
	for _, link := range links {
		// a VLAN has the MAC address of its NIC
		if link.Type() != "vlan" {
			byMAC[link.Attrs().HardwareAddr.String()] = link
		}
	}
	found := map[string]netlink.Link{}
	for _, nic := range nics {
		mac, _ := net.ParseMAC(nic.MAC) // Check made sure it parses
		link, exists := byMAC[mac.String()]
		if !exists {
			return refused(fmt.Errorf("interface %s: no NIC has the MAC %s", nic.Name, mac))
		}
		found[nic.Name] = link
	}
	for _, nic := range nics {
		link := found[nic.Name]
		if link.Attrs().Name == nic.Name {
			continue
		}
		if err := netlink.LinkSetDown(link); err != nil {
			return fmt.Errorf("renaming %s to %s: %w", link.Attrs().Name, nic.Name, err)
		}
		if err := netlink.LinkSetName(link, nic.Name); err != nil {
			return fmt.Errorf("renaming %s to %s: %w", link.Attrs().Name, nic.Name, err)
		}
		if link.Attrs().Flags&net.FlagUp == 0 {
			continue
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("setting %s up again after renaming it: %w", nic.Name, err)
		}
	}
	return nil
}

// applyVRFs creates the picture's VRFs and deletes every other one.
func applyVRFs(vrfs []agentproto.VRF) error {
	wanted := map[string]uint32{}
	for _, vrf := range vrfs {
		wanted[vrf.Name] = vrf.Table
	}
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	for _, link := range links {
		vrf, isVRF := link.(*netlink.Vrf)
		if !isVRF {
			continue
		}
		if table, keep := wanted[vrf.Name]; keep && table == vrf.Table {
			delete(wanted, vrf.Name)
			continue
		}
		if err := netlink.LinkDel(vrf); err != nil {
			return fmt.Errorf("deleting VRF %s: %w", vrf.Name, err)
		}
	}
	for _, vrf := range vrfs {
		if _, missing := wanted[vrf.Name]; !missing {
			continue
		}
		link := &netlink.Vrf{LinkAttrs: netlink.LinkAttrs{Name: vrf.Name}, Table: vrf.Table}
		if err := netlink.LinkAdd(link); err != nil {
			return fmt.Errorf("creating VRF %s: %w", vrf.Name, err)
		}
	}
	return nil
}

// applyVLANs creates the picture's VLANs, puts each into its VRF and
// deletes every other VLAN.
func applyVLANs(vlans []agentproto.VLAN) error {
	links, err := netlink.LinkList()
	if err != nil {
		return err
	}
	byName := map[string]netlink.Link{}
	for _, link := range links {
		byName[link.Attrs().Name] = link
	}
	wanted := map[string]agentproto.VLAN{}
	for _, vlan := range vlans {
		wanted[vlan.Name] = vlan
	}
	for _, link := range links {
		existing, isVLAN := link.(*netlink.Vlan)
		if !isVLAN {
			continue
		}
		vlan, keep := wanted[existing.Name]
		if keep && existing.VlanId == vlan.ID &&
			existing.ParentIndex == byName[vlan.Parent].Attrs().Index {
			continue
		}
		if err := netlink.LinkDel(existing); err != nil {
			return fmt.Errorf("deleting VLAN %s: %w", existing.Name, err)
		}
		delete(byName, existing.Name)
	}
	for _, vlan := range vlans {
		if _, exists := byName[vlan.Name]; !exists {
			link := &netlink.Vlan{
				LinkAttrs: netlink.LinkAttrs{Name: vlan.Name, ParentIndex: byName[vlan.Parent].Attrs().Index},
				VlanId:    vlan.ID,
			}
			if err := netlink.LinkAdd(link); err != nil {
				return fmt.Errorf("creating VLAN %s: %w", vlan.Name, err)
			}
		}
		if err := setVRF(vlan); err != nil {
			return err
		}
	}
	return nil
}

// setVRF puts a VLAN into its VRF, or into none.
func setVRF(vlan agentproto.VLAN) error {
	link, err := netlink.LinkByName(vlan.Name)
	if err != nil {
		return err
	}
	if vlan.VRF == "" {
		if link.Attrs().MasterIndex != 0 {
			err = netlink.LinkSetNoMaster(link)
		}
	} else {
		var vrf netlink.Link
		vrf, err = netlink.LinkByName(vlan.VRF)
		if err == nil && link.Attrs().MasterIndex != vrf.Attrs().Index {
			err = netlink.LinkSetMasterByIndex(link, vrf.Attrs().Index)
		}
	}
	if err != nil {
		return fmt.Errorf("putting VLAN %s into VRF %q: %w", vlan.Name, vlan.VRF, err)
	}
	return nil
}

// applyAddresses gives each device the picture's addresses and removes
// every other address from it; lo keeps its loopback addresses.
func applyAddresses(devices []string, addresses []agentproto.Address) error {
	wanted := map[string]map[netip.Prefix]bool{}
	for _, device := range devices {
		wanted[device] = map[netip.Prefix]bool{}
	}
	for _, address := range addresses {
		wanted[address.Device][address.Prefix] = true
	}
	for _, device := range devices {
		link, err := netlink.LinkByName(device)
		if err != nil {
			return fmt.Errorf("addresses of %s: %w", device, err)
		}
		current, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return fmt.Errorf("addresses of %s: %w", device, err)
		}
		for _, address := range current {
			prefix := toPrefix(address.IPNet)
			if wanted[device][prefix] {
				delete(wanted[device], prefix)
				continue
			}
			if device == "lo" && prefix.Addr().IsLoopback() {
				continue
			}
			if err := netlink.AddrDel(link, &address); err != nil {
				return fmt.Errorf("removing %s from %s: %w", prefix, device, err)
			}
		}
		for prefix := range wanted[device] {
			if err := netlink.AddrAdd(link, toAddr(prefix)); err != nil {
				return fmt.Errorf("adding %s to %s: %w", prefix, device, err)
			}
		}
	}
	return nil
}

// applyRoutes sets the picture's routes and removes the other routes the
// agent set before.
func applyRoutes(routes []agentproto.Route) error {
	type key struct {
		table       int
		destination netip.Prefix
	}
	wanted := map[key]netlink.Route{}
	for _, route := range routes {
		link, err := netlink.LinkByName(route.Device)
		if err != nil {
			return fmt.Errorf("route %s: %w", route.Destination, err)
		}
		table := int(route.Table)
		if table == 0 {
			table = syscall.RT_TABLE_MAIN
		}
		wanted[key{table, route.Destination}] = toRoute(route, link.Attrs().Index, table)
	}
	filter := &netlink.Route{Protocol: routeProtocol}
	current, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, filter,
		netlink.RT_FILTER_PROTOCOL|netlink.RT_FILTER_TABLE)
	if err != nil {
		return err
	}
	for _, route := range current {
		destination := toPrefix(route.Dst)
		if route.Dst == nil { // netlink leaves out a default route's destination
			destination = netip.PrefixFrom(netip.IPv6Unspecified(), 0)
			if route.Family == netlink.FAMILY_V4 {
				destination = netip.PrefixFrom(netip.IPv4Unspecified(), 0)
			}
		}
		if _, keep := wanted[key{route.Table, destination}]; keep {
			continue
		}
		if err := netlink.RouteDel(&route); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("removing route %s: %w", route.Dst, err)
		}
	}
	for _, route := range wanted {
		if err := netlink.RouteReplace(&route); err != nil {
			return fmt.Errorf("route %s: %w", route.Dst, err)
		}
	}
	return nil
}

// toRoute returns the route for netlink.
func toRoute(route agentproto.Route, linkIndex, table int) netlink.Route {
	result := netlink.Route{
		LinkIndex: linkIndex,
		Dst:       toIPNet(route.Destination),
		Table:     table,
		Protocol:  routeProtocol,
	}
	if route.Gateway.IsValid() {
		result.Gw = route.Gateway.AsSlice()
	} else if route.Destination.Addr().Is4() {
		result.Scope = netlink.SCOPE_LINK
	}
	if route.Source.IsValid() {
		result.Src = route.Source.AsSlice()
	}
	return result
}

// toAddr returns an address for netlink. IPv6 addresses skip duplicate
// address detection: the appliance is the only holder, and an address in
// detection cannot be a route's source yet.
func toAddr(prefix netip.Prefix) *netlink.Addr {
	address := &netlink.Addr{IPNet: toIPNet(prefix)}
	if prefix.Addr().Is6() {
		address.Flags = syscall.IFA_F_NODAD
	}
	return address
}

// toIPNet converts a prefix; the address keeps its host bits.
func toIPNet(prefix netip.Prefix) *net.IPNet {
	return &net.IPNet{
		IP:   prefix.Addr().AsSlice(),
		Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen()),
	}
}

// toPrefix converts an address or network from netlink.
func toPrefix(network *net.IPNet) netip.Prefix {
	address, _ := netip.AddrFromSlice(network.IP)
	bits, _ := network.Mask.Size()
	return netip.PrefixFrom(address.Unmap(), bits)
}
