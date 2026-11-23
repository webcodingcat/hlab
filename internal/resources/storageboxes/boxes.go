package storageboxes

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/storagepass"
	"github.com/webcodingcat/hlab/internal/store"
)

// nextUserKey is the kv key of the next username number; usernames start
// at u<firstUser> and are never reused.
const (
	nextUserKey   = "storage_boxes/next_user"
	firstUser     = 100000
	serverDomain  = "storagebox.hetzner.internal"
	createCommand = "create_storage_box"
	deleteCommand = "delete_storage_box"
)

func (m *Module) boxRoutes(r resource.Registrar) {
	r.Handle("GET /storage_boxes", http.HandlerFunc(m.list))
	r.Handle("POST /storage_boxes", http.HandlerFunc(m.create))
	r.Handle("GET /storage_boxes/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /storage_boxes/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /storage_boxes/{id}", http.HandlerFunc(m.delete))
}

// checkName refuses an empty name and one another box of the project has.
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

var listOptions = api.ListOptions[Box]{
	Name:   func(b Box) string { return b.Name },
	Labels: func(b Box) map[string]string { return b.Labels },
	Sort: map[string]func(a, b Box) int{
		"id":         func(a, b Box) int { return cmp.Compare(a.ID, b.ID) },
		"name":       func(a, b Box) int { return cmp.Compare(a.Name, b.Name) },
		"created":    func(a, b Box) int { return a.Created.Compare(b.Created) },
		"stats.size": func(a, b Box) int { return cmp.Compare(a.Stats.Size, b.Stats.Size) },
		// The description offers it although stats has no such field;
		// the box's size is the closest figure.
		"stats.size_filesystem": func(a, b Box) int { return cmp.Compare(a.Stats.Size, b.Stats.Size) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var recs []Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		recs, err = store.List[Record](tx, Kind, p.ID)
		return err
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	boxes := make([]Box, 0, len(recs))
	for _, r := range recs {
		boxes = append(boxes, r.Box)
	}
	api.ListRequest(w, req, Kind, boxes, listOptions)
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
		r, err = Load(tx, p.ID, id)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"storage_box": r.Box})
}

type accessRequest struct {
	ReachableExternally *bool `json:"reachable_externally"`
	SambaEnabled        *bool `json:"samba_enabled"`
	SSHEnabled          *bool `json:"ssh_enabled"`
	WebDAVEnabled       *bool `json:"webdav_enabled"`
	ZFSEnabled          *bool `json:"zfs_enabled"`
}

// apply sets the flags present in a on s.
func (a accessRequest) apply(s *AccessSettings) {
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&s.ReachableExternally, a.ReachableExternally)
	set(&s.SambaEnabled, a.SambaEnabled)
	set(&s.SSHEnabled, a.SSHEnabled)
	set(&s.WebDAVEnabled, a.WebDAVEnabled)
	set(&s.ZFSEnabled, a.ZFSEnabled)
}

type createRequest struct {
	Name           string            `json:"name"`
	Location       string            `json:"location"`
	StorageBoxType string            `json:"storage_box_type"`
	Password       string            `json:"password"`
	Labels         map[string]string `json:"labels"`
	SSHKeys        []string          `json:"ssh_keys"`
	AccessSettings *accessRequest    `json:"access_settings"`
}

// boxType returns the catalog or own type whose name or ID is ref.
func (m *Module) boxType(ref string) (catalog.StorageBoxType, bool) {
	return m.d.Catalog.StorageBoxTypeByRef(ref)
}

// location returns the catalog location whose name or ID is ref.
func (m *Module) location(ref string) (catalog.Location, bool) {
	for _, l := range m.d.Catalog.Locations {
		if l.Name == ref || strconv.FormatInt(l.ID, 10) == ref {
			return l, true
		}
	}
	return catalog.Location{}, false
}

