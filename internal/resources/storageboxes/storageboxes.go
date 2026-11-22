// Package storageboxes serves Hetzner's Storage Boxes, their actions and
// their subaccounts under the Hetzner API prefix. Boxes and subaccounts are
// documents of the resources table; the plain passwords are never kept,
// only a sha512-crypt and an NT hash.
package storageboxes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/storagepass"
	"github.com/webcodingcat/hlab/internal/store"
)

// Store kinds and action resource types.
const (
	Kind           = "storage_boxes"
	SubaccountKind = "storage_box_subaccounts"
	refType        = "storage_box"
)

// Box statuses.
const (
	StatusInitializing = "initializing"
	StatusActive       = "active"
)

// Deps are the dependencies of the module.
type Deps struct {
	Catalog *catalog.Catalog
	// Apply makes the storage VM match box boxID; it runs in the work
	// function of every box and subaccount action. nil does nothing.
	Apply func(ctx context.Context, boxID int64) error
}

// Module is the storage_boxes resource type.
type Module struct{ d Deps }

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Prefix implements resource.Prefixed.
func (m *Module) Prefix() string { return api.HetznerPrefix }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	m.boxRoutes(r)
	m.actionRoutes(r)
	m.subaccountRoutes(r)
	// GET /storage_boxes/actions/{id} conflicts with
	// /storage_boxes/{id}/actions in http.ServeMux; the less specific
	// pattern takes it.
	r.Handle("GET /storage_boxes/{id}/{sub}", http.HandlerFunc(m.boxActionByID))
	resactions.RoutesFor(r, Kind, refType)
}

// AccessSettings are the access settings of a box.
type AccessSettings struct {
	ReachableExternally bool `json:"reachable_externally"`
	SambaEnabled        bool `json:"samba_enabled"`
	SSHEnabled          bool `json:"ssh_enabled"`
	WebDAVEnabled       bool `json:"webdav_enabled"`
	ZFSEnabled          bool `json:"zfs_enabled"`
}

// SnapshotPlan is the snapshot plan of a box.
type SnapshotPlan struct {
	MaxSnapshots int  `json:"max_snapshots"`
	Minute       int  `json:"minute"`
	Hour         int  `json:"hour"`
	DayOfWeek    *int `json:"day_of_week"`
	DayOfMonth   *int `json:"day_of_month"`
}

// Protection is the protection of a box.
type Protection struct {
	Delete bool `json:"delete"`
}

// Stats are the disk usage figures of a box.
type Stats struct {
	Size          int64 `json:"size"`
	SizeData      int64 `json:"size_data"`
	SizeSnapshots int64 `json:"size_snapshots"`
}

// Box is a storage box in Hetzner's API shape. StorageBoxType and Location
// are the catalog objects.
type Box struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	StorageBoxType json.RawMessage   `json:"storage_box_type"`
	Location       json.RawMessage   `json:"location"`
	AccessSettings AccessSettings    `json:"access_settings"`
	SnapshotPlan   *SnapshotPlan     `json:"snapshot_plan"`
	Protection     Protection        `json:"protection"`
	Labels         map[string]string `json:"labels"`
	Status         string            `json:"status"`
	Username       *string           `json:"username"`
	Server         *string           `json:"server"`
	System         *string           `json:"system"`
	Stats          Stats             `json:"stats"`
	Created        time.Time         `json:"created"`
}

// HLab is the part of a stored box that the API never shows.
type HLab struct {
	// N is the number of the username u<n>; 0 until create_storage_box
	// succeeds.
	N       int64              `json:"n,omitempty"`
	Hashes  storagepass.Hashes `json:"hashes"`
	NextSub int64              `json:"next_sub"`
	SSHKeys []string           `json:"ssh_keys,omitempty"`
}

// Record is a stored box.
type Record struct {
	Box
	HLab HLab `json:"_hlab"`
}

// Load returns box id of projectID, or store.ErrNotFound.
func Load(tx store.Tx, projectID, id int64) (Record, error) {
	return store.Get[Record](tx, Kind, projectID, id)
}

func put(tx store.Tx, projectID int64, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: projectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

func boxRef(id int64) actions.Ref { return actions.Ref{ID: id, Type: refType} }

// begin starts command on box boxID of projectID in tx. The action
// references the box and extra; it answers actions.ErrLocked while any
// action of the box runs, so every action on one box locks it.
func begin(tx store.Tx, acts *actions.Manager, projectID, boxID int64, command string, extra ...actions.Ref) (*actions.Action, error) {
	refs := append([]actions.Ref{boxRef(boxID)}, extra...)
	return acts.Start(tx, projectID, command, refs)
}

// run hands the work of action actionID on box boxID to the action
// manager: Apply, then done on the stored box of projectID in one
// transaction. done stores what it changes itself.
func (m *Module) run(req *http.Request, actionID, boxID int64, done func(tx store.Tx, projectID int64, r *Record) error) {
	p, _ := api.ProjectFrom(req.Context())
	st := api.StoreFrom(req.Context())
	api.ActionsFrom(req.Context()).Run(actionID, func(ctx context.Context, _ func(int)) error {
		if m.d.Apply != nil {
			if err := m.d.Apply(ctx, boxID); err != nil {
				return err
			}
		}
		return st.Tx(func(tx store.Tx) error {
			r, err := Load(tx, p.ID, boxID)
			if err != nil {
				return err
			}
			return done(tx, p.ID, &r)
		})
	})
}

// boxAction answers a request that runs command as an action on the box
// of the path: check refuses on the stored box before the action starts,
// done changes it when the action succeeds (see run). It answers 201 with
// the action.
func (m *Module) boxAction(w http.ResponseWriter, req *http.Request, command string,
	check func(r Record) error, done func(tx store.Tx, projectID int64, r *Record) error,
) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var a *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(r); err != nil {
				return err
			}
		}
		a, err = begin(tx, acts, p.ID, id, command)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.run(req, a.ID, id, done)
	api.JSON(w, http.StatusCreated, map[string]any{"action": a})
}

// boxActionByID answers GET /storage_boxes/actions/{id}: an action of a
// box of the project. Other paths of two segments below /storage_boxes
// are not found.
func (m *Module) boxActionByID(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(req.PathValue("sub"), 10, 64)
	if req.PathValue("id") != "actions" || err != nil {
		api.NotFound(w)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	a, err := api.ActionsFrom(req.Context()).Get(p.ID, id)
	if errors.Is(err, store.ErrNotFound) || err == nil && !slices.ContainsFunc(a.Resources, func(r actions.Ref) bool { return r.Type == refType }) {
		api.NotFound(w)
		return
	}
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"action": a})
}
