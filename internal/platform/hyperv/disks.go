package hyperv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

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

// CreateChildDisk creates the differencing VHDX path on the VHDX parent and
// gives it a new disk identifier (children inherit the parent's, E7).
func (p *Platform) CreateChildDisk(ctx context.Context, path, parent string) error {
	const op = "CreateChildDisk"
	if path == "" || parent == "" {
		return &platform.Error{Kind: platform.Failed, Op: op, Err: errors.New("path and parent are required")}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return winErr(op, err)
	}
	_, err := p.r.run(ctx, op, map[string]any{"path": path, "parent": parent}, `if (Test-Path -LiteralPath $path) { throw [System.InvalidOperationException]::new("disk $path exists") }
if (-not (Test-Path -LiteralPath $parent)) { throw [System.Management.Automation.ItemNotFoundException]::new("disk $parent not found") }
try {
  New-VHD -Path $path -ParentPath $parent -Differencing | Out-Null
  Set-VHD -Path $path -ResetDiskIdentifier -Force
} catch {
  Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
  throw
}
`)
	return err
}

// checkpointPrefix starts the name of every checkpoint Snapshot takes, so
// one left behind by a crash can be found and removed.
const checkpointPrefix = "hlab-snapshot-"

// mergeTimeout bounds the wait for Hyper-V to merge a removed checkpoint
// back into the VM's disks.
const mergeTimeout = 10 * time.Minute

// Snapshot copies the VM's disk diskPath to target, a standalone dynamic
// VHDX with a new disk identifier. Checkpoints hlab left behind are removed
// first. An off or saved VM's disk is copied directly; for any other state
// a Standard checkpoint is taken, its read-only parent (diskPath) copied
// and the checkpoint removed, and Snapshot waits until the merge is done.
// The copy of a running disk is crash-consistent.
func (p *Platform) Snapshot(ctx context.Context, vm, diskPath, target string) error {
	const op = "Snapshot"
	if diskPath == "" || target == "" {
		return &platform.Error{Kind: platform.Failed, Op: op, Err: errors.New("disk path and target are required")}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return winErr(op, err)
	}
	data, err := p.r.run(ctx, op, map[string]any{"name": vm, "prefix": checkpointPrefix}, getVM+`$old = @(Get-VMSnapshot -VM $vm | Where-Object { $_.Name -like ($prefix + '*') })
foreach ($c in $old) { Remove-VMSnapshot -VMSnapshot $c }
@{ state = [int]$vm.State; removed = $old.Count }
`)
	if err != nil {
		return err
	}
	var v struct {
		State   int `json:"state"`
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return &platform.Error{Kind: platform.Failed, Op: op, Err: err}
	}
	if v.Removed > 0 {
		if err := p.waitMerged(ctx, op, vm); err != nil {
			return err
		}
	}
	vars := map[string]any{"name": vm, "path": diskPath, "target": target}
	if st := mapState(v.State); st == platform.StateOff || st == platform.StateSaved {
		_, err := p.r.run(ctx, op, vars, getVM+snapshotPrelude+`try {
  Copy-Item -LiteralPath $full -Destination $tgt
  Set-VHD -Path $tgt -ResetDiskIdentifier -Force
} catch {
  Remove-Item -LiteralPath $tgt -Force -ErrorAction SilentlyContinue
  throw
}
`)
		return err
	}
	vars["cpname"] = checkpointPrefix + time.Now().UTC().Format("20060102T150405.000000000Z")
	_, err = p.r.run(ctx, op, vars, getVM+snapshotPrelude+`$d = Get-VMHardDiskDrive -VM $vm | Where-Object { $_.Path -and [System.IO.Path]::GetFullPath($_.Path) -eq $full } | Select-Object -First 1
if (-not $d) { throw [System.Management.Automation.ItemNotFoundException]::new("disk $path is not attached to vm $name") }
Set-VM -VM $vm -CheckpointType Standard
$cp = Checkpoint-VM -VM $vm -SnapshotName $cpname -Passthru
try {
  Copy-Item -LiteralPath $full -Destination $tgt
  Set-VHD -Path $tgt -ResetDiskIdentifier -Force
} catch {
  Remove-Item -LiteralPath $tgt -Force -ErrorAction SilentlyContinue
  throw
} finally {
  Remove-VMSnapshot -VMSnapshot $cp
}
`)
	if werr := p.waitMerged(ctx, op, vm); err == nil && werr != nil {
		_ = os.Remove(target)
		err = werr
	}
	return err
}

