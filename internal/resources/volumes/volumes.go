// Package volumes serves Hetzner's volumes of a project. A volume is a thin
// VHDX in the data folder whose page 83 identifier carries the volume ID;
// the disk work runs in the module's actions (resource.Reconciler).
package volumes

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of volumes.
const Kind = "volumes"

// refType is the resource type of a volume in actions.
const refType = "volume"

// Size limits of a volume in GB.
const (
	MinSize = 10
	MaxSize = 10240
)

// Volume statuses.
const (
	StatusCreating  = "creating"
	StatusAvailable = "available"
)

// Deps are what the module needs.
type Deps struct {
	Catalog *catalog.Catalog
	// Store is what Reconcile and Delete work on outside a request
	// (project deletion); the handlers use the store of the request.
	Store      *store.Store
	Platform   platform.Platform
	DataFolder string // volume disks in <DataFolder>\volumes
	VMPrefix   string
	// CheckDisk is the disk rule (servers.Module.CheckDisk); nil = no check.
	CheckDisk func(ctx context.Context, stor *store.Store, addBytes int64) error
	// ApplianceJob runs job on the appliance with diskPath hot-added; the
	// cloud sets job.LUN.
	ApplianceJob func(ctx context.Context, diskPath string, job agentapi.Job) error
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
}

// Volume is a volume in Hetzner's API shape.
type Volume struct {
	ID          int64             `json:"id"`
	Created     time.Time         `json:"created"`
	Name        string            `json:"name"`
	Server      *int64            `json:"server"`
	Assignee    *Assignee         `json:"assignee"`
	Location    json.RawMessage   `json:"location"`
	Size        int               `json:"size"` // GB
	LinuxDevice string            `json:"linux_device"`
	Protection  Protection        `json:"protection"`
	Labels      map[string]string `json:"labels"`
	Status      string            `json:"status"`
	Format      *string           `json:"format"`
}

// Assignee is the resource a volume is attached to.
type Assignee struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Protection is the protection of a volume.
type Protection struct {
	Delete bool `json:"delete"`
}

// Internal are the fields hlab keeps beside the API shape (`_hlab`).
type Internal struct {
	ProjectID int64  `json:"project_id"`
	Location  string `json:"location"`
	Path      string `json:"path"` // <data>\volumes\volume-<id>.vhdx
	// LUN is the SCSI LUN of the disk in its server's VM while attached.
	LUN int `json:"lun,omitempty"`
	// Automount is set while attached with automount.
	Automount bool `json:"automount,omitempty"`
	// Pending is the attach a running action has started and commits at
	// success; nil without one.
	Pending *Pending `json:"pending,omitempty"`
	// Detaching is set while a detach whose disk has left the server's VM
	// pushes the agent document; the volume shows attached until then.
	Detaching bool `json:"detaching,omitempty"`
}

// Pending is the attach of a volume the running action Action works on:
// the agent document serves it while that action runs, GET shows it once
// the action has succeeded.
type Pending struct {
	Action    int64 `json:"action"`
	Server    int64 `json:"server"`
	LUN       int   `json:"lun"`
	Automount bool  `json:"automount"`
}

// Record is the stored document of a volume.
type Record struct {
	Volume
	Hlab Internal `json:"_hlab"`
}

// Disk is the disk of a volume for the disk rule.
type Disk struct {
	Path         string
	NominalBytes int64
}

// Disks returns the disk of every volume of every project.
func Disks(tx store.Tx) ([]Disk, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return nil, err
	}
	out := make([]Disk, 0, len(recs))
	for _, r := range recs {
		out = append(out, Disk{Path: r.Hlab.Path, NominalBytes: int64(r.Size) << 30})
	}
	return out, nil
}

// Page83 is the page 83 identifier of the disk of volume id:
// 484C4142-0000-4000-8000-<id as 12 hex digits> in text byte order.
func Page83(id int64) [16]byte {
	b := [16]byte{0x48, 0x4C, 0x41, 0x42, 0x00, 0x00, 0x40, 0x00, 0x80, 0x00}
	for i := range 6 {
		b[15-i] = byte(id >> (8 * i))
	}
	return b
}

