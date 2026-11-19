package hyperv

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/platform"
)

// dvdController is the SCSI controller holding the DVD drives; disks are on
// controller 0 (decision "Guest-visible identifiers").
const dvdController = 1

// SetDVD inserts iso into the DVD drive at location slot of SCSI controller
// 1 (0 cidata, 1 user ISO), adding the controller and the drive if they are
// missing; "" ejects. Hyper-V completes an eject a few seconds after
// Set-VMDvdDrive returns, and inserting before that fails with "object not
// found", so an eject returns only once the drive reports no media.
func (p *Platform) SetDVD(ctx context.Context, vm string, slot int, iso string) error {
	if slot != 0 && slot != 1 {
		return &platform.Error{Kind: platform.Failed, Op: "SetDVD", Err: fmt.Errorf("dvd slot %d is not 0 or 1", slot)}
	}
	_, err := p.r.run(ctx, "SetDVD", map[string]any{"name": vm, "slot": slot, "iso": iso, "ctl": dvdController}, getVM+`$iso = if ($iso) { [System.IO.Path]::GetFullPath($iso) } else { $null }
$d = Get-VMDvdDrive -VM $vm | Where-Object { $_.ControllerNumber -eq $ctl -and $_.ControllerLocation -eq $slot } | Select-Object -First 1
if ($d) {
  Set-VMDvdDrive -VMDvdDrive $d -Path $iso
  if ($iso) { return }
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  while ((Get-VMDvdDrive -VM $vm | Where-Object { $_.ControllerNumber -eq $ctl -and $_.ControllerLocation -eq $slot } | Select-Object -First 1).Path) {
    if ($sw.Elapsed.TotalSeconds -gt 60) { throw "dvd $slot of vm $name still holds media 60 s after the eject" }
    Start-Sleep -Milliseconds 250
  }
  return
}
if (-not $iso) { return }
if (-not (Get-VMScsiController -VM $vm | Where-Object { $_.ControllerNumber -eq $ctl })) { Add-VMScsiController -VM $vm }
Add-VMDvdDrive -VM $vm -ControllerNumber $ctl -ControllerLocation $slot -Path $iso
`)
	return err
}

// SetBootOrder sets the VM's firmware boot order: "disk" entries name the
// LUN on SCSI controller 0, "dvd" entries the slot on SCSI controller 1.
func (p *Platform) SetBootOrder(ctx context.Context, vm string, order []platform.BootDevice) error {
	entries := make([]string, 0, len(order))
	for _, b := range order {
		var ctl int
		switch b.Kind {
		case "disk":
			ctl = 0
		case "dvd":
			ctl = dvdController
		default:
			return &platform.Error{Kind: platform.Failed, Op: "SetBootOrder", Err: fmt.Errorf("unknown boot device kind %q", b.Kind)}
		}
		entries = append(entries, b.Kind+":"+strconv.Itoa(ctl)+":"+strconv.Itoa(b.Slot))
	}
	_, err := p.r.run(ctx, "SetBootOrder", map[string]any{"name": vm, "order": strings.Join(entries, ",")}, getVM+`$list = @()
foreach ($e in ($order -split [char]44 | Where-Object { $_ })) {
  $kind, $ctl, $loc = $e -split [char]58
  if ($kind -eq 'disk') { $all = Get-VMHardDiskDrive -VM $vm } else { $all = Get-VMDvdDrive -VM $vm }
  $d = $all | Where-Object { $_.ControllerNumber -eq [int]$ctl -and $_.ControllerLocation -eq [int]$loc } | Select-Object -First 1
  if (-not $d) { throw [System.Management.Automation.ItemNotFoundException]::new("$kind $loc of vm $name not found") }
  $list += $d
}
Set-VMFirmware -VM $vm -BootOrder $list
`)
	return err
}
