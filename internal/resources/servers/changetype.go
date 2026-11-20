package servers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// errNotStopped refuses an action that needs a stopped server.
var errNotStopped = &api.FieldError{Code: "server_not_stopped", Message: "the action requires a stopped server"}

// changeType gives a stopped server the cores and RAM of another server
// type and, with upgrade_disk, its disk size; the server is started again
// afterwards. Without upgrade_disk the disk and primary_disk_size stay, so
// a later change to a smaller type remains possible.
func (m *Module) changeType(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		ServerType  json.RawMessage `json:"server_type"`
		UpgradeDisk bool            `json:"upgrade_disk"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())

	var rec Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		rec, err = store.Get[Record](tx, Kind, p.ID, id)
		return err
	}); err != nil {
		api.Fail(w, err)
		return
	}
	if rec.Status != StatusOff {
		api.Fail(w, errNotStopped)
		return
	}
	typ, err := m.fittingType(refString(body.ServerType), rec)
	if err != nil {
		api.Fail(w, err)
		return
	}
	diskGB := rec.PrimaryDiskSize
	if body.UpgradeDisk {
		diskGB = typ.Disk // fittingType ensures it is not smaller
	}
	addDisk := int64(diskGB-rec.PrimaryDiskSize) << 30
	check := func(ctx context.Context) error {
		return m.checkResources(ctx, stor, typ.Cores, int64(typ.Memory*1024), addDisk)
	}
	if err := check(req.Context()); err != nil {
		api.Fail(w, err)
		return
	}

	var action *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if r.Status != StatusOff {
			return errNotStopped
		}
		action, err = acts.Start(tx, p.ID, "change_server_type", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		r.Status = StatusMigrating
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}

	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		if err := m.changeMachine(ctx, stor, id, typ, diskGB, check); err != nil {
			// The status follows what the VM does now.
			m.settle(ctx, stor, id)
			return err
		}
		return m.setStatus(stor, id, StatusRunning)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// fittingType returns the server type ref when server r can change to it:
// a known x86 type offered and not deprecated in the server's location,
// with a disk not below the server's primary_disk_size. Otherwise it
// answers invalid_server_type.
func (m *Module) fittingType(ref string, r Record) (catalog.ServerType, error) {
	invalid := func(msg string) error {
		return &api.FieldError{Code: "invalid_server_type", Message: msg}
	}
	typ, ok := m.serverType(ref)
	if !ok {
		return typ, invalid("server type " + ref + " not found")
	}
	if typ.Architecture == "arm" {
		return typ, invalid("server type " + typ.Name + " is an arm type; HLab runs only x86 servers")
	}
	if typ.Disk < r.PrimaryDiskSize {
		return typ, invalid("server type " + typ.Name + " has a smaller disk than the server")
	}
	offered, deprecated := typeAt(typ, r.Hlab.Location)
	if !offered {
		return typ, invalid("server type " + typ.Name + " is not offered in " + r.Hlab.Location)
	}
	if deprecated {
		return typ, invalid("server type " + typ.Name + " is deprecated in " + r.Hlab.Location)
	}
	return typ, nil
}

// typeAt reports whether typ is offered in location and whether it is
// deprecated there or as a whole, from the served server type object.
func typeAt(typ catalog.ServerType, location string) (offered, deprecated bool) {
	var raw struct {
		Deprecation json.RawMessage `json:"deprecation"`
		Locations   []struct {
			Name        string          `json:"name"`
			Deprecation json.RawMessage `json:"deprecation"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(typ.Raw, &raw); err != nil {
		return false, false
	}
	set := func(r json.RawMessage) bool { return len(r) > 0 && string(r) != "null" }
	for _, l := range raw.Locations {
		if l.Name == location {
			return true, set(raw.Deprecation) || set(l.Deprecation)
		}
	}
	return false, set(raw.Deprecation)
}

// changeMachine sets the cores and RAM of typ on the VM of server id,
// grows its disk to diskGB, stores the new type and size and starts the
// VM.
func (m *Module) changeMachine(ctx context.Context, stor *store.Store, id int64, typ catalog.ServerType, diskGB int, check func(context.Context) error) error {
	if err := check(ctx); err != nil {
		return err
	}
	p, name := m.d.Platform, m.vmName(id)
	if err := p.SetVM(ctx, name, m.vmSpec(name, id, typ.Cores, int64(typ.Memory*1024))); err != nil {
		return err
	}
	var rec Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		rec, err = Load(tx, id)
		return err
	}); err != nil {
		return err
	}
	if diskGB > rec.PrimaryDiskSize {
		if err := p.ResizeDisk(ctx, m.diskPath(id), int64(diskGB)<<30); err != nil {
			return err
		}
	}
	if err := stor.Tx(func(tx store.Tx) error {
		r, err := Load(tx, id)
		if err != nil {
			return err
		}
		r.ServerType = typ.Raw
		r.Hlab.ServerType = typ.Name
		r.PrimaryDiskSize = diskGB
		return put(tx, r)
	}); err != nil {
		return err
	}
	return p.Power(ctx, name, platform.PowerStart)
}
