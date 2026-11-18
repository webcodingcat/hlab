package hyperv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/go-winio/vhd"

	"github.com/webcodingcat/hlab/internal/platform"
)

// winErr wraps an error of a Win32 call made from Go, classified by its
// error code like the HResults of PowerShell errors.
func winErr(op string, err error) error {
	kind := platform.Failed
	var errno syscall.Errno
	if errors.As(err, &errno) && noResourceHResults[0x80070000|uint32(errno)] {
		kind = platform.NoResources
	}
	return &platform.Error{Kind: kind, Op: op, Err: err}
}

// CreateDisk creates the dynamic VHDX path. Without from it is empty and
// sizeBytes large; with from it is a full copy of the base VHDX (so it
// survives the base's removal) with a new disk identifier, grown to
// sizeBytes if that is larger. A non-zero page83 is the disk identifier
// the guest sees in VPD page 0x83, in the GUID's textual byte order.
func (p *Platform) CreateDisk(ctx context.Context, path string, sizeBytes int64, from string, page83 [16]byte) error {
	const op = "CreateDisk"
	if path == "" || (from == "" && sizeBytes <= 0) || sizeBytes < 0 {
		return &platform.Error{Kind: platform.Failed, Op: op, Err: errors.New("path and a positive size are required")}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return winErr(op, err)
	}
	hasID := page83 != [16]byte{}
	switch {
	case from == "" && hasID:
		h, err := vhd.CreateVirtualDisk(path, vhd.VirtualDiskAccessNone, vhd.CreateVirtualDiskFlagNone, &vhd.CreateVirtualDiskParameters{
			Version:  2,
			Version2: vhd.CreateVersion2{UniqueID: guid.FromArray(page83), MaximumSize: uint64(sizeBytes)},
		})
		if err != nil {
			return winErr(op, err)
		}
		if err := syscall.CloseHandle(h); err != nil {
			return winErr(op, err)
		}
		return nil
	case from == "":
		_, err := p.r.run(ctx, op, map[string]any{"path": path, "size": sizeBytes},
			`New-VHD -Path $path -SizeBytes $size -Dynamic | Out-Null`)
		return err
	}
	_, err := p.r.run(ctx, op, map[string]any{"path": path, "size": sizeBytes, "from": from}, `if (Test-Path -LiteralPath $path) { throw [System.InvalidOperationException]::new("disk $path exists") }
try {
  Copy-Item -LiteralPath $from -Destination $path
  Set-VHD -Path $path -ResetDiskIdentifier -Force
  if ($size -gt (Get-VHD -Path $path).Size) { Resize-VHD -Path $path -SizeBytes $size }
} catch {
  Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
  throw
}
`)
	if err != nil || !hasID {
		return err
	}
	if err := vhd.SetVirtualDiskIdentifier(path, guid.FromArray(page83)); err != nil {
		_ = os.Remove(path)
		return winErr(op, err)
	}
	return nil
}

// ResizeDisk grows the VHDX path to sizeBytes; shrinking is InvalidState.
func (p *Platform) ResizeDisk(ctx context.Context, path string, sizeBytes int64) error {
	_, err := p.r.run(ctx, "ResizeDisk", map[string]any{"path": path, "size": sizeBytes}, `$v = Get-VHD -Path $path
if ($size -lt $v.Size) { throw [System.InvalidOperationException]::new("disk $path cannot shrink") }
if ($size -gt $v.Size) { Resize-VHD -Path $path -SizeBytes $size }
`)
	return err
}

// AttachDisk attaches the VHDX path at SCSI controller 0, location lun; a
// disk already attached there with the same path is left as it is.
func (p *Platform) AttachDisk(ctx context.Context, vm, path string, lun int) error {
	if lun < 0 || lun > 63 {
		return &platform.Error{Kind: platform.Failed, Op: "AttachDisk", Err: fmt.Errorf("lun %d out of range 0..63", lun)}
	}
	_, err := p.r.run(ctx, "AttachDisk", map[string]any{"name": vm, "path": path, "lun": lun}, getVM+`$full = [System.IO.Path]::GetFullPath($path)
$d = Get-VMHardDiskDrive -VM $vm | Where-Object { $_.ControllerType -eq 'SCSI' -and $_.ControllerNumber -eq 0 -and $_.ControllerLocation -eq $lun } | Select-Object -First 1
if ($d -and $d.Path -and [System.IO.Path]::GetFullPath($d.Path) -eq $full) { return }
Add-VMHardDiskDrive -VM $vm -ControllerType SCSI -ControllerNumber 0 -ControllerLocation $lun -Path $full
`)
	return err
}

// DetachDisk removes the drive holding the VHDX path from the VM; the file
// stays.
func (p *Platform) DetachDisk(ctx context.Context, vm, path string) error {
	_, err := p.r.run(ctx, "DetachDisk", map[string]any{"name": vm, "path": path}, getVM+`$full = [System.IO.Path]::GetFullPath($path)
$d = Get-VMHardDiskDrive -VM $vm | Where-Object { $_.Path -and [System.IO.Path]::GetFullPath($_.Path) -eq $full } | Select-Object -First 1
if (-not $d) { throw [System.Management.Automation.ItemNotFoundException]::new("disk $path is not attached to vm $name") }
Remove-VMHardDiskDrive -VMHardDiskDrive $d
`)
	return err
}
