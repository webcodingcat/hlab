package servers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// rebuild replaces the server's disk with a new one from a system image or
// a project image (snapshot, backup) and keeps ID, MAC and addresses. Without user_data in the body the server's
// previous user_data is used again. A server without SSH keys gets a new
// root password, returned once.
func (m *Module) rebuild(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Image    json.RawMessage `json:"image"`
		UserData *string         `json:"user_data"`
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
	if rec.Protection.Rebuild {
		api.Fail(w, actions.ErrProtected)
		return
	}
	typ, ok := m.d.Catalog.ServerType(rec.Hlab.ServerType)
	if !ok {
		api.ServerError(w, errors.New("server type "+rec.Hlab.ServerType+" not in the catalog"))
		return
	}
	var img source
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		if img, err = m.findImage(tx, p.ID, refString(body.Image), true); err != nil {
			return err
		}
		return img.fits(typ.Disk, "image")
	}); err != nil {
		api.Fail(w, err)
		return
	}
	// The new disk replaces the old one: the server's disks can grow by
	// as much as before, so no disk is added.
	check := func(ctx context.Context) error { return m.checkResources(ctx, stor, 0, 0, 0) }
	if err := check(req.Context()); err != nil {
		api.Fail(w, err)
		return
	}

	var rootPassword any
	var rootHash string
	if len(rec.Hlab.SSHKeys) == 0 {
		pw, hash, err := newRootPassword()
		if err != nil {
			api.ServerError(w, err)
			return
		}
		rootPassword, rootHash = pw, hash
	}

	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if r.Protection.Rebuild {
			return actions.ErrProtected
		}
		action, err = acts.Start(tx, p.ID, "rebuild_server", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		r.Status = StatusRebuilding
		r.Image = img.raw
		r.Hlab.Image = img.name
		r.Hlab.ImageID = img.imageID
		if body.UserData != nil {
			r.Hlab.UserData = *body.UserData
		}
		r.Hlab.RootHash = rootHash
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}

	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		if err := m.rebuildMachine(ctx, stor, id, check); err != nil {
			// The status follows what the VM does now.
			m.settle(ctx, stor, id)
			return err
		}
		return m.setStatus(stor, id, StatusRunning)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action, "root_password": rootPassword})
}

// rebuildMachine turns the VM of server id off, deletes its disk and seed,
// lets reconcile create both again at the same paths from the stored record
// (the seed keeps instance-id, MAC and IP) and starts the VM; the VM keeps
// its drives, and the new files inherit the data folder's access for VMs.
// The image is fetched before the old disk goes, so a failed download
// leaves the server as it was.
func (m *Module) rebuildMachine(ctx context.Context, stor *store.Store, id int64, check func(context.Context) error) error {
	if err := check(ctx); err != nil {
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
	if _, err := m.baseDisk(ctx, stor, rec); err != nil {
		return err
	}
	p, name := m.d.Platform, m.vmName(id)
	if err := p.Power(ctx, name, platform.PowerStop); err != nil {
		return err
	}
	for _, f := range []string{m.diskPath(id), m.seedPath(id)} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := m.reconcile(ctx, stor, id); err != nil {
		return err
	}
	return p.Power(ctx, name, platform.PowerStart)
}
