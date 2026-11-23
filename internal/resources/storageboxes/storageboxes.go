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
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/scheduler"
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

// StorageMAC is the MAC of the storage VM's adapter on the public PVLAN.
const StorageMAC = "02:48:4c:00:00:03"

// FreeSpaceReserve is the free space of the host drive of the data folder
// below which create and a change_type to a larger type answer 412
// resource_unavailable.
const FreeSpaceReserve = 10 << 30

// StatsEvery is the interval of the job refreshing the stats of the boxes.
const StatsEvery = 5 * time.Minute

// Deps are the dependencies of the module.
type Deps struct {
	Catalog *catalog.Catalog
	// PublicRange is the range box IPs are handed out of.
	PublicRange netip.Prefix
	// Apply makes the storage VM match the store (all boxes without the
	// deleting mark); it runs in the work function of every box and
	// subaccount action and returns only after the storage agent
	// confirmed. nil does nothing.
	Apply func(ctx context.Context, boxID int64) error
	// Job runs a job on the storage VM.
	Job func(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error)
	// FreeBytes returns the free bytes of the host drive of the data folder.
	FreeBytes func() (uint64, error)
	// Clock drives the stats job; nil: scheduler.RealClock().
	Clock scheduler.Clock
	// PushAgent pushes the appliance's document again after the failed
	// path of an action changed the store (a box deleted after a failed
	// create, the deleting mark cleared). nil does nothing.
	PushAgent func(ctx context.Context) error
	// Store is what Recover works on outside a request.
	Store *store.Store
}

// Module is the storage_boxes resource type.
type Module struct {
	d    Deps
	acts *actions.Manager // set at mount (UseActions); the stats job reads the store through it
}

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
	m.snapshotRoutes(r)
	r.Handle("GET /storage_boxes/{id}/folders", http.HandlerFunc(m.folders))
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
	// N is the number of the username u<n>, assigned at create.
	N       int64              `json:"n,omitempty"`
	Hashes  storagepass.Hashes `json:"hashes"`
	NextSub int64              `json:"next_sub"`
	SSHKeys []string           `json:"ssh_keys,omitempty"`
	// IP is the box's address from the public range, assigned at create.
	IP string `json:"ip,omitempty"`
	// Deleting marks a box whose delete_storage_box runs; the storage VM's
	// document leaves it out.
	Deleting bool `json:"deleting,omitempty"`
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

// errNotActive refuses subaccount create and every action but delete on a
// box that is not active.
var errNotActive = &actions.Error{Code: "locked", Message: "the storage box is not active"}

// checkActive returns errNotActive unless box r is active.
func checkActive(r Record) error {
	if r.Status != StatusActive {
		return errNotActive
	}
	return nil
}

// removeBox deletes stored box r of projectID with its subaccounts and
// snapshots and releases its IP.
func removeBox(tx store.Tx, projectID int64, r Record) error {
	subs, err := boxSubs(tx, projectID, r.ID)
	if err != nil {
		return err
	}
	for _, s := range subs {
		if err := store.Delete(tx, SubaccountKind, projectID, s.ID); err != nil {
			return err
		}
	}
	snaps, err := boxSnaps(tx, projectID, r.ID)
	if err != nil {
		return err
	}
	for _, s := range snaps {
		if err := store.Delete(tx, SnapshotKind, projectID, s.ID); err != nil {
			return err
		}
	}
	if a, err := netip.ParseAddr(r.HLab.IP); err == nil {
		if err := ipam.Release(tx, a); err != nil {
			return err
		}
	}
	return store.Delete(tx, Kind, projectID, r.ID)
}

// begin starts command on box boxID of projectID in tx. The action
// references the box and extra; it answers actions.ErrLocked while any
// action of the box runs, so every action on one box locks it.
func begin(tx store.Tx, acts *actions.Manager, projectID, boxID int64, command string, extra ...actions.Ref) (*actions.Action, error) {
	refs := append([]actions.Ref{boxRef(boxID)}, extra...)
	return acts.Start(tx, projectID, command, refs)
}

// boxFunc changes stored box r of projectID in tx and stores what it
// changes itself.
type boxFunc func(tx store.Tx, projectID int64, r *Record) error

// run hands the work of action actionID on box boxID to the action
// manager. Each step works on the stored box of projectID in a
// transaction of its own: change (if not nil) stores the action's change,
// so Apply pushes documents that already hold it; then Apply; then done
// (if not nil) and a refresh of the box's stats. When Apply fails, failed
// (if not nil) runs instead of done and undoes what change stored, the
// appliance's document is pushed again, and the action fails with Apply's
// error.
func (m *Module) run(req *http.Request, actionID, boxID int64, change, done, failed boxFunc) {
	p, _ := api.ProjectFrom(req.Context())
	st := api.StoreFrom(req.Context())
	onBox := func(fn boxFunc) error {
		if fn == nil {
			return nil
		}
		return st.Tx(func(tx store.Tx) error {
			r, err := Load(tx, p.ID, boxID)
			if err != nil {
				return err
			}
			return fn(tx, p.ID, &r)
		})
	}
	api.ActionsFrom(req.Context()).Run(actionID, func(ctx context.Context, _ func(int)) error {
		if err := onBox(change); err != nil {
			return err
		}
		if m.d.Apply != nil {
			if err := m.d.Apply(ctx, boxID); err != nil {
				if failed != nil {
					if ferr := onBox(failed); ferr != nil {
						return errors.Join(err, ferr)
					}
					if m.d.PushAgent != nil {
						if perr := m.d.PushAgent(ctx); perr != nil {
							return errors.Join(err, perr)
						}
					}
				}
				return err
			}
		}
		if err := onBox(done); err != nil {
			return err
		}
		m.refreshStats(ctx, st, p.ID, boxID)
		return nil
	})
}

// boxAction answers a request that runs command as an action on the box
// of the path: a box that is not active and check refuse on the stored
// box before the action starts, change stores the action's change before
// Apply, and a failed Apply restores the box as it was (see run). It
// answers 201 with the action.
func (m *Module) boxAction(w http.ResponseWriter, req *http.Request, command string,
	check func(r Record) error, change boxFunc,
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
		if err := checkActive(r); err != nil {
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
	var old Record
	m.run(req, a.ID, id, func(tx store.Tx, projectID int64, r *Record) error {
		old = *r
		return change(tx, projectID, r)
	}, nil, func(tx store.Tx, projectID int64, _ *Record) error {
		return put(tx, projectID, old)
	})
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
