package system_test

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/system"
)

var testLink = system.HostLink{
	Ranges:    []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("2001:db8::/32")},
	Addresses: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/29")},
	Gateways:  []netip.Addr{netip.MustParseAddr("198.18.0.2")},
}

func TestConnectHostRoutesTheLabsRangesToTheAppliance(t *testing.T) {
	machine := fake.New()
	if err := system.New(machine.Platform()).ConnectHost(t.Context(), testLink); err != nil {
		t.Fatal(err)
	}
	link, up := machine.Link()
	if !up || link.Switch != system.LinkSwitch || !slices.Equal(link.Routes, testLink.Ranges) ||
		!slices.Equal(link.PrivateSwitches, []string{system.PublicSwitch}) {
		t.Errorf("Link() = %+v, %v; want the lab's switches and its ranges as routes", link, up)
	}
}

func TestConnectHostRefusesARangeTheMachineUses(t *testing.T) {
	machine := fake.New()
	machine.SetUsedRanges(netip.MustParsePrefix("198.19.0.0/16"))
	err := system.New(machine.Platform()).ConnectHost(t.Context(), testLink)
	if !errors.Is(err, platform.ErrInUse) {
		t.Fatalf("got %v, want ErrInUse", err)
	}
	if _, up := machine.Link(); up {
		t.Error("the link was set up although a range is in use")
	}
}

func TestRemoveStrayVMsKeepsTheNamedAndOthersVMs(t *testing.T) {
	machine := fake.New()
	hypervisor := machine.Platform().Hypervisor
	for _, vm := range []platform.VMSpec{
		{Name: system.ApplianceVM, Tag: system.VMTag},
		{Name: "left-behind", Tag: system.VMTag},
		{Name: "not-ours", Tag: "someone else"},
	} {
		vm.CPUs, vm.MemoryMB = 1, 512
		if err := hypervisor.CreateVM(t.Context(), vm); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := system.New(machine.Platform()).RemoveStrayVMs(t.Context(), []string{system.ApplianceVM})
	if err != nil || !slices.Equal(removed, []string{"left-behind"}) {
		t.Fatalf("got %v, %v; want left-behind removed", removed, err)
	}
	for _, name := range []string{system.ApplianceVM, "not-ours"} {
		if _, err := hypervisor.VMState(t.Context(), name); err != nil {
			t.Errorf("%s: %v, want it kept", name, err)
		}
	}
}
