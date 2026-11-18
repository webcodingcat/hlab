package volumes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// MaxLUN is the highest SCSI LUN a volume gets; LUN 0 is the server's own
// disk, so a server holds at most MaxLUN volumes.
const MaxLUN = 16

// serverRefType is the resource type of a server in actions.
const serverRefType = "server"

// errAttached is the answer to attaching a volume that is attached already.
func errAttached(id, server int64) error {
	return &api.FieldError{Code: "volume_already_attached", Message: fmt.Sprintf("volume %d is already attached to server %d", id, server)}
}

// checkBind refuses volume r for a server in location: attached already,
// or in another location (field names the request field).
func checkBind(r Record, location, field string) error {
	if r.Server != nil {
		return errAttached(r.ID, *r.Server)
	}
	if r.Hlab.Location != location {
		return &api.FieldError{Field: field, Message: fmt.Sprintf("volume %d is in %s, the server in %s", r.ID, r.Hlab.Location, location)}
	}
	return nil
}

// freeLUN is the lowest LUN from 1 that no volume of server uses.
func freeLUN(tx store.Tx, server int64, field string) (int, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for _, r := range recs {
		if r.Server != nil && *r.Server == server {
			used[r.Hlab.LUN] = true
		}
	}
	for lun := 1; lun <= MaxLUN; lun++ {
		if !used[lun] {
			return lun, nil
		}
	}
	return 0, &api.FieldError{Field: field, Message: fmt.Sprintf("server %d has %d volumes attached already", server, MaxLUN)}
}

// bind records volume id as attached to server at its lowest free LUN and
// adds it to the server's volumes; field names the request field of the
// server for errors.
func bind(tx store.Tx, id, server int64, automount bool, field string) (Record, error) {
	r, err := load(tx, id)
	if err != nil {
		return r, err
	}
	s, err := servers.Load(tx, server)
	if errors.Is(err, store.ErrNotFound) || err == nil && s.Hlab.ProjectID != r.Hlab.ProjectID {
		return r, &api.FieldError{Field: field, Message: fmt.Sprintf("server %d not found", server)}
	}
	if err != nil {
		return r, err
	}
	if err := checkBind(r, s.Hlab.Location, field); err != nil {
		return r, err
	}
	lun, err := freeLUN(tx, server, field)
	if err != nil {
		return r, err
	}
	r.Server = &server
	r.Assignee = &Assignee{ID: server, Type: serverRefType}
	r.Hlab.LUN = lun
	r.Hlab.Automount = automount
	if err := put(tx, r); err != nil {
		return r, err
	}
	return r, servers.SetVolumes(tx, server, append(slices.Clone(s.Volumes), id))
}

// unbind records volume id as detached and removes it from its server's
// volumes; a server already gone is skipped.
func unbind(tx store.Tx, id int64) error {
	r, err := load(tx, id)
	if err != nil || r.Server == nil {
		return err
	}
	server := *r.Server
	r.Server, r.Assignee, r.Hlab.LUN, r.Hlab.Automount = nil, nil, 0, false
	if err := put(tx, r); err != nil {
		return err
	}
	s, err := servers.Load(tx, server)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return servers.SetVolumes(tx, server, slices.DeleteFunc(slices.Clone(s.Volumes), func(v int64) bool { return v == id }))
}

// plug hot-adds the disk of the bound volume r to its server's VM; a
// failure unbinds it again.
func (m *Module) plug(ctx context.Context, stor *store.Store, r Record) error {
	err := m.d.Platform.AttachDisk(ctx, servers.VMName(m.d.VMPrefix, *r.Server), r.Hlab.Path, r.Hlab.LUN)
	if err != nil {
		if uerr := stor.Tx(func(tx store.Tx) error { return unbind(tx, r.ID) }); uerr != nil {
			err = errors.Join(err, uerr)
		}
		return err
	}
	if r.Hlab.Automount {
		return m.d.PushAgent(ctx)
	}
	return nil
}

// unplug removes the disk of the attached volume r from its server's VM
// and then records it as detached. A VM already gone is no error.
func (m *Module) unplug(ctx context.Context, stor *store.Store, r Record) error {
	err := m.d.Platform.DetachDisk(ctx, servers.VMName(m.d.VMPrefix, *r.Server), r.Hlab.Path)
	if err != nil && !isNotFound(err) {
		return err
	}
	if err := stor.Tx(func(tx store.Tx) error { return unbind(tx, r.ID) }); err != nil {
		return err
	}
	if r.Hlab.Automount {
		return m.d.PushAgent(ctx)
	}
	return nil
}