// LinuxDevice is the guest's device path of volume id.
func LinuxDevice(id int64) string {
	return fmt.Sprintf("/dev/disk/by-id/scsi-0HC_Volume_%d", id)
}

// Module is the volumes resource type.
type Module struct {
	d Deps
}

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /volumes", http.HandlerFunc(m.list))
	r.Handle("POST /volumes", http.HandlerFunc(m.create))
	r.Handle("GET /volumes/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /volumes/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /volumes/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /volumes/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /volumes/{id}/actions/resize", http.HandlerFunc(m.resize))
	r.Handle("POST /volumes/{id}/actions/attach", http.HandlerFunc(m.attach))
	r.Handle("POST /volumes/{id}/actions/detach", http.HandlerFunc(m.detach))
	resactions.RoutesFor(r, Kind, refType)
}

// load returns volume id of any project, or store.ErrNotFound.
func load(tx store.Tx, id int64) (Record, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return Record{}, err
	}
	for _, r := range recs {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, store.ErrNotFound
}

func put(tx store.Tx, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

// checkName refuses an empty name and one another volume of the project has.
func checkName(tx store.Tx, projectID int64, name string, exceptID int64) error {
	if strings.TrimSpace(name) == "" {
		return &api.FieldError{Field: "name", Message: "name is required"}
	}
	taken, err := store.NameTaken(tx, Kind, projectID, name, exceptID)
	if err != nil {
		return err
	}
	if taken {
		return &api.FieldError{Code: "uniqueness_error", Field: "name", Message: "name is already used"}
	}
	return nil
}

// checkSize refuses a size that is missing, not an integer or outside
// 10 to 10240 GB.
func checkSize(size *float64) (int, error) {
	if size == nil {
		return 0, &api.FieldError{Field: "size", Message: "size is required"}
	}
	if *size != math.Trunc(*size) || *size < MinSize || *size > MaxSize {
		return 0, &api.FieldError{Field: "size", Message: fmt.Sprintf("size must be an integer from %d to %d GB", MinSize, MaxSize)}
	}
	return int(*size), nil
}

// location finds a catalog location by name or ID.
func (m *Module) location(ref string) (catalog.Location, bool) {
	if l, ok := m.d.Catalog.Location(ref); ok {
		return l, true
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	for _, l := range m.d.Catalog.Locations {
		if err == nil && l.ID == id {
			return l, true
		}
	}
	return catalog.Location{}, false
}

var listOptions = api.ListOptions[Volume]{
	Name:   func(v Volume) string { return v.Name },
	Labels: func(v Volume) map[string]string { return v.Labels },
	Sort: map[string]func(a, b Volume) int{
		"id":      func(a, b Volume) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Volume) int { return strings.Compare(a.Name, b.Name) },
		"created": func(a, b Volume) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Volume, []string) bool{
		"status": func(v Volume, s []string) bool { return slices.Contains(s, v.Status) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []Volume
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		out = make([]Volume, 0, len(recs))
		for _, r := range recs {
			out = append(out, r.Volume)
		}
		return err
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.ListRequest(w, req, Kind, out, listOptions)
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		r, err = store.Get[Record](tx, Kind, p.ID, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"volume": r.Volume})
}

// createRequest is POST /volumes.
type createRequest struct {
	Name      string            `json:"name"`
	Size      *float64          `json:"size"`
	Labels    map[string]string `json:"labels"`
	Automount *bool             `json:"automount"`
	Format    *string           `json:"format"`
	Location  json.RawMessage   `json:"location"`
	Server    *int64            `json:"server"`
	Assignee  *Assignee         `json:"assignee"`
}

// target returns the server named by assignee or the deprecated server
// field and the field it came from; 0 when neither is given.
func (b createRequest) target() (int64, string, error) {
	switch {
	case b.Assignee != nil:
		if b.Assignee.Type != "server" {
			return 0, "assignee", &api.FieldError{Field: "assignee", Message: "assignee type must be server"}
		}
		return b.Assignee.ID, "assignee", nil
	case b.Server != nil:
		return *b.Server, "server", nil
	}
	return 0, "", nil
}

// check validates what needs no store: size, format, automount.
func (b createRequest) check(serverID int64) (int, error) {
	size, err := checkSize(b.Size)
	if err != nil {
		return 0, err
	}
	if b.Format != nil && *b.Format != "ext4" && *b.Format != "xfs" {
		return 0, &api.FieldError{Field: "format", Message: "format must be ext4 or xfs"}
	}
	if b.Automount != nil && *b.Automount && serverID == 0 {
		return 0, &api.FieldError{Field: "automount", Message: "automount needs a server"}
	}
	return size, nil
}

// resolveLocation is the volume's location: the server's, which a given
// location must equal, or the given one.
func (m *Module) resolveLocation(tx store.Tx, projectID int64, b createRequest, serverID int64, serverField string) (catalog.Location, error) {
	ref := refString(b.Location)
	var loc catalog.Location
	if ref != "" {
		l, ok := m.location(ref)
		if !ok {
			return loc, &api.FieldError{Field: "location", Message: "location " + ref + " not found"}
		}
		loc = l
	}
	if serverID == 0 {
		if ref == "" {
			return loc, &api.FieldError{Field: "location", Message: "location or server is required"}
		}
		return loc, nil
	}
	s, err := servers.Load(tx, serverID)
	if errors.Is(err, store.ErrNotFound) || err == nil && s.Hlab.ProjectID != projectID {
		return loc, &api.FieldError{Field: serverField, Message: fmt.Sprintf("server %d not found", serverID)}
	}
	if err != nil {
		return loc, err
	}
	if ref != "" && loc.Name != s.Hlab.Location {
		return loc, &api.FieldError{Field: "location", Message: "location " + loc.Name + " differs from the server's location " + s.Hlab.Location}
	}
	l, ok := m.d.Catalog.Location(s.Hlab.Location)
	if !ok {
		return loc, fmt.Errorf("location %q of server %d not in the catalog", s.Hlab.Location, serverID)
	}
	return l, nil
}

func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	serverID, serverField, err := body.target()
	if err != nil {
		api.Fail(w, err)
		return
	}
	size, err := body.check(serverID)
	if err != nil {
		api.Fail(w, err)
		return
	}
	labels := body.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	if err := api.CheckLabels(labels); err != nil {
		api.Fail(w, err)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	if err := stor.Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		_, err := m.resolveLocation(tx, p.ID, body, serverID, serverField)
		return err
	}); err != nil {
		api.Fail(w, err)
		return
	}
	if err := m.checkDisk(req.Context(), stor, int64(size)<<30); err != nil {
		api.Fail(w, err)
		return
	}

	var rec Record
	var action, attach *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		loc, err := m.resolveLocation(tx, p.ID, body, serverID, serverField)
		if err != nil {
			return err
		}
		id, err := store.NextID(tx, Kind)
		if err != nil {
			return err
		}
		rec = Record{
			Volume: Volume{
				ID:          id,
				Created:     time.Now().UTC().Truncate(time.Second),
				Name:        body.Name,
				Location:    loc.Raw,
				Size:        size,
				LinuxDevice: LinuxDevice(id),
				Labels:      labels,
				Status:      StatusCreating,
				Format:      body.Format,
			},
			Hlab: Internal{ProjectID: p.ID, Location: loc.Name, Path: m.diskPath(id)},
		}
		if err := put(tx, rec); err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "create_volume", []actions.Ref{{ID: id, Type: refType}})
		if err != nil || serverID == 0 {
			return err
		}
		// The attach queues behind the creation, which holds the new volume,
		// and behind the actions on the server; nothing else can hold the
		// volume yet, so it refuses on nothing.
		attach, err = acts.StartQueued(tx, p.ID, "attach_volume",
			[]actions.Ref{{ID: id, Type: refType}, {ID: serverID, Type: serverRefType}}, nil)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	id := rec.ID
	automount := body.Automount != nil && *body.Automount
	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		err := m.reconcile(ctx, stor, id)
		if err != nil {
			m.drop(stor, rec)
		}
		return err
	})
	if attach != nil {
		attachID := attach.ID
		acts.Run(attachID, func(ctx context.Context, _ func(int)) error {
			return m.attachWork(ctx, stor, attachID, id, serverID, automount, serverField)
		})
	}
	next := []any{}
	if attach != nil {
		next = append(next, attach)
	}
	api.JSON(w, http.StatusCreated, map[string]any{
		"volume":       rec.Volume,
		"action":       action,
		"next_actions": next,
	})
}

