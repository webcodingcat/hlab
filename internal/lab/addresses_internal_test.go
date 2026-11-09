package lab

import (
	"net/netip"
	"testing"
)

func TestServiceAddressCountsTheSixtyFourIntoTheServiceRange(t *testing.T) {
	cases := map[string]string{
		"2001:db8:1:1::/64":    "100.64.0.1",
		"2001:db8:1:1ff::/64":  "100.64.1.255",
		"2001:db8:1:ffff::/64": "100.64.255.255",
	}
	for ipv6, want := range cases {
		if got := serviceAddress(netip.MustParsePrefix(ipv6)); got.String() != want {
			t.Errorf("serviceAddress(%s) = %s, want %s", ipv6, got, want)
		}
	}
}