func isNotFound(err error) bool {
	var perr *platform.Error
	return errors.As(err, &perr) && perr.Kind == platform.NotFound
}

// attachRequest is POST /volumes/{id}/actions/attach.
type attachRequest struct {
	Server    *int64    `json:"server"`
	Assignee  *Assignee `json:"assignee"`
	Automount *bool     `json:"automount"`
}

func (m *Module) attach(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body attachRequest
	if !api.Decode(w, req, &body) {
		return
	}
	server, field, err := createRequest{Server: body.Server, Assignee: body.Assignee}.target()
	if err == nil && server == 0 {
		err = &api.FieldError{Field: "assignee", Message: "assignee or server is required"}
	}
	if err != nil {
		api.Fail(w, err)
		return
	}
	automount := body.Automount != nil && *body.Automount
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var rec Record
	var action *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		if _, err := store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		var err error
		if rec, err = bind(tx, id, server, automount, field); err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "attach_volume", []actions.Ref{{ID: id, Type: refType}, {ID: server, Type: serverRefType}})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		return m.plug(ctx, stor, rec)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// detach detaches the volume; a detached volume gives an action with
// nothing to do.
func (m *Module) detach(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var rec Record
	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		var err error
		if rec, err = store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		refs := []actions.Ref{{ID: id, Type: refType}}
		if rec.Server != nil {
			refs = append(refs, actions.Ref{ID: *rec.Server, Type: serverRefType})
		}
		action, err = acts.Start(tx, p.ID, "detach_volume", refs)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		if rec.Server == nil {
			return nil
		}
		return m.unplug(ctx, stor, rec)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// CheckAttach implements servers.VolumeHooks: the volumes of a server
// create must be volumes of the project in location, detached, at most
// MaxLUN and each named once.
func (m *Module) CheckAttach(tx store.Tx, projectID int64, location string, ids []int64) error {
	if len(ids) > MaxLUN {
		return &api.FieldError{Field: "volumes", Message: fmt.Sprintf("a server holds at most %d volumes", MaxLUN)}
	}
	for i, id := range ids {
		if slices.Contains(ids[:i], id) {
			return &api.FieldError{Field: "volumes", Message: fmt.Sprintf("volume %d is named twice", id)}
		}
		r, err := store.Get[Record](tx, Kind, projectID, id)
		if errors.Is(err, store.ErrNotFound) {
			return &api.FieldError{Field: "volumes", Message: fmt.Sprintf("volume %d not found", id)}
		}
		if err != nil {
			return err
		}
		if err := checkBind(r, location, "volumes"); err != nil {
			return err
		}
	}
	return nil
}

// Attach implements servers.VolumeHooks: it attaches the volumes ids to
// server serverID, whose VM exists.
func (m *Module) Attach(ctx context.Context, stor *store.Store, serverID int64, ids []int64, automount bool) error {
	for _, id := range ids {
		var r Record
		if err := stor.Tx(func(tx store.Tx) error {
			var err error
			r, err = bind(tx, id, serverID, automount, "volumes")
			return err
		}); err != nil {
			return fmt.Errorf("attach volume %d: %w", id, err)
		}
		if err := m.plug(ctx, stor, r); err != nil {
			return fmt.Errorf("attach volume %d: %w", id, err)
		}
	}
	return nil
}

// DetachAll implements servers.VolumeHooks: it detaches every volume of
// server serverID; the volume files stay.
func (m *Module) DetachAll(ctx context.Context, stor *store.Store, serverID int64) error {
	var attached []Record
	if err := stor.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		for _, r := range recs {
			if r.Server != nil && *r.Server == serverID {
				attached = append(attached, r)
			}
		}
		return err
	}); err != nil {
		return err
	}
	for _, r := range attached {
		if err := m.unplug(ctx, stor, r); err != nil {
			return fmt.Errorf("detach volume %d: %w", r.ID, err)
		}
	}
	return nil
}

// Contribute implements resource.NetContributor: the metadata of each
// server lists its attached volumes with automount. It runs after the
// servers module added the metadata.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	auto := map[int64][]int64{}
	for _, r := range recs {
		if r.Server != nil && r.Hlab.Automount {
			auto[*r.Server] = append(auto[*r.Server], r.ID)
		}
	}
	for i := range doc.Metadata {
		ids := auto[doc.Metadata[i].ServerID]
		slices.Sort(ids)
		doc.Metadata[i].Volumes = ids
	}
	return nil
}
