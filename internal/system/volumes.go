package system

import (
	"context"
	"errors"
	"fmt"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/vhdx"
)

// volumeMarker starts the disk ID of every volume: "HLAB", which the
// guest's udev rule looks for in the WWN (internal/seed).
var volumeMarker = [4]byte{'H', 'L', 'A', 'B'}

// VolumeAutomount is the flag in a volume's disk ID that asks the guest to
// mount it.
const VolumeAutomount = 1

// VolumeDiskID returns the disk ID of a volume: the marker in bytes 0 to
// 3, the flags in byte 4, and the volume's ID as 12 decimal digits in
// bytes 10 to 15, two to a byte. Hyper-V's WWN shows bytes 0 to 5 and 10
// to 15 (vhdx.WWN), so the guest reads the ID in decimal from the WWN's
// hex digits, without arithmetic in a udev rule.
func VolumeDiskID(id int64, automount bool) [16]byte {
	var diskID [16]byte
	copy(diskID[:4], volumeMarker[:])
	if automount {
		diskID[4] = VolumeAutomount
	}
	digits := fmt.Sprintf("%012d", id%1_000_000_000_000)
	for i := range 6 {
		diskID[10+i] = (digits[2*i]-'0')<<4 | (digits[2*i+1] - '0')
	}
	return diskID
}

// CreateVolume creates a volume's disk, an empty dynamic disk, and with a
// file system makes it on the disk in the appliance. A disk that is there
// already, from a create that HLab's end cut short, is made anew.
func (s *System) CreateVolume(ctx context.Context, appliance *Appliance, path string, sizeGB int, volumeID int64, fileSystem string) error {
	hypervisor := s.platform.Hypervisor
	if err := hypervisor.DeleteDisk(ctx, path); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("removing what an earlier create left of %s: %w", path, err)
	}
	if err := hypervisor.CreateDisk(ctx, path, sizeGB, ""); err != nil {
		return fmt.Errorf("creating the volume %s: %w", path, err)
	}
	if fileSystem == "" {
		return nil
	}
	if appliance == nil {
		return errors.Join(errors.New("the appliance, which formats volumes, does not run"),
			s.DeleteVolume(context.WithoutCancel(ctx), path))
	}
	if err := appliance.FormatDisk(ctx, path, VolumeDiskID(volumeID, false), fileSystem); err != nil {
		return errors.Join(fmt.Errorf("formatting the volume %s: %w", path, err),
			s.DeleteVolume(context.WithoutCancel(ctx), path))
	}
	return nil
}

// VolumeSlot is a volume's disk in a slot of a server's VM.
type VolumeSlot struct {
	Slot      int
	Path      string
	VolumeID  int64
	Automount bool // the guest mounts it
}

// disk returns the slot for the platform, with the volume's disk ID.
func (v VolumeSlot) disk() platform.DiskSlot {
	return platform.DiskSlot{Slot: v.Slot, Path: v.Path, ID: VolumeDiskID(v.VolumeID, v.Automount)}
}

// AttachVolume puts a volume's disk into a slot of a server's VM, also
// while it runs, with the disk ID the guest names it by.
func (s *System) AttachVolume(ctx context.Context, vm string, volume VolumeSlot) error {
	if err := s.platform.Hypervisor.AttachDisk(ctx, vm, volume.disk()); err != nil {
		return fmt.Errorf("attaching %s to %s: %w", volume.Path, vm, err)
	}
	return nil
}

// DetachVolume takes a volume's disk out of a slot of a server's VM, also
// while it runs. A VM that is gone, or a slot that is empty already,
// leaves nothing to do.
func (s *System) DetachVolume(ctx context.Context, vm string, slot int) error {
	err := s.platform.Hypervisor.DetachDisk(ctx, vm, slot)
	if err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("detaching slot %d of %s: %w", slot, vm, err)
	}
	return nil
}

// ResizeVolume grows a volume's disk, also while a running VM uses it.
// The guest sees the new size at its next access to the disk.
func (s *System) ResizeVolume(ctx context.Context, path string, sizeGB int) error {
	if err := s.platform.Hypervisor.ResizeDisk(ctx, path, sizeGB); err != nil {
		return fmt.Errorf("resizing the volume %s: %w", path, err)
	}
	return nil
}

// DeleteVolume deletes a volume's disk; one that is gone already is fine.
func (s *System) DeleteVolume(ctx context.Context, path string) error {
	err := s.platform.Hypervisor.DeleteDisk(ctx, path)
	if err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("deleting the volume %s: %w", path, err)
	}
	return nil
}

// formatSlot is the appliance's slot for a volume it formats; formats run
// one at a time (Appliance.formatMu).
const formatSlot = 63

// FormatDisk makes a file system on a disk: it attaches the disk to the
// appliance with its ID, lets the agent format it, found by its WWN, and
// detaches it again.
func (a *Appliance) FormatDisk(ctx context.Context, path string, id [16]byte, fileSystem string) error {
	a.formatMu.Lock()
	defer a.formatMu.Unlock()
	hypervisor := a.system.platform.Hypervisor
	if err := hypervisor.AttachDisk(ctx, a.spec.VM, platform.DiskSlot{Slot: formatSlot, Path: path, ID: id}); err != nil {
		return fmt.Errorf("attaching %s to the appliance: %w", path, err)
	}
	formatErr := a.agent.format(ctx, agentproto.Format{WWN: vhdx.WWN(id), FileSystem: fileSystem})
	if err := hypervisor.DetachDisk(context.WithoutCancel(ctx), a.spec.VM, formatSlot); err != nil {
		return errors.Join(formatErr, fmt.Errorf("detaching %s from the appliance: %w", path, err))
	}
	return formatErr
}