// checkDisk applies the disk rule when it is wired.
func (m *Module) checkDisk(ctx context.Context, stor *store.Store, addBytes int64) error {
	if m.d.CheckDisk == nil {
		return nil
	}
	return m.d.CheckDisk(ctx, stor, addBytes)
}

// update changes only the fields present in the body; labels, when
// present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name   *string            `json:"name"`
		Labels *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		if body.Name != nil {
			if err := checkName(tx, p.ID, *body.Name, id); err != nil {
				return err
			}
			r.Name = *body.Name
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			r.Labels = *body.Labels
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"volume": r.Volume})
}

// delete removes a detached volume without delete protection and its
// disk; a volume still being created is locked.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		switch {
		case r.Protection.Delete:
			return actions.ErrProtected
		case r.Server != nil:
			return &api.FieldError{Message: fmt.Sprintf("volume %d is attached to server %d; detach it first", id, *r.Server)}
		case r.Status == StatusCreating:
			return actions.ErrLocked
		}
		// An attach records the volume's server only at its success.
		if held, err := busy(tx, id); err != nil || held {
			if err == nil {
				err = actions.ErrLocked
			}
			return err
		}
		return store.Delete(tx, Kind, p.ID, id)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	if err := removeFile(r.Hlab.Path); err != nil {
		api.ServerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// changeProtection sets the delete protection present in the body as an
// action. The change is written with the action; its work has nothing
// left to do.
func (m *Module) changeProtection(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Delete *bool `json:"delete"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "change_protection", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		if body.Delete != nil {
			r.Protection.Delete = *body.Delete
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(context.Context, func(int)) error { return nil })
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// resize grows the volume to the size in the body. The new size is stored
// with the action, so the disk rule counts it at once; a failing resize
// restores the old one.
func (m *Module) resize(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Size *float64 `json:"size"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	size, err := checkSize(body.Size)
	if err != nil {
		api.Fail(w, err)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	grows := func(r Record) error {
		if size <= r.Size {
			return &api.FieldError{Field: "size", Message: fmt.Sprintf("size must be greater than the current %d GB", r.Size)}
		}
		return nil
	}
	var old Record
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		if old, err = store.Get[Record](tx, Kind, p.ID, id); err != nil {
			return err
		}
		return grows(old)
	}); err != nil {
		api.Fail(w, err)
		return
	}
	if err := m.checkDisk(req.Context(), stor, int64(size-old.Size)<<30); err != nil {
		api.Fail(w, err)
		return
	}
	var action *actions.Action
	err = stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if err := grows(r); err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "resize_volume", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		old = r
		r.Size = size
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		err := m.d.Platform.ResizeDisk(ctx, old.Hlab.Path, int64(size)<<30)
		if err == nil {
			return nil
		}
		if serr := stor.Tx(func(tx store.Tx) error {
			r, lerr := load(tx, id)
			if lerr != nil {
				return lerr
			}
			r.Size = old.Size
			return put(tx, r)
		}); serr != nil {
			err = errors.Join(err, serr)
		}
		return err
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// refString returns a JSON string or number as text; anything else as "".
func refString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}
