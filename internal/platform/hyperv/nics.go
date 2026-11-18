package hyperv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/webcodingcat/hlab/internal/platform"
)

// The public segment's private VLAN (decision "Guest-visible identifiers").
const (
	pvlanPrimary   = 4094
	pvlanSecondary = 4093
)

// macPlain renders a MAC as Hyper-V's -StaticMacAddress wants it:
// 12 hex digits without separators.
func macPlain(mac net.HardwareAddr) (string, error) {
	if len(mac) != 6 {
		return "", fmt.Errorf("mac %q is not 48 bits", mac.String())
	}
	return strings.ToUpper(strings.ReplaceAll(mac.String(), ":", "")), nil
}

// macDashes renders a MAC as port ACLs need it; the switch rejects the
// plain form (E1).
func macDashes(mac net.HardwareAddr) (string, error) {
	if len(mac) != 6 {
		return "", fmt.Errorf("mac %q is not 48 bits", mac.String())
	}
	return strings.ToUpper(strings.ReplaceAll(mac.String(), ":", "-")), nil
}

// AttachNIC adds an adapter with a static MAC on the switch of
// Options.SwitchName and puts it on its segment: the public PVLAN as
// isolated port (guests) or promiscuous port (the appliance), or a private
// network's access VLAN with a deny ACL for each of DenyMACs. Guest
// adapters get MAC spoofing off and router and DHCP guard on; the
// appliance routes, so its promiscuous adapter gets no guards.
func (p *Platform) AttachNIC(ctx context.Context, vm string, nic platform.NICSpec) error {
	const op = "AttachNIC"
	fail := func(err error) error { return &platform.Error{Kind: platform.Failed, Op: op, Err: err} }
	if p.opts.SwitchName == "" {
		return fail(errors.New("no switch configured"))
	}
	mac, err := macPlain(nic.MAC)
	if err != nil {
		return fail(err)
	}
	seg := nic.Segment
	var vlan string
	switch {
	case seg.PublicPVLAN && seg.Promiscuous:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Promiscuous -PrimaryVlanId $primary -SecondaryVlanIdList ([string]$secondary)`
	case seg.PublicPVLAN:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Isolated -PrimaryVlanId $primary -SecondaryVlanId $secondary`
	case seg.VLAN >= 2 && seg.VLAN <= 4092:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Access -VlanId $vlan`
	default:
		return fail(fmt.Errorf("invalid segment %+v", seg))
	}
	guards := "On"
	if seg.Promiscuous {
		guards = "Off"
	}
	deny := make([]string, 0, len(nic.DenyMACs))
	for _, m := range nic.DenyMACs {
		d, err := macDashes(m)
		if err != nil {
			return fail(err)
		}
		deny = append(deny, d)
	}
	_, err = p.r.run(ctx, op, map[string]any{
		"name": vm, "switch": p.opts.SwitchName, "mac": mac, "guards": guards,
		"primary": pvlanPrimary, "secondary": pvlanSecondary, "vlan": seg.VLAN, "deny": strings.Join(deny, ","),
	}, getVM+`$a = Add-VMNetworkAdapter -VM $vm -SwitchName $switch -StaticMacAddress $mac -Passthru
try {
  Set-VMNetworkAdapter -VMNetworkAdapter $a -MacAddressSpoofing Off -RouterGuard $guards -DhcpGuard $guards
  `+vlan+`
  foreach ($d in ($deny -split [char]44 | Where-Object { $_ })) {
    Add-VMNetworkAdapterAcl -VMNetworkAdapter $a -RemoteMacAddress $d -Direction Both -Action Deny
  }
} catch {
  Remove-VMNetworkAdapter -VMNetworkAdapter $a -ErrorAction SilentlyContinue
  throw
}
`)
	return err
}

// DetachNIC removes the VM's adapter with the MAC mac (any form
// net.ParseMAC accepts).
func (p *Platform) DetachNIC(ctx context.Context, vm, mac string) error {
	hw, err := net.ParseMAC(mac)
	if err == nil {
		mac, err = macPlain(hw)
	}
	if err != nil {
		return &platform.Error{Kind: platform.Failed, Op: "DetachNIC", Err: err}
	}
	_, err = p.r.run(ctx, "DetachNIC", map[string]any{"name": vm, "mac": mac}, getVM+`$a = Get-VMNetworkAdapter -VM $vm | Where-Object { $_.MacAddress -eq $mac } | Select-Object -First 1
if (-not $a) { throw [System.Management.Automation.ItemNotFoundException]::new("adapter $mac of vm $name not found") }
Remove-VMNetworkAdapter -VMNetworkAdapter $a
`)
	return err
}
