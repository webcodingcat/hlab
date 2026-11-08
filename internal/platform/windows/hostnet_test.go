//go:build windows

package windows

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
)

func TestOverlappingNamesTheRangeAndTheInterface(t *testing.T) {
	lab := []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("2001:db8::/32")}
	free := []usedRange{
		{Prefix: "0.0.0.0/0", Interface: "Wi-Fi"}, // a default route overlaps everything
		{Prefix: "::/0", Interface: "Wi-Fi"},
		{Prefix: "192.168.178.0/24", Interface: "Wi-Fi"},
		{Prefix: "fe80::1/64", Interface: "Wi-Fi"},
		{Prefix: "not a prefix", Interface: "Wi-Fi"},
	}
	if err := overlapping(lab, free); err != nil {
		t.Fatalf("free ranges: %v", err)
	}

	used := append(free, usedRange{Prefix: "198.19.4.7/16", Interface: "VPN"})
	err := overlapping(lab, used)
	if !errors.Is(err, platform.ErrInUse) || !strings.Contains(err.Error(), "198.18.0.0/15") || !strings.Contains(err.Error(), `"VPN"`) {
		t.Fatalf("got %v, want ErrInUse naming 198.18.0.0/15 and VPN", err)
	}
}

func TestGatewayForTakesTheRoutesAddressFamily(t *testing.T) {
	gateways := []netip.Addr{netip.MustParseAddr("198.18.0.2"), netip.MustParseAddr("2001:db8:0:ffff::2")}
	for route, want := range map[string]string{"198.18.0.0/15": "198.18.0.2", "2001:db8::/32": "2001:db8:0:ffff::2"} {
		got, err := gatewayFor(netip.MustParsePrefix(route), gateways)
		if err != nil || got.String() != want {
			t.Errorf("gatewayFor(%s) = %v, %v; want %s", route, got, err, want)
		}
	}
	if _, err := gatewayFor(netip.MustParsePrefix("2001:db8::/32"), gateways[:1]); err == nil {
		t.Error("an IPv6 route without an IPv6 gateway got one")
	}
}
