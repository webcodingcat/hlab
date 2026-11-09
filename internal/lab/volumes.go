package lab

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// VolumeDir is the folder of the volumes' disks in the data folder:
// <id>.vhdx.
const VolumeDir = "disks/volumes"

// VolumeResource is how actions name a volume.
const VolumeResource = "volume"

// VolumeRef names a volume as actions and flags do.
func VolumeRef(id int64) state.ResourceRef {
	return state.ResourceRef{Type: VolumeResource, ID: id}
}

// volumeFile returns the path of a volume's disk.
func (m *Machine) volumeFile(id int64) string {
	return filepath.Join(m.lab.dataDir, VolumeDir, fmt.Sprint(id)+".vhdx")
}

// volumeSlot returns a volume's disk in its slot of its server's VM.
func (m *Machine) volumeSlot(volume *state.Volume) system.VolumeSlot {
	return system.VolumeSlot{Slot: volume.Slot, Path: m.volumeFile(volume.ID), VolumeID: volume.ID, Automount: volume.Automount}
}

// serverVolumes returns the volumes attached to a server, in their slots,
// for its VM.
func (m *Machine) serverVolumes(tx *state.Tx, serverID int64) []system.VolumeSlot {
	var slots []system.VolumeSlot
	for _, volume := range state.All(tx, state.Volumes) {
		if volume.ServerID == serverID {
			slots = append(slots, m.volumeSlot(volume))
		}
	}
	return slots
}

// CreateVolume is the create action's steps: it makes the volume's disk,
// formatted in the appliance if the volume has a format, and the volume
// is available then. If the steps fail, the disk is gone again
// (system.CreateVolume) and the action's undo removes the record.
func (m *Machine) CreateVolume(ctx context.Context, id int64) error {
	volume, err := Current(m.lab, state.Volumes, id)
	if err != nil {
		return err
	}
	sizeGB, format := volume.SizeGB, volume.Format // not kept across the system's call
	m.lab.mu.Lock()
	appliance := m.appliance
	m.lab.mu.Unlock()
	if err := m.system.CreateVolume(ctx, appliance, m.volumeFile(id), sizeGB, id, format); err != nil {
		return err
	}
	return m.lab.Change(func(tx *state.Tx) error {
		volume, err := state.Get(tx, state.Volumes, id)
		if err != nil {
			return err
		}
		volume.Status = state.VolumeAvailable
		return nil
	})
}

// AttachVolume is the attach action's steps: the volume's disk goes into
// its slot of the server's VM, also while it runs, with the disk ID that
// names it in the guest (and asks for the mount if the attach did).
func (m *Machine) AttachVolume(ctx context.Context, id int64) error {
	volume, err := Current(m.lab, state.Volumes, id)
	if err != nil {
		return err
	}
	return m.system.AttachVolume(ctx, ServerVM(volume.ServerID), m.volumeSlot(volume))
}

// DetachVolume is the detach action's steps: the disk leaves the slot of
// the server's VM, also while it runs.
func (m *Machine) DetachVolume(ctx context.Context, serverID int64, slot int) error {
	return m.system.DetachVolume(ctx, ServerVM(serverID), slot)
}

// releaseVolumes detaches in the records the volumes of a server that is
// gone. A server's delete keeps its volumes attached until its VM is
// gone, which held their disks; meanwhile their actions are 423, as on
// Hetzner (measured 2026-11-09).
func (m *Machine) releaseVolumes(serverID int64) error {
	return m.lab.Change(func(tx *state.Tx) error {
		releaseVolumes(tx, serverID)
		return nil
	})
}

// releaseVolumes detaches the volumes of a server in the records.
func releaseVolumes(tx *state.Tx, serverID int64) {
	for _, id := range state.IDs(tx, state.Volumes, func(volume *state.Volume) bool { return volume.ServerID == serverID }) {
		if volume, err := state.Get(tx, state.Volumes, id); err == nil {
			volume.ServerID, volume.Slot = 0, 0
		}
	}
}

// ResizeVolume is the resize action's steps: the disk grows to the size
// the plan set, also while a VM uses it. The guest sees the new size at
// its next access to the disk (measured with Hyper-V); Hetzner's guest
// sees it at once (assumed, not measured).
func (m *Machine) ResizeVolume(ctx context.Context, id int64) error {
	volume, err := Current(m.lab, state.Volumes, id)
	if err != nil {
		return err
	}
	return m.system.ResizeVolume(ctx, m.volumeFile(id), volume.SizeGB)
}

// DeleteVolumeFile removes a deleted volume's disk. The API's delete
// calls it after the change, as Hetzner deletes a volume without an
// action; a failure is logged, and the next start removes what is left
// (cleanDataFiles).
func (m *Machine) DeleteVolumeFile(ctx context.Context, id int64) {
	if err := m.system.DeleteVolume(ctx, m.volumeFile(id)); err != nil {
		m.lab.logger.Warn("removing a deleted volume's disk; the next start removes it", "volume", id, "error", err)
	}
}

// resumeVolumes detaches the volumes of servers that are gone, and makes
// the disks anew of the volumes whose create HLab's end cut short: their
// action ended with an error at the start, but the record stays, as on
// Hetzner a volume does not vanish. A failure leaves the volume creating;
// the next start tries again.
func (m *Machine) resumeVolumes(ctx context.Context) {
	var creating []int64
	err := m.lab.Change(func(tx *state.Tx) error {
		creating = state.IDs(tx, state.Volumes, func(volume *state.Volume) bool { return volume.Status == state.VolumeCreating })
		// the servers whose delete HLab's end cut short
		for _, volume := range state.All(tx, state.Volumes) {
			if _, err := state.Get(tx, state.Servers, volume.ServerID); volume.ServerID != 0 && err != nil {
				releaseVolumes(tx, volume.ServerID)
			}
		}
		return nil
	})
	if err != nil {
		m.lab.logger.Warn("detaching the volumes of deleted servers", "error", err)
	}
	for _, id := range creating {
		m.lab.logger.Warn("a volume has no disk, as HLab ended during its create; making it anew", "volume", id)
		if err := m.CreateVolume(ctx, id); err != nil && !errors.Is(err, state.ErrNotFound) {
			m.lab.logger.Error("a volume's disk could not be made; the next start tries again", "volume", id, "error", err)
		}
	}
}
