package servers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// isoSlot is the DVD slot of an attached ISO; slot 0 holds the seed.
const isoSlot = 1

// bootOrder is the boot order of a server: the rescue disk first while
// rescue is enabled, then an attached ISO, then the server's disk.
func bootOrder(rescue, iso bool) []platform.BootDevice {
	var order []platform.BootDevice
	if rescue {
		order = append(order, platform.BootDevice{Kind: "disk", Slot: rescueLUN})
	}
	if iso {
		order = append(order, platform.BootDevice{Kind: "dvd", Slot: isoSlot})
	}
	return append(order, platform.BootDevice{Kind: "disk", Slot: 0})
}

// rescueOn reports whether rescue is enabled on rec.
func rescueOn(rec Record) bool { return rec.Hlab.Rescue != nil && rec.Hlab.Rescue.Enabled }

// attachISO inserts an ISO into DVD slot 1 and boots it before the
// server's disk; an attached ISO is replaced. A catalog ISO is taken by name
// or ID, an own ISO by ID only. The ISO is fetched into the cache inside the
// action, under its cache name.
func (m *Module) attachISO(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		ISO json.RawMessage `json:"iso"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	ref := refString(body.ISO)
	iso, found := m.d.Catalog.ISO(ref)
	if !found || iso.Deprecated {
		api.Fail(w, &api.FieldError{Field: "iso", Message: "iso " + ref + " not found"})
		return
	}
	m.isoAction(w, req, id, "attach_iso", func(ctx context.Context, stor *store.Store) error {
		path, err := m.d.Images.EnsureISO(ctx, iso.CacheName)
		if err != nil {
			return err
		}
		return m.setISO(ctx, stor, id, path, iso.CacheName, iso.Raw)
	})
}

// detachISO ejects the attached ISO and boots the server's disk first
// again; a server without ISO is left as it is.
func (m *Module) detachISO(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	m.isoAction(w, req, id, "detach_iso", func(ctx context.Context, stor *store.Store) error {
		rec, err := m.load(stor, id)
		if err != nil || rec.Hlab.ISO == "" {
			return err
		}
		return m.setISO(ctx, stor, id, "", "", nil)
	})
}

// isoAction starts the action command on server id and runs work in it.
func (m *Module) isoAction(w http.ResponseWriter, req *http.Request, id int64, command string, work func(context.Context, *store.Store) error) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		if _, err := store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		var err error
		action, err = acts.Start(tx, p.ID, command, []actions.Ref{{ID: id, Type: refType}})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		return work(ctx, stor)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// setISO puts the ISO file path ("" ejects) into DVD slot 1 of server id,
// sets the boot order to match and records the ISO's cache name and object.
func (m *Module) setISO(ctx context.Context, stor *store.Store, id int64, path, name string, object json.RawMessage) error {
	rec, err := m.load(stor, id)
	if err != nil {
		return err
	}
	p, vm := m.d.Platform, m.vmName(id)
	if err := p.SetDVD(ctx, vm, isoSlot, path); err != nil {
		return err
	}
	if err := p.SetBootOrder(ctx, vm, bootOrder(rescueOn(rec), path != "")); err != nil {
		return err
	}
	return stor.Tx(func(tx store.Tx) error {
		r, err := Load(tx, id)
		if err != nil {
			return err
		}
		r.Hlab.ISO = name
		r.ISO = object
		return put(tx, r)
	})
}
