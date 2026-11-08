package agentproto

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// Links is the picture of the appliance's interfaces, VLANs, VRFs,
// addresses and routes. The agent makes the appliance match it: it adds
// what is missing and removes what is not in the picture, except on the
// uplink, which networkd manages.
type Links struct {
	Generation uint64 `json:"generation"`
	// Interfaces are the NICs, found by MAC address and renamed.
	Interfaces []Interface `json:"interfaces"`
	VRFs       []VRF       `json:"vrfs"`
	VLANs      []VLAN      `json:"vlans"`
	// Addresses on the interfaces, VLANs, VRFs and lo.
	Addresses []Address `json:"addresses"`
	Routes    []Route   `json:"routes"`
	// Sysctls are set after the VLANs exist and before any link goes up.
	Sysctls []Sysctl `json:"sysctls"`
}

// Interface is a NIC of the appliance. The agent sets it up and gives it
// the picture's addresses, unless networkd manages it.
type Interface struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
	// Networkd: networkd manages the NIC (the uplink, by DHCP); the agent
	// only names it and leaves its state, addresses and routes alone.
	Networkd bool `json:"networkd,omitempty"`
}

// VRF is a routing table of its own with a device, one per private network.
type VRF struct {
	Name  string `json:"name"`
	Table uint32 `json:"table"`
}

// VLAN is a VLAN device on a NIC, in a VRF or (VRF "") in the main table.
type VLAN struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
	ID     int    `json:"id"`
	VRF    string `json:"vrf,omitempty"`
}

// Address is an address of a device, with its prefix length.
type Address struct {
	Device string       `json:"device"`
	Prefix netip.Prefix `json:"prefix"`
	// Announce: the agent tells the neighbors about it whenever the
	// device's carrier comes up, so that they learn the new MAC address
	// after an update of the appliance (gratuitous ARP, unsolicited
	// neighbor advertisement).
	Announce bool `json:"announce,omitempty"`
}

// Route is a route through a device, in the main table (Table 0) or a VRF's.
// A blackhole route has no device: the kernel drops what it takes, without
// an answer.
type Route struct {
	Destination netip.Prefix `json:"destination"`
	Blackhole   bool         `json:"blackhole,omitempty"`
	Device      string       `json:"device,omitempty"`
	Gateway     netip.Addr   `json:"gateway,omitzero"` // none: on-link
	// OnLink takes the gateway as reachable on the device, though no
	// route leads to it (a private network's route to a gateway that no
	// server holds yet).
	OnLink bool       `json:"on_link,omitempty"`
	Source netip.Addr `json:"source,omitzero"`
	Table  uint32     `json:"table,omitempty"`
}

// Sysctl is a kernel setting below /proc/sys/net, such as
// "net/ipv4/conf/public/send_redirects".
type Sysctl struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Ruleset is the appliance's whole nftables ruleset as text. nft -f applies
// it as one transaction, so a broken ruleset changes nothing.
type Ruleset struct {
	Generation uint64 `json:"generation"`
	Text       string `json:"text"`
}

// DHCP is the configuration of the dnsmasq instances that hand out the
// servers' addresses: one per device it serves, the public NIC and each
// private network's VLAN. An instance of its own per network keeps the
// leases of two networks apart, which may hold the same addresses.
type DHCP struct {
	Generation uint64         `json:"generation"`
	Instances  []DHCPInstance `json:"instances"`
}

// DHCPInstance is one dnsmasq: the device it serves and its whole
// configuration.
type DHCPInstance struct {
	Device string `json:"device"`
	Config string `json:"config"`
}

// Metadata holds what the metadata service answers each server.
type Metadata struct {
	Generation uint64           `json:"generation"`
	Servers    []MetadataServer `json:"servers"`
}

// MetadataServer is what one server gets from the metadata service. The
// agent tells the servers apart by the source address of the request.
type MetadataServer struct {
	Sources []netip.Prefix `json:"sources"`
	// Metadata is the answer to /hetzner/v1/metadata.
	Metadata string `json:"metadata"`
	// Keys holds the answer to /hetzner/v1/metadata/<key>.
	Keys map[string]string `json:"keys"`
	// UserData is the answer to /hetzner/v1/userdata; empty is 204.
	UserData string `json:"user_data"`
}

