package cloud

import (
	"net/http"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// The checks of a network's fields and the rules for its ranges, subnets
// and routes, which a create and the network's actions share.

// checkSubnetFields notes the fields of a subnet that are missing or not
// one of their values. A create needs the ip_range; an add_subnet takes
// the first free /24 without one (measured 2026-11-08).
func checkSubnetFields(check *web.Check, prefix string, subnet subnetRequest, needRange bool) {
	if subnet.NetworkZone == nil {
		check.Fail(prefix+"network_zone", "'"+prefix+"network_zone' is required")
	} else if !slices.Contains(networkZones, *subnet.NetworkZone) {
		check.Fail(prefix+"network_zone", networkZoneUnknown)
	}
	if subnet.Type == nil {
		check.Fail(prefix+"type", "'"+prefix+"type' is required")
	} else if !slices.Contains([]string{subnetTypeCloud, subnetTypeServer, subnetTypeVSwitch}, *subnet.Type) {
		check.Fail(prefix+"type", networkValueInvalid)
	} else if *subnet.Type == subnetTypeVSwitch && subnet.VSwitchID == nil {
		check.Fail(prefix+"vswitch_id", networkVSwitchMissing)
	}
	if needRange && subnet.IPRange == nil {
		check.Fail(prefix+"ip_range", "'"+prefix+"ip_range' is required")
	}
}

// checkRouteFields notes a route's missing fields.
func checkRouteFields(check *web.Check, prefix string, route routeRequest) {
	if route.Destination == nil {
		check.Fail(prefix+"destination", "'"+prefix+"destination' is required")
	}
	if route.Gateway == nil {
		check.Fail(prefix+"gateway", "'"+prefix+"gateway' is required")
	}
}

// networkZones are Hetzner's network zones.
var networkZones = []string{"eu-central", "us-east", "us-west", "ap-southeast"}

// validNetworkRange tells whether a range may be a network's: in RFC 1918,
// at least a /24, without host bits.
func validNetworkRange(ipRange netip.Prefix) bool {
	return ipRange.Addr().Is4() && ipRange.Bits() <= networkSmallestRange &&
		ipRange == ipRange.Masked() && inPrivateRange(ipRange)
}

// inPrivateRange tells whether a prefix lies in one of RFC 1918's ranges.
func inPrivateRange(prefix netip.Prefix) bool {
	for _, private := range privateRanges {
		if private.Bits() <= prefix.Bits() && private.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

// newSubnet checks a subnet the fields of which are checked already
// against the network, as Hetzner does, and returns it. Without an
// ip_range it takes the first free /24 of the network. A vSwitch subnet is
// refused as Hetzner refuses a vSwitch it does not know: the lab has no
// Robot. overlap is the text of the request's kind.
func newSubnet(network *state.Network, request subnetRequest, overlap string) (state.NetworkSubnet, error) {
	if *request.Type == subnetTypeVSwitch {
		return state.NetworkSubnet{}, web.RefusalNull(http.StatusUnprocessableEntity, "service_error", networkVSwitchNotFound)
	}
	ipRange := netip.MustParsePrefix(network.IPRange)
	taken := networkPrefixes(network)
	var subnetRange netip.Prefix
	if request.IPRange == nil {
		var found bool
		if subnetRange, found = freeSubnet(ipRange, taken); !found {
			return state.NetworkSubnet{}, web.RefusalNull(http.StatusUnprocessableEntity, "no_subnet_available", serverNoSubnet)
		}
	} else {
		prefix, err := netip.ParsePrefix(*request.IPRange)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() ||
			prefix.Bits() > networkSmallestSubnet || prefix.Bits() < ipRange.Bits() || !ipRange.Contains(prefix.Addr()) {
			return state.NetworkSubnet{}, web.InvalidInput(networkSubnetInvalid)
		}
		subnetRange = prefix
	}
	for _, prefix := range taken {
		if prefix.Overlaps(subnetRange) {
			return state.NetworkSubnet{}, web.InvalidInput(overlap)
		}
	}
	for _, subnet := range network.Subnets {
		if subnet.NetworkZone != *request.NetworkZone {
			return state.NetworkSubnet{}, web.InvalidInput(networkZonesNotAligned)
		}
	}
	return state.NetworkSubnet{Type: *request.Type, IPRange: subnetRange.String(), NetworkZone: *request.NetworkZone}, nil
}

// freeSubnet returns the first /24 of a range that overlaps nothing taken;
// without one, the first free block of the next size down, to the
// smallest subnet: Hetzner gave the free /25 of a /24 and of a /23 whose
// space was taken but for it, and refused a /22 of four /24 subnets with
// no_subnet_available (measured 2026-11-09).
func freeSubnet(ipRange netip.Prefix, taken []netip.Prefix) (netip.Prefix, bool) {
	for bits := max(networkSmallestRange, ipRange.Bits()); bits <= networkSmallestSubnet; bits++ {
		size := uint64(1) << (32 - bits)
		bytes := ipRange.Masked().Addr().As4()
		first := uint64(bytes[0])<<24 | uint64(bytes[1])<<16 | uint64(bytes[2])<<8 | uint64(bytes[3])
		last := first + uint64(1)<<(32-ipRange.Bits())
		for start := first; start < last; start += size {
			candidate := netip.PrefixFrom(netip.AddrFrom4([4]byte{byte(start >> 24), byte(start >> 16), byte(start >> 8), byte(start)}), bits)
			if !slices.ContainsFunc(taken, candidate.Overlaps) {
				return candidate, true
			}
		}
	}
	return netip.Prefix{}, false
}

// newRoute checks a route whose fields are there against the network, as
// Hetzner does, and returns it: its destination in RFC 1918 or the default
// route, never 172.31.1.1; its gateway in the network, neither its first
// address nor 172.31.1.1; no overlap with a subnet or another route,
// except for the default route. overlap is the text of the request's kind.
func newRoute(network *state.Network, request routeRequest, overlap string) (state.NetworkRoute, error) {
	destination, err := netip.ParsePrefix(*request.Destination)
	isDefault := err == nil && destination.Bits() == 0 && destination.Addr() == netip.IPv4Unspecified()
	if err != nil || !destination.Addr().Is4() || destination != destination.Masked() ||
		!isDefault && !inPrivateRange(destination) {
		return state.NetworkRoute{}, web.InvalidInput(networkRouteInvalid)
	}
	ipRange := netip.MustParsePrefix(network.IPRange)
	gateway, err := netip.ParseAddr(*request.Gateway)
	if err != nil || !ipRange.Contains(gateway) || gateway == ipRange.Addr().Next() ||
		gateway == publicGateway || !isDefault && destination.Contains(publicGateway) {
		return state.NetworkRoute{}, web.InvalidInput(networkGatewayInvalid)
	}
	if !isDefault {
		for _, prefix := range networkPrefixes(network) {
			if prefix.Overlaps(destination) {
				return state.NetworkRoute{}, web.InvalidInput(overlap)
			}
		}
	}
	return state.NetworkRoute{Destination: destination.String(), Gateway: gateway.String()}, nil
}

// networkPrefixes returns the network's subnets and the destinations of
// its routes other than the default route: what a new subnet or route must
// not overlap.
func networkPrefixes(network *state.Network) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, subnet := range network.Subnets {
		prefixes = append(prefixes, netip.MustParsePrefix(subnet.IPRange))
	}
	for _, route := range network.Routes {
		if destination := netip.MustParsePrefix(route.Destination); destination.Bits() > 0 {
			prefixes = append(prefixes, destination)
		}
	}
	return prefixes
}

// sortSubnets keeps a network's subnets in the order of their ranges, as
// Hetzner lists them (measured 2026-11-08).
func sortSubnets(network *state.Network) {
	slices.SortFunc(network.Subnets, func(a, b state.NetworkSubnet) int {
		return netip.MustParsePrefix(a.IPRange).Addr().Compare(netip.MustParsePrefix(b.IPRange).Addr())
	})
}
