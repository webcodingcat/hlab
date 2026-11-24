package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/cidata"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/store"
)

// rescueLUN is the SCSI location of the rescue disk; the server disk stays
// at LUN 0 (/dev/sda, E7).
const rescueLUN = 63

// rescueTimeout disables a rescue no boot used.
const rescueTimeout = 60 * time.Minute

func (m *Module) rescueDiskPath(id int64) string {
	return filepath.Join(m.d.Settings.DataFolder, "disks", fmt.Sprintf("server-%d-rescue.vhdx", id))
}

func (m *Module) rescueSeedPath(id int64) string {
	return filepath.Join(m.d.Settings.DataFolder, "disks", fmt.Sprintf("server-%d-rescue-cidata.iso", id))
}

// enableRescue prepares the next boot into the rescue system with the given
// keys and a new root password, returned once; it does not change the power
// state.
func (m *Module) enableRescue(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Type    *string           `json:"type"`
		SSHKeys []json.RawMessage `json:"ssh_keys"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Type != nil && *body.Type != "linux64" {
		api.Fail(w, &api.FieldError{Field: "type", Message: "type must be linux64"})
		return
	}
	password, hash, err := newRootPassword()
	if err != nil {
		api.ServerError(w, err)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())

	var keys []string
	var action *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		if _, err := store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		for _, raw := range body.SSHKeys {
			k, err := sshkeys.Find(tx, p.ID, refString(raw))
			if errors.Is(err, store.ErrNotFound) {
				return &api.FieldError{Field: "ssh_keys", Message: "ssh key " + refString(raw) + " not found"}
			}
			if err != nil {
				return err
			}
			keys = append(keys, k.PublicKey)
		}
		action, err = acts.Start(tx, p.ID, "enable_rescue", []actions.Ref{{ID: id, Type: refType}})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		return m.startRescue(ctx, stor, id, keys, hash)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"root_password": password, "action": action})
}

// disableRescue ends an enabled rescue at once: the server boots from its
// own disk with its own seed again.
func (m *Module) disableRescue(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		if _, err := store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		var err error
		action, err = acts.Start(tx, p.ID, "disable_rescue", []actions.Ref{{ID: id, Type: refType}})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		rec, err := m.load(stor, id)
		if err != nil || rec.Hlab.Rescue == nil || !rec.Hlab.Rescue.Enabled {
			return err
		}
		return m.endRescue(ctx, stor, id, false)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// startRescue replaces a previous rescue disk by a new child of the rescue
// base at LUN 63, puts the rescue seed (the server's instance-id and
// network, keys and password hash, no user_data) in DVD slot 0 and LUN 63
// first in the boot order.
func (m *Module) startRescue(ctx context.Context, stor *store.Store, id int64, keys []string, hash string) error {
	base, err := m.d.Images.EnsureRescue(ctx)
	if err != nil {
		return err
	}
	if err := m.clearRescue(ctx, stor, id); err != nil {
		return err
	}
	rec, err := m.load(stor, id)
	if err != nil {
		return err
	}
	disk, seedPath := m.rescueDiskPath(id), m.rescueSeedPath(id)
	if err := os.Remove(seedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := m.d.Platform.CreateChildDisk(ctx, disk, base); err != nil {
		return err
	}
	// Recorded at once, so the disk is removed later even if a step fails.
	if err := m.updateRescue(stor, id, func(r *Record) { r.Hlab.Rescue = &Rescue{DiskPath: disk} }); err != nil {
		return err
	}
	p, name := m.d.Platform, m.vmName(id)
	if err := p.AttachDisk(ctx, name, disk, rescueLUN); err != nil {
		return err
	}
	seed := m.seed(rec)
	seed.SSHKeys = keys
	seed.UserData = ""
	seed.VendorData = vendorData(hash, rec.Name)
	seed.NoOnLink = false // the rescue system is Debian, whatever the server's image
	if err := cidata.BuildFile(seed, seedPath); err != nil {
		return err
	}
	err = p.SetDVD(ctx, name, 0, seedPath)
	if err == nil {
		err = p.SetBootOrder(ctx, name, bootOrder(true, rec.Hlab.ISO != ""))
	}
	if err != nil {
		return errors.Join(err, m.restoreBoot(ctx, stor, id))
	}
	now := m.d.Clock.Now()
	return m.updateRescue(stor, id, func(r *Record) {
		r.Hlab.Rescue = &Rescue{Enabled: true, Since: now, DiskPath: disk, RootHash: hash, SSHKeys: keys}
	})
}

// bootPower runs power operation op on the VM of server id. A start,
// reboot or reset while rescue is enabled boots the rescue system and ends
// rescue; one without rescue removes a rescue disk left from before.
func (m *Module) bootPower(ctx context.Context, stor *store.Store, id int64, op platform.PowerOp) error {
	if op == platform.PowerReboot {
		rec, err := m.load(stor, id)
		if err != nil {
			return err
		}
		if res := rec.Hlab.Rescue; res != nil && !res.Enabled && res.Booted {
			return m.rebootFromRescue(ctx, stor, id)
		}
	}
	if err := m.d.Platform.Power(ctx, m.vmName(id), op); err != nil {
		return err
	}
	if op != platform.PowerStart && op != platform.PowerReboot && op != platform.PowerReset {
		return nil
	}
	rec, err := m.load(stor, id)
	if err != nil || rec.Hlab.Rescue == nil {
		return err
	}
	if !rec.Hlab.Rescue.Enabled {
		if err := m.dropRescueDisk(ctx, stor, id); err != nil {
			// The status watch removes it once the VM is seen off.
			slog.Warn("remove the rescue disk after a boot", "id", id, "err", err)
		}
		return nil
	}
	// The rescue system reads its seed early in its first boot.
	if hold := m.d.RescueSeedHold; hold > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.d.Clock.After(hold):
		}
	}
	return m.endRescue(ctx, stor, id, true)
}

// rescueOffTimeout bounds the wait for the guest of a rebooted rescue
// system to turn the VM off.
const rescueOffTimeout = 2 * time.Minute

// rebootFromRescue reboots a server whose guest runs the rescue system from
// the rescue disk. A guest reboot keeps that disk in use while the guest
// shuts down, so it cannot be detached then: the guest is shut down, the
// rescue disk removed once the VM is off and the VM started again.
func (m *Module) rebootFromRescue(ctx context.Context, stor *store.Store, id int64) error {
	p, name := m.d.Platform, m.vmName(id)
	if err := p.Power(ctx, name, platform.PowerShutdown); err != nil {
		return err
	}
	deadline := m.d.Clock.Now().Add(rescueOffTimeout)
	for {
		st, err := p.State(ctx, name)
		if err != nil {
			return err
		}
		if st == platform.StateOff {
			break
		}
		if !m.d.Clock.Now().Before(deadline) {
			return fmt.Errorf("vm %s did not turn off within %s", name, rescueOffTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.d.Clock.After(time.Second):
		}
	}
	if err := m.dropRescueDisk(ctx, stor, id); err != nil {
		return err
	}
	return p.Power(ctx, name, platform.PowerStart)
}

// watchRescue disables a rescue unused for 60 minutes and removes the
// rescue disk of a server whose VM was seen off (state).
func (m *Module) watchRescue(ctx context.Context, recs []Record, locked map[int64]bool, state map[int64]platform.VMState) {
	now := m.d.Clock.Now()
	for _, r := range recs {
		res := r.Hlab.Rescue
		if res == nil || locked[r.ID] || r.Status == StatusInitializing || r.Status == StatusDeleting {
			continue
		}
		var err error
		switch st, seen := state[r.ID]; {
		case res.Enabled && !now.Before(res.Since.Add(rescueTimeout)):
			slog.Info("rescue disabled after 60 minutes without a boot", "id", r.ID)
			err = m.endRescue(ctx, m.d.Store, r.ID, false)
		case !res.Enabled && res.DiskPath != "" && seen && st == platform.StateOff:
			err = m.dropRescueDisk(ctx, m.d.Store, r.ID)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Error("server rescue watch", "id", r.ID, "err", err)
		}
	}
}

// clearRescue ends an enabled rescue and removes the rescue disk.
func (m *Module) clearRescue(ctx context.Context, stor *store.Store, id int64) error {
	rec, err := m.load(stor, id)
	if err != nil || rec.Hlab.Rescue == nil {
		return err
	}
	if rec.Hlab.Rescue.Enabled {
		if err := m.endRescue(ctx, stor, id, false); err != nil {
			return err
		}
	}
	return m.dropRescueDisk(ctx, stor, id)
}

// endRescue restores LUN 0 first and the server's seed and records rescue
// as disabled; the rescue disk stays until it can be removed.
func (m *Module) endRescue(ctx context.Context, stor *store.Store, id int64, booted bool) error {
	if err := m.restoreBoot(ctx, stor, id); err != nil {
		return err
	}
	return m.updateRescue(stor, id, func(r *Record) {
		if r.Hlab.Rescue != nil {
			r.Hlab.Rescue.Enabled = false
			r.Hlab.Rescue.Booted = booted
		}
	})
}

// restoreBoot sets the boot order without rescue (an attached ISO, then
// LUN 0), puts the server's seed in DVD slot 0 and deletes the rescue seed.
func (m *Module) restoreBoot(ctx context.Context, stor *store.Store, id int64) error {
	rec, err := m.load(stor, id)
	if err != nil {
		return err
	}
	p, name := m.d.Platform, m.vmName(id)
	if err := p.SetBootOrder(ctx, name, bootOrder(false, rec.Hlab.ISO != "")); err != nil {
		return err
	}
	if err := p.SetDVD(ctx, name, 0, m.seedPath(id)); err != nil {
		return err
	}
	if err := os.Remove(m.rescueSeedPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("delete the rescue seed", "id", id, "err", err)
	}
	return nil
}

// dropRescueDisk detaches and deletes the rescue disk of server id, whose
// rescue is not enabled, and forgets the rescue.
func (m *Module) dropRescueDisk(ctx context.Context, stor *store.Store, id int64) error {
	rec, err := m.load(stor, id)
	if err != nil || rec.Hlab.Rescue == nil {
		return err
	}
	if disk := rec.Hlab.Rescue.DiskPath; disk != "" {
		if err := m.d.Platform.DetachDisk(ctx, m.vmName(id), disk); err != nil && !isNotFound(err) {
			return err
		}
		if err := os.Remove(disk); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return m.updateRescue(stor, id, func(r *Record) { r.Hlab.Rescue = nil })
}

// updateRescue applies change to server id and keeps rescue_enabled in
// step with the rescue state.
func (m *Module) updateRescue(stor *store.Store, id int64, change func(*Record)) error {
	return stor.Tx(func(tx store.Tx) error {
		r, err := Load(tx, id)
		if err != nil {
			return err
		}
		change(&r)
		r.RescueEnabled = r.Hlab.Rescue != nil && r.Hlab.Rescue.Enabled
		return put(tx, r)
	})
}

// load reads server id.
func (m *Module) load(stor *store.Store, id int64) (Record, error) {
	var rec Record
	err := stor.Tx(func(tx store.Tx) error {
		var err error
		rec, err = Load(tx, id)
		return err
	})
	return rec, err
}