// Check reports what makes the picture impossible to apply: a bad name or
// value, or a reference to a device the picture does not have.
func (l Links) Check() error {
	var problems []error
	devices := map[string]string{"lo": "device"}
	add := func(kind, name string) {
		if err := checkDeviceName(name); err != nil {
			problems = append(problems, fmt.Errorf("%s %q: %w", kind, name, err))
		} else if _, taken := devices[name]; taken {
			problems = append(problems, fmt.Errorf("%s %q: the name is taken", kind, name))
		}
		devices[name] = kind
	}
	macs := map[string]bool{}
	for _, nic := range l.Interfaces {
		add("interface", nic.Name)
		mac, err := net.ParseMAC(nic.MAC)
		if err != nil || len(mac) != 6 {
			problems = append(problems, fmt.Errorf("interface %q: bad MAC %q", nic.Name, nic.MAC))
		} else if macs[mac.String()] {
			problems = append(problems, fmt.Errorf("interface %q: MAC %s is taken", nic.Name, mac))
		}
		macs[mac.String()] = true
	}
	tables := map[uint32]bool{}
	for _, vrf := range l.VRFs {
		add("VRF", vrf.Name)
		// 0 is unspecified, 253 to 255 are the kernel's own tables
		if vrf.Table == 0 || vrf.Table >= 253 && vrf.Table <= 255 || tables[vrf.Table] {
			problems = append(problems, fmt.Errorf("VRF %q: table %d is not free", vrf.Name, vrf.Table))
		}
		tables[vrf.Table] = true
	}
	for _, vlan := range l.VLANs {
		add("VLAN", vlan.Name)
		if devices[vlan.Parent] != "interface" {
			problems = append(problems, fmt.Errorf("VLAN %q: no interface %q", vlan.Name, vlan.Parent))
		}
		if vlan.ID < 1 || vlan.ID > 4094 {
			problems = append(problems, fmt.Errorf("VLAN %q: ID %d is not 1 to 4094", vlan.Name, vlan.ID))
		}
		if vlan.VRF != "" && devices[vlan.VRF] != "VRF" {
			problems = append(problems, fmt.Errorf("VLAN %q: no VRF %q", vlan.Name, vlan.VRF))
		}
	}
	networkd := map[string]bool{}
	for _, nic := range l.Interfaces {
		networkd[nic.Name] = nic.Networkd
	}
	for _, address := range l.Addresses {
		if networkd[address.Device] {
			problems = append(problems, fmt.Errorf("address %s: networkd manages %q", address.Prefix, address.Device))
		} else if _, known := devices[address.Device]; !known {
			problems = append(problems, fmt.Errorf("address %s: no device %q", address.Prefix, address.Device))
		}
		if !address.Prefix.IsValid() {
			problems = append(problems, errors.New("an address is missing its prefix"))
		}
	}
	for _, route := range l.Routes {
		if networkd[route.Device] {
			problems = append(problems, fmt.Errorf("route %s: networkd manages %q", route.Destination, route.Device))
		}
		problems = append(problems, checkRoute(route, devices, tables))
	}
	for _, sysctl := range l.Sysctls {
		// only the network's settings, and no way out of /proc/sys/net
		if !strings.HasPrefix(sysctl.Key, "net/") || strings.Contains(sysctl.Key, "..") {
			problems = append(problems, fmt.Errorf("sysctl %q: not below net/", sysctl.Key))
		}
	}
	return errors.Join(problems...)
}

// checkRoute reports what is wrong with a route, or nil.
func checkRoute(route Route, devices map[string]string, tables map[uint32]bool) error {
	destination := route.Destination
	switch {
	case !destination.IsValid():
		return errors.New("a route is missing its destination")
	case destination != destination.Masked():
		return fmt.Errorf("route %s: host bits are set", destination)
	case route.Blackhole && (route.Device != "" || route.Gateway.IsValid() || route.Source.IsValid()):
		return fmt.Errorf("route %s: a blackhole has no device, gateway or source", destination)
	case !route.Blackhole && devices[route.Device] == "":
		return fmt.Errorf("route %s: no device %q", destination, route.Device)
	case route.OnLink && !route.Gateway.IsValid():
		return fmt.Errorf("route %s: on-link without a gateway", destination)
	case route.Gateway.IsValid() && route.Gateway.Is4() != destination.Addr().Is4():
		return fmt.Errorf("route %s: gateway %s is of the other family", destination, route.Gateway)
	case route.Source.IsValid() && route.Source.Is4() != destination.Addr().Is4():
		return fmt.Errorf("route %s: source %s is of the other family", destination, route.Source)
	case route.Table != 0 && !tables[route.Table]:
		return fmt.Errorf("route %s: no VRF has table %d", destination, route.Table)
	}
	return nil
}

// checkDeviceName refuses what Linux refuses as a device name.
func checkDeviceName(name string) error {
	if name == "" || len(name) > 15 || name == "." || name == ".." ||
		strings.ContainsAny(name, "/: \t\n") {
		return errors.New("not a device name (1 to 15 characters, no '/', ':' or spaces)")
	}
	return nil
}

// Check refuses an empty ruleset: it would leave the appliance open.
func (r Ruleset) Check() error {
	if strings.TrimSpace(r.Text) == "" {
		return errors.New("the ruleset is empty")
	}
	return nil
}

// Check refuses a picture without instances (the public NIC always has
// one), an instance without a configuration, and a device that is no
// device name or has two instances.
func (d DHCP) Check() error {
	if len(d.Instances) == 0 {
		return errors.New("the DHCP picture has no instance")
	}
	var problems []error
	devices := map[string]bool{}
	for _, instance := range d.Instances {
		if err := checkDeviceName(instance.Device); err != nil {
			problems = append(problems, fmt.Errorf("DHCP instance %q: %w", instance.Device, err))
		} else if devices[instance.Device] {
			problems = append(problems, fmt.Errorf("DHCP instance %q: the device has two", instance.Device))
		}
		devices[instance.Device] = true
		if strings.TrimSpace(instance.Config) == "" {
			problems = append(problems, fmt.Errorf("DHCP instance %q: the configuration is empty", instance.Device))
		}
	}
	return errors.Join(problems...)
}

// Check refuses a source that two servers share: the agent could not tell
// them apart.
func (m Metadata) Check() error {
	var problems []error
	for i, server := range m.Servers {
		for _, source := range server.Sources {
			if !source.IsValid() {
				problems = append(problems, fmt.Errorf("server %d: a source is missing", i))
				continue
			}
			for _, other := range m.Servers[i+1:] {
				for _, theirs := range other.Sources {
					if theirs.IsValid() && source.Overlaps(theirs) {
						problems = append(problems, fmt.Errorf("sources %s and %s overlap", source, theirs))
					}
				}
			}
		}
	}
	return errors.Join(problems...)
}
