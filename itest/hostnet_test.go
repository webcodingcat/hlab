//go:build live && windows

package itest

import (
	"net"
	"net/netip"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform/contract"
	"github.com/webcodingcat/hlab/internal/platform/windows"
)

// TestHostNetKeepsTheContract runs the host network's contract tests against
// Hyper-V's switches and the host's addresses and routes; it needs
// administrator rights. HLab must not run: the tests take its ranges.
func TestHostNetKeepsTheContract(t *testing.T) {
	hostNet := windows.NewHostNet(quietLogger())
	t.Cleanup(func() { _ = hostNet.Close() })
	contract.HostNet(t, hostNet, usedRange(t))
}

// TestHostDNSKeepsTheContract runs the host DNS's contract tests against the
// NRPT; it needs administrator rights.
func TestHostDNSKeepsTheContract(t *testing.T) {
	hostDNS := windows.NewHostDNS(quietLogger())
	t.Cleanup(func() { _ = hostDNS.Close() })
	contract.HostDNS(t, hostDNS)
}

// usedRange returns the network of an IPv4 address the machine has, such as
// its LAN's.
func usedRange(t *testing.T) netip.Prefix {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok {
			continue
		}
		ip, _ := netip.AddrFromSlice(network.IP)
		ip = ip.Unmap()
		if ip.Is4() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			bits, _ := network.Mask.Size()
			return netip.PrefixFrom(ip, bits).Masked()
		}
	}
	t.Fatal("the machine has no IPv4 address to test with")
	return netip.Prefix{}
}
