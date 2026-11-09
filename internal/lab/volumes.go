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

// VolumeSlot returns a volume's disk in a slot of a server's VM; the
// plans take it with their values, so that the steps do not read a
// record that a plan queued behind has changed (DEVELOPING.md, Actions).
func (m *Machine) VolumeSlot(id int64, slot int, automount bool) system.VolumeSlot {
	return system.VolumeSlot{Slot: slot, Path: m.volumeFile(id), VolumeID: id, Automount: automount}
}

// VolumesRead tells whether a server's VM has taken its volumes from the
// records. A volume's attach or detach while the server's create runs
// asks it: before, the create makes the VM as the records then are, and
// the action has nothing to do; after, it waits for the create
// (WaitForServer) and changes the VM. The caller holds the lab's mutex,
// as a plan does (Machine's fields).
func (m *Machine) VolumesRead(serverID int64) bool {
	return m.volumesRead[serverID]
}

// WaitForServer waits until the actions that run on a server have ended:
// a volume's attach or detach sent during the server's create runs after
// it, as on Hetzner (measured 2026-11-09).
func (m *Machine) WaitForServer(ctx context.Context, serverID int64) error {
	for _, done := range m.lab.RunningOn([]state.ResourceRef{serverRef(serverID)}) {
		if err := waitFor(ctx, done); err != nil {
			return err
		}
	}
	return nil
}

// serverVolumes returns the volumes attached to a server, in their slots,
// for its VM.
func (m *Machine) serverVolumes(tx *state.Tx, serverID int64) []system.VolumeSlot {
	var slots []system.VolumeSlot
	for _, volume := range state.All(tx, state.Volumes) {
		if volume.ServerID == serverID {
			slots = append(slots, m.VolumeSlot(volume.ID, volume.Slot, volume.Automount))
		}
	}
	return slots
}

// CreateVolume is the create action's steps: it makes the volume's disk
// of the size, formatted in the appliance if the volume has a format, and
// the volume is available then. If the steps fail, the disk is gone again
// (system.CreateVolume) and the action's undo removes the record.
func (m *Machine) CreateVolume(ctx context.Context, id int64, sizeGB int, format string) error {
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
func (m *Machine) AttachVolume(ctx context.Context, serverID int64, volume system.VolumeSlot) error {
	return m.system.AttachVolume(ctx, ServerVM(serverID), volume)
}

// DetachVolume is the detach action's steps: the disk leaves the slot of
// the server's VM, also while it runs.
func (m *Machine) DetachVolume(ctx context.Context, serverID int64, slot int) error {
	return m.system.DetachVolume(ctx, ServerVM(serverID), slot)
}

// detachServerVolumes detaches in the records the volumes of a server
// whose VM is gone. A server's delete keeps its volumes attached until
// then, as the VM held their disks; meanwhile their actions are 423, as
// on Hetzner (measured 2026-11-09).
func detachServerVolumes(tx *state.Tx, serverID int64) {
	for _, id := range state.IDs(tx, state.Volumes, func(volume *state.Volume) bool { return volume.ServerID == serverID }) {
		if volume, err := state.Get(tx, state.Volumes, id); err == nil {
			volume.ServerID, volume.Slot = 0, 0
		}
	}
}

// ResizeVolume is the resize action's steps: the disk grows to the size
// the plan set, also while a VM uses it. The guest sees the new size at
// its next access to the disk, which a mounted file system makes within
// seconds (measured with Hyper-V); Hetzner's guest sees it at once
// (assumed, not measured).
func (m *Machine) ResizeVolume(ctx context.Context, id int64, sizeGB int) error {
	return m.system.ResizeVolume(ctx, m.volumeFile(id), sizeGB)
}

// DeleteVolumeFileLater removes a deleted volume's disk in the background,
// as Hetzner deletes a volume without an action. It is the lab's work, so
// that Close waits for it; a closing lab leaves it, and a failure is
// logged: the next start removes what is left (cleanDataFiles).
func (m *Machine) DeleteVolumeFileLater(id int64) {
	m.lab.mu.Lock()
	defer m.lab.mu.Unlock()
	if m.lab.closed {
		return
	}
	m.lab.background.Go(func() { m.deleteVolumeFile(m.lab.ctx, id) })
}

// deleteVolumeFile removes a deleted volume's disk; a failure is logged.
func (m *Machine) deleteVolumeFile(ctx context.Context, id int64) {
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
	// what a create needs, not the records: they are kept across the
	// calls to the system
	type create struct {
		id     int64
		sizeGB int
		format string
	}
	var creating []create
	err := m.lab.Change(func(tx *state.Tx) error {
		for _, volume := range state.All(tx, state.Volumes) {
			if volume.Status == state.VolumeCreating {
				creating = append(creating, create{volume.ID, volume.SizeGB, volume.Format})
			}
			if volume.ServerID == 0 {
				continue
			}
			// a server whose delete HLab's end cut short
			if _, err := state.Get(tx, state.Servers, volume.ServerID); errors.Is(err, state.ErrNotFound) {
				detachServerVolumes(tx, volume.ServerID)
			}
		}
		return nil
	})
	if err != nil {
		m.lab.logger.Warn("detaching the volumes of deleted servers", "error", err)
	}
	for _, volume := range creating {
		m.lab.logger.Warn("a volume has no disk, as HLab ended during its create; making it anew", "volume", volume.id)
		if err := m.CreateVolume(ctx, volume.id, volume.sizeGB, volume.format); err != nil && !errors.Is(err, state.ErrNotFound) {
			m.lab.logger.Error("a volume's disk could not be made; the next start tries again", "volume", volume.id, "error", err)
		}
	}
}