// validate checks the fields of a create body that need no store.
func (m *Module) validate(body createRequest) (catalog.StorageBoxType, catalog.Location, error) {
	st, ok := m.boxType(body.StorageBoxType)
	if !ok {
		return st, catalog.Location{}, &api.FieldError{Field: "storage_box_type", Message: "unknown storage box type"}
	}
	loc, ok := m.location(body.Location)
	if !ok {
		return st, loc, &api.FieldError{Field: "location", Message: "unknown location"}
	}
	if !slices.Contains(st.Locations, loc.Name) {
		return st, loc, &api.FieldError{Field: "location",
			Message: fmt.Sprintf("storage box type %s is not available in %s", st.Name, loc.Name)}
	}
	if err := storagepass.Check(body.Password); err != nil {
		return st, loc, &api.FieldError{Field: "password", Message: err.Error()}
	}
	for _, k := range body.SSHKeys {
		if _, err := sshkeys.Fingerprint(k); err != nil {
			return st, loc, &api.FieldError{Field: "ssh_keys", Message: "ssh_keys must be public keys in OpenSSH format"}
		}
	}
	return st, loc, api.CheckLabels(body.Labels)
}

// create stores the box as initializing with its number and IP and starts
// create_storage_box, which shows its username, server and system and
// makes it active.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	st, loc, err := m.validate(body)
	if err != nil {
		api.Fail(w, err)
		return
	}
	hashes, err := storagepass.Hash(body.Password)
	if err != nil {
		api.ServerError(w, err)
		return
	}
	r := Record{
		Box: Box{
			Name:           body.Name,
			StorageBoxType: st.Raw,
			Location:       loc.Raw,
			Labels:         body.Labels,
			Status:         StatusInitializing,
			Created:        time.Now().UTC().Truncate(time.Second),
		},
		HLab: HLab{Hashes: hashes, NextSub: 1, SSHKeys: body.SSHKeys},
	}
	if body.AccessSettings != nil {
		body.AccessSettings.apply(&r.AccessSettings)
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var a *actions.Action
	err = api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, r.Name, 0); err != nil {
			return err
		}
		var err error
		if r.ID, err = store.NextID(tx, Kind); err != nil {
			return err
		}
		if r.HLab.N, err = nextUser(tx); err != nil {
			return err
		}
		ip, err := ipam.NextPublic(tx, m.d.PublicRange)
		if err != nil {
			return err
		}
		r.HLab.IP = ip.String()
		if err := put(tx, p.ID, r); err != nil {
			return err
		}
		a, err = begin(tx, acts, p.ID, r.ID, createCommand)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	system := strings.ToUpper(loc.Name) + "-BX" + strconv.FormatInt(r.ID, 10)
	m.run(req, a.ID, r.ID, func(tx store.Tx, projectID int64, b *Record) error {
		username := userName(b.HLab.N)
		server := username + "." + serverDomain
		b.Username, b.Server, b.System = &username, &server, &system
		b.Status = StatusActive
		return put(tx, projectID, *b)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"storage_box": r.Box, "action": a})
}

// userName is the username of box number n.
func userName(n int64) string { return "u" + strconv.FormatInt(n, 10) }

// nextUser takes the next username number from the kv counter.
func nextUser(tx store.Tx) (int64, error) {
	n := int64(firstUser)
	v, err := store.GetKV(tx, nextUserKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return 0, err
	default:
		if n, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, fmt.Errorf("kv %s: %w", nextUserKey, err)
		}
	}
	return n, store.SetKV(tx, nextUserKey, strconv.FormatInt(n+1, 10))
}

// update changes the name and labels present in the body at once; labels,
// when present, replace the whole set.
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
		if r, err = Load(tx, p.ID, id); err != nil {
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
		return put(tx, p.ID, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"storage_box": r.Box})
}

// delete starts delete_storage_box, which removes the box and its
// subaccounts; a box with delete protection answers 423 protected.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	m.boxAction(w, req, deleteCommand, func(r Record) error {
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		return nil
	}, func(tx store.Tx, projectID int64, r *Record) error {
		subs, err := store.List[struct {
			ID         int64 `json:"id"`
			StorageBox int64 `json:"storage_box"`
		}](tx, SubaccountKind, projectID)
		if err != nil {
			return err
		}
		for _, s := range subs {
			if s.StorageBox == r.ID {
				if err := store.Delete(tx, SubaccountKind, projectID, s.ID); err != nil {
					return err
				}
			}
		}
		if a, err := netip.ParseAddr(r.HLab.IP); err == nil {
			if err := ipam.Release(tx, a); err != nil {
				return err
			}
		}
		return store.Delete(tx, Kind, projectID, r.ID)
	})
}
