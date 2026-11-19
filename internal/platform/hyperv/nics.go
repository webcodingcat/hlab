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
// isolated port (guests) or promiscuous port (the appliance), a private
// network's access VLAN with a deny ACL for each of DenyMACs, or the trunk
// (the appliance's second adapter). Guest adapters get MAC spoofing off and
// router and DHCP guard on; the appliance routes, so its promiscuous and
// trunk adapters get no guards. The VM may be running (hot add).
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
	case seg.PublicPVLAN && seg.Promiscuous && !seg.Trunk:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Promiscuous -PrimaryVlanId $primary -SecondaryVlanIdList ([string]$secondary)`
	case seg.PublicPVLAN && !seg.Trunk:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Isolated -PrimaryVlanId $primary -SecondaryVlanId $secondary`
	case seg.Trunk && !seg.PublicPVLAN && seg.VLAN == 0:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Trunk -AllowedVlanIdList '2-4092' -NativeVlanId 0`
	case !seg.Trunk && seg.VLAN >= 2 && seg.VLAN <= 4092:
		vlan = `Set-VMNetworkAdapterVlan -VMNetworkAdapter $a -Access -VlanId $vlan`
	default:
		return fail(fmt.Errorf("invalid segment %+v", seg))
	}
	guards := "On"
	if seg.Promiscuous || seg.Trunk {
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
// net.ParseMAC accepts); the VM may be running (hot remove).
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

// SetNICDeny makes the deny ACLs for remote MACs of the VM's adapter with
// the MAC mac exactly deny, each in both directions. Hyper-V lists an entry
// added with Direction Both as one inbound and one outbound entry and
// refuses to add an existing entry again (0x800700B7), so the script keeps
// the complete entries that are wanted, removes the others and adds the
// missing ones. MACs are written with dashes (E1).
func (p *Platform) SetNICDeny(ctx context.Context, vm, mac string, deny []net.HardwareAddr) error {
	const op = "SetNICDeny"
	fail := func(err error) error { return &platform.Error{Kind: platform.Failed, Op: op, Err: err} }
	hw, err := net.ParseMAC(mac)
	if err == nil {
		mac, err = macPlain(hw)
	}
	if err != nil {
		return fail(err)
	}
	want := make([]string, 0, len(deny))
	for _, m := range deny {
		d, err := macDashes(m)
		if err != nil {
			return fail(err)
		}
		want = append(want, d)
	}
	_, err = p.r.run(ctx, op, map[string]any{"name": vm, "mac": mac, "deny": strings.Join(want, ",")},
		getVM+`$a = Get-VMNetworkAdapter -VM $vm | Where-Object { $_.MacAddress -eq $mac } | Select-Object -First 1
if (-not $a) { throw [System.Management.Automation.ItemNotFoundException]::new("adapter $mac of vm $name not found") }
$want = @($deny -split [char]44 | Where-Object { $_ })
$keep = @{}
$entries = @(Get-VMNetworkAdapterAcl -VMNetworkAdapter $a | Where-Object { [int]$_.Action -eq 2 -and $_.RemoteAddress -match '^([0-9A-Fa-f]{2}-){5}[0-9A-Fa-f]{2}$' })
foreach ($g in ($entries | Group-Object { $_.RemoteAddress.ToUpper() })) {
  $dirs = 0
  foreach ($e in $g.Group) { $dirs = $dirs -bor [int]$e.Direction }
  if ($want -contains $g.Name -and $dirs -eq 3) { $keep[$g.Name] = $true; continue }
  $g.Group | Remove-VMNetworkAdapterAcl
}
foreach ($d in $want) {
  if (-not $keep.ContainsKey($d)) {
    Add-VMNetworkAdapterAcl -VMNetworkAdapter $a -RemoteMacAddress $d -Direction Both -Action Deny
    $keep[$d] = $true
  }
}
`)
	return err
}
