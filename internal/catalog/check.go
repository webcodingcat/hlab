package catalog

import (
	"fmt"
	"slices"
)

// The values Hetzner's API allows (its spec's enums), so that a catalog
// entry the tools would refuse is refused when the lab starts.
var (
	architectures = []string{"x86", "arm"}
	cpuTypes      = []string{"shared", "dedicated"}
	storageTypes  = []string{"local", "network"}
	osFlavors     = []string{"ubuntu", "centos", "debian", "fedora", "rocky", "alma", "opensuse", "unknown"}
	ipTypes       = []string{"ipv4", "ipv6"}
)

// check tells what is wrong with a catalog: IDs and names that are missing
// or not unique within their list, values the API does not allow, and
// references to locations that are not there.
func (c *Catalog) check() error {
	locations := map[string]bool{}
	for _, location := range c.Locations {
		locations[location.Name] = true
	}
	checkLocation := func(what, name string) error {
		if !locations[name] {
			return fmt.Errorf("%s: unknown location %q", what, name)
		}
		return nil
	}
	checkPrices := func(what string, prices []TypePrice) error {
		for _, price := range prices {
			if err := checkLocation(what+" price", price.Location); err != nil {
				return err
			}
		}
		return nil
	}

	if err := uniqueIDsAndNames("locations", c.Locations, func(l Location) (int64, string) { return l.ID, l.Name }); err != nil {
		return err
	}
	if err := uniqueIDsAndNames("server_types", c.ServerTypes, func(t ServerType) (int64, string) { return t.ID, t.Name }); err != nil {
		return err
	}
	for _, serverType := range c.ServerTypes {
		what := "server type " + serverType.Name
		if err := oneOf(what+" architecture", serverType.Architecture, architectures); err != nil {
			return err
		}
		if err := oneOf(what+" cpu_type", serverType.CPUType, cpuTypes); err != nil {
			return err
		}
		if err := oneOf(what+" storage_type", serverType.StorageType, storageTypes); err != nil {
			return err
		}
		for _, location := range serverType.Locations {
			if err := checkLocation(what, location.Name); err != nil {
				return err
			}
		}
		if err := checkPrices(what, serverType.Prices); err != nil {
			return err
		}
	}
	if err := uniqueIDsAndNames("load_balancer_types", c.LoadBalancerTypes, func(t LoadBalancerType) (int64, string) { return t.ID, t.Name }); err != nil {
		return err
	}
	for _, balancerType := range c.LoadBalancerTypes {
		if err := checkPrices("load balancer type "+balancerType.Name, balancerType.Prices); err != nil {
			return err
		}
	}
	if err := uniqueIDsAndNames("storage_box_types", c.StorageBoxTypes, func(t StorageBoxType) (int64, string) { return t.ID, t.Name }); err != nil {
		return err
	}
	for _, boxType := range c.StorageBoxTypes {
		for _, price := range boxType.Prices {
			if err := checkLocation("storage box type "+boxType.Name+" price", price.Location); err != nil {
				return err
			}
		}
	}
	if err := uniqueIDsAndNames("images", c.Images, func(i Image) (int64, string) { return i.ID, i.Name }); err != nil {
		return err
	}
	for _, image := range c.Images {
		if err := oneOf("image "+image.Name+" os_flavor", image.OSFlavor, osFlavors); err != nil {
			return err
		}
		if err := oneOf("image "+image.Name+" architecture", image.Architecture, architectures); err != nil {
			return err
		}
	}
	if err := uniqueIDsAndNames("isos", c.ISOs, func(i ISO) (int64, string) { return i.ID, i.Name }); err != nil {
		return err
	}
	for _, iso := range c.ISOs {
		if iso.Architecture != "" {
			if err := oneOf("iso "+iso.Name+" architecture", iso.Architecture, architectures); err != nil {
				return err
			}
		}
	}
	for _, ipPrices := range c.Pricing.PrimaryIPs {
		for _, price := range ipPrices.Prices {
			if price.PriceHourly == nil {
				return fmt.Errorf("pricing primary_ips %s %s: price_hourly is missing", ipPrices.Type, price.Location)
			}
		}
	}
	for _, ipPrices := range slices.Concat(c.Pricing.FloatingIPs, c.Pricing.PrimaryIPs) {
		if err := oneOf("pricing ip type", ipPrices.Type, ipTypes); err != nil {
			return err
		}
		for _, price := range ipPrices.Prices {
			if err := checkLocation("pricing "+ipPrices.Type, price.Location); err != nil {
				return err
			}
		}
	}
	return nil
}

// uniqueIDsAndNames tells which entry of a list has no ID or name, or one
// that another entry has too.
func uniqueIDsAndNames[T any](list string, entries []T, idAndName func(T) (int64, string)) error {
	ids, names := map[int64]bool{}, map[string]bool{}
	for _, entry := range entries {
		id, name := idAndName(entry)
		switch {
		case id < 1 || name == "":
			return fmt.Errorf("%s: an entry lacks its id or name (id %d, name %q)", list, id, name)
		case ids[id]:
			return fmt.Errorf("%s: the id %d is there twice", list, id)
		case names[name]:
			return fmt.Errorf("%s: the name %q is there twice", list, name)
		}
		ids[id], names[name] = true, true
	}
	return nil
}

// oneOf tells whether a value is one of those allowed.
func oneOf(what, value string, allowed []string) error {
	if !slices.Contains(allowed, value) {
		return fmt.Errorf("%s: %q is none of %v", what, value, allowed)
	}
	return nil
}
