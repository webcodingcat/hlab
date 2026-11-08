package system

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/webcodingcat/hlab/internal/platform"
)

// The names of the lab's switches and VMs on the machine. The switches
// start with platform.SwitchPrefix, so that the start finds and takes them
// over.
const (
	// LinkSwitch connects the host and the appliance (the host link).
	LinkSwitch = platform.SwitchPrefix + "Link"
	// PublicSwitch connects the servers' public NICs with the appliance,
	// through a private VLAN.
	PublicSwitch = platform.SwitchPrefix + "Public"
	// UplinkSwitch is Hyper-V's own switch with NAT to the host's network:
	// the appliance's way to the Internet.
	UplinkSwitch = "Default Switch"
	// VMTag marks the lab's VMs, so that the start finds what an earlier run
	// left (FindVMs).
	VMTag = "hlab"
)

// HostLink is the host's side of the lab.
type HostLink struct {
	// Ranges are the lab's whole address ranges; nothing else on the machine
	// may use them, and the host routes them to the appliance.
	Ranges []netip.Prefix
	// Addresses are the host's own on the host link.
	Addresses []netip.Prefix
	// Gateways are the appliance's addresses on the host link.
	Gateways []netip.Addr
}

// ConnectHost checks that the machine uses none of the lab's ranges, then
// creates or takes over the switches and sets the host's addresses and
// routes.
func (s *System) ConnectHost(ctx context.Context, link HostLink) error {
	if err := s.platform.HostNet.CheckRanges(ctx, link.Ranges); err != nil {
		return fmt.Errorf("checking the lab's address ranges: %w", err)
	}
	spec := platform.LinkSpec{
		Switch:          LinkSwitch,
		Addresses:       link.Addresses,
		Routes:          link.Ranges,
		Gateways:        link.Gateways,
		PrivateSwitches: []string{PublicSwitch},
	}
	if err := s.platform.HostNet.SetUp(ctx, spec); err != nil {
		return fmt.Errorf("setting up the host link: %w", err)
	}
	return nil
}

// DisconnectHost removes the host's DNS rules and its link to the lab: the
// switches with the host's addresses and routes. The VMs keep their NICs,
// without a switch.
func (s *System) DisconnectHost(ctx context.Context) error {
	return errors.Join(s.platform.HostDNS.Clear(ctx), s.platform.HostNet.TearDown(ctx))
}

// SetHostDNS makes rules the host's whole set of DNS rules for the lab.
func (s *System) SetHostDNS(ctx context.Context, rules []platform.DNSRule) error {
	if err := s.platform.HostDNS.Apply(ctx, rules); err != nil {
		return fmt.Errorf("setting the host's DNS rules: %w", err)
	}
	return nil
}

// RemoveStrayVMs deletes the lab's VMs that are not in keep: those of
// records that are gone, or of an older HLab. It returns their names; their
// disks stay.
func (s *System) RemoveStrayVMs(ctx context.Context, keep []string) ([]string, error) {
	found, err := s.platform.Hypervisor.FindVMs(ctx, VMTag)
	if err != nil {
		return nil, fmt.Errorf("finding the lab's VMs: %w", err)
	}
	removed := []string{}
	for _, vm := range found {
		if slices.Contains(keep, vm.Name) {
			continue
		}
		if err := s.platform.Hypervisor.DeleteVM(ctx, vm.Name); err != nil && !errors.Is(err, platform.ErrNotFound) {
			return removed, fmt.Errorf("removing the stray VM %s: %w", vm.Name, err)
		}
		removed = append(removed, vm.Name)
	}
	return removed, nil
}