// snapshotPrelude sets $full and $tgt from $path and $target and refuses an
// existing target.
const snapshotPrelude = `$full = [System.IO.Path]::GetFullPath($path)
$tgt = [System.IO.Path]::GetFullPath($target)
if (Test-Path -LiteralPath $tgt) { throw [System.InvalidOperationException]::new("disk $target exists") }
`

// waitMerged waits until no hard disk drive of the VM points at an .avhdx
// file, that is until Hyper-V merged every removed checkpoint, with
// mergeTimeout as deadline.
func (p *Platform) waitMerged(ctx context.Context, op, vm string) error {
	deadline := time.Now().Add(mergeTimeout)
	for {
		data, err := p.r.run(ctx, op, map[string]any{"name": vm}, getVM+`@(Get-VMHardDiskDrive -VM $vm | Where-Object { $_.Path -like '*.avhdx' }).Count
`)
		if err != nil {
			return err
		}
		var n int
		if err := json.Unmarshal(data, &n); err != nil {
			return &platform.Error{Kind: platform.Failed, Op: op, Err: err}
		}
		if n == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return &platform.Error{Kind: platform.Failed, Op: op, Err: fmt.Errorf("checkpoint of vm %s not merged within %s", vm, mergeTimeout)}
		}
		select {
		case <-ctx.Done():
			return &platform.Error{Kind: platform.Failed, Op: op, Err: ctx.Err()}
		case <-time.After(time.Second):
		}
	}
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

// SetDiskIOPS sets MaximumIOPS of the drive holding the VHDX path on the
// VM, or of every hard disk drive with path ""; drives already at max are
// not set again (E37). A path not attached to the VM is NotFound.
func (p *Platform) SetDiskIOPS(ctx context.Context, vm, path string, max int) error {
	_, err := p.r.run(ctx, "SetDiskIOPS", map[string]any{"name": vm, "path": path, "max": max}, getVM+`$drives = @(Get-VMHardDiskDrive -VM $vm)
if ($path) {
  $full = [System.IO.Path]::GetFullPath($path)
  $drives = @($drives | Where-Object { $_.Path -and [System.IO.Path]::GetFullPath($_.Path) -eq $full } | Select-Object -First 1)
  if ($drives.Count -eq 0) { throw [System.Management.Automation.ItemNotFoundException]::new("disk $path is not attached to vm $name") }
}
foreach ($d in $drives) {
  if ($d.MaximumIOPS -ne $max) { Set-VMHardDiskDrive -VMHardDiskDrive $d -MaximumIOPS $max }
}
`)
	return err
}

// CompactDisk runs Optimize-VHD -Mode Full on the detached VHDX path; not
// mounted, Hyper-V runs it as Prezeroed and drops trimmed blocks.
func (p *Platform) CompactDisk(ctx context.Context, path string) error {
	_, err := p.r.run(ctx, "CompactDisk", map[string]any{"path": path}, `Optimize-VHD -Path $path -Mode Full`)
	return err
}

// ConvertToDynamic writes the dynamic VHDX dst from the VHD src (the
// images.Options.Convert step); src stays.
func (p *Platform) ConvertToDynamic(ctx context.Context, src, dst string) error {
	_, err := p.r.run(ctx, "ConvertToDynamic", map[string]any{"src": src, "dst": dst},
		`Convert-VHD -Path $src -DestinationPath $dst -VHDType Dynamic`)
	return err
}
