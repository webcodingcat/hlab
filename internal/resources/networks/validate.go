package networks

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
)

// VSwitchMessage is SPEC's fixed answer to a subnet of type vswitch.
const VSwitchMessage = "vswitch subnets are not supported by HLab"

// Subnet sizes: a network is at least a /24, a subnet at least a /30, and
// a subnet without ip_range is the first free /24.
const (
	minNetworkBits = 24
	minSubnetBits  = 30
	defaultBits    = 24
)

var (
	rfc1918 = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	defaultRoute = netip.MustParsePrefix("0.0.0.0/0")
)

// subnetRequest is a subnet in a create or add_subnet body.
type subnetRequest struct {
	Type        string `json:"type"`
	IPRange     string `json:"ip_range"`
	NetworkZone string `json:"network_zone"`
}

func invalid(field, format string, args ...any) error {
	return &api.FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// parsePrefix parses an IPv4 prefix in canonical form (no host bits).
func parsePrefix(s, field string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, invalid(field, "%q is no IPv4 range in CIDR notation", s)
	}
	if p != p.Masked() {
		return netip.Prefix{}, invalid(field, "%q has host bits set; the range starts at %s", s, p.Masked().Addr())
	}
	return p, nil
}

// within reports whether inner lies completely inside outer.
func within(inner, outer netip.Prefix) bool {
	return inner.Bits() >= outer.Bits() && outer.Contains(inner.Addr())
}

func isRFC1918(p netip.Prefix) bool {
	return slices.ContainsFunc(rfc1918, func(r netip.Prefix) bool { return within(p, r) })
}

// checkRange parses a network range: RFC1918, at least a /24.
func checkRange(s, field string) (netip.Prefix, error) {
	p, err := parsePrefix(s, field)
	if err != nil {
		return p, err
	}
	if !isRFC1918(p) {
		return p, invalid(field, "%s is not inside a private range of RFC1918", p)
	}
	if p.Bits() > minNetworkBits {
		return p, invalid(field, "%s is smaller than the minimum network size /%d", p, minNetworkBits)
	}
	return p, nil
}

// knownZone reports whether a catalog location has network zone z.
func (m *Module) knownZone(z string) bool {
	for _, l := range m.d.Catalog.Locations {
		if l.NetworkZone == z {
			return true
		}
	}
	return false
}

// routeDestinations returns the destinations of routes except 0.0.0.0/0,
// which overlaps everything by design.
func routeDestinations(routes []Route) []netip.Prefix {
	var out []netip.Prefix
	for _, r := range routes {
		if d, err := netip.ParsePrefix(r.Destination); err == nil && d != defaultRoute {
			out = append(out, d)
		}
	}
	return out
}

func subnetRanges(subnets []Subnet) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range subnets {
		if p, err := netip.ParsePrefix(s.IPRange); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func overlapsAny(p netip.Prefix, others []netip.Prefix) (netip.Prefix, bool) {
	for _, o := range others {
		if p.Overlaps(o) {
			return o, true
		}
	}
	return netip.Prefix{}, false
}

// checkSubnet validates s for a network with range rng, its subnets and
// routes; field names carry prefix. Without ip_range the subnet is the
// first free /24 of the range.
func (m *Module) checkSubnet(rng netip.Prefix, s subnetRequest, subnets []Subnet, routes []Route, prefix string) (Subnet, error) {
	switch s.Type {
	case "cloud", "server":
	case "vswitch":
		return Subnet{}, &api.FieldError{Field: prefix + "type", Message: VSwitchMessage}
	default:
		return Subnet{}, invalid(prefix+"type", "type must be cloud or server")
	}
	if !m.knownZone(s.NetworkZone) {
		return Subnet{}, invalid(prefix+"network_zone", "unknown network zone %q", s.NetworkZone)
	}
	taken := slices.Concat(subnetRanges(subnets), routeDestinations(routes))
	field := prefix + "ip_range"
	var p netip.Prefix
	if s.IPRange == "" {
		var ok bool
		if p, ok = firstFree(rng, taken); !ok {
			return Subnet{}, invalid(field, "no free /%d left in the network range %s", defaultBits, rng)
		}
	} else {
		var err error
		if p, err = parsePrefix(s.IPRange, field); err != nil {
			return Subnet{}, err
		}
		if !within(p, rng) {
			return Subnet{}, invalid(field, "%s is not inside the network range %s", p, rng)
		}
		if p.Bits() > minSubnetBits {
			return Subnet{}, invalid(field, "%s is smaller than the minimum subnet size /%d", p, minSubnetBits)
		}
		if o, ok := overlapsAny(p, taken); ok {
			return Subnet{}, invalid(field, "%s overlaps %s of the network", p, o)
		}
	}
	return Subnet{Type: s.Type, IPRange: p.String(), NetworkZone: s.NetworkZone, Gateway: Gateway(rng).String()}, nil
}

// firstFree returns the first /24 of rng that overlaps none of taken.
func firstFree(rng netip.Prefix, taken []netip.Prefix) (netip.Prefix, bool) {
	b := rng.Addr().As4()
	base := binary.BigEndian.Uint32(b[:])
	for i := range uint32(1) << (defaultBits - rng.Bits()) {
		binary.BigEndian.PutUint32(b[:], base+i<<(32-defaultBits))
		p := netip.PrefixFrom(netip.AddrFrom4(b), defaultBits)
		if _, ok := overlapsAny(p, taken); !ok {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

// checkRoute validates r for a network with range rng, its subnets and
// other routes; field names carry prefix. It returns the route in
// canonical form.
func checkRoute(rng netip.Prefix, r Route, subnets []Subnet, routes []Route, prefix string) (Route, error) {
	dfield, gfield := prefix+"destination", prefix+"gateway"
	d, err := parsePrefix(r.Destination, dfield)
	if err != nil {
		return Route{}, err
	}
	if d != defaultRoute {
		if !isRFC1918(d) {
			return Route{}, invalid(dfield, "%s is neither inside a private range of RFC1918 nor 0.0.0.0/0", d)
		}
		if o, ok := overlapsAny(d, subnetRanges(subnets)); ok {
			return Route{}, invalid(dfield, "%s overlaps the subnet %s", d, o)
		}
		if d.Contains(publicGateway) {
			return Route{}, invalid(dfield, "%s overlaps %s", d, publicGateway)
		}
	}
	for _, o := range routes {
		if od, err := netip.ParsePrefix(o.Destination); err == nil && d.Overlaps(od) {
			return Route{}, invalid(dfield, "%s overlaps the destination %s of another route", d, od)
		}
	}
	gw, err := netip.ParseAddr(r.Gateway)
	if err != nil || !gw.Is4() {
		return Route{}, invalid(gfield, "%q is no IPv4 address", r.Gateway)
	}
	switch {
	case !rng.Contains(gw):
		return Route{}, invalid(gfield, "%s is not inside the network range %s", gw, rng)
	case gw == Gateway(rng):
		return Route{}, invalid(gfield, "%s is the network gateway", gw)
	case gw == publicGateway:
		return Route{}, invalid(gfield, "%s is the public gateway", gw)
	}
	return Route{Destination: d.String(), Gateway: gw.String()}, nil
}
