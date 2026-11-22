package storageboxes

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/storagepass"
	"github.com/webcodingcat/hlab/internal/store"
)

// subRefType is the action resource type of a subaccount.
const subRefType = "storage_box_subaccount"

const (
	createSubCommand = "create_subaccount"
	deleteSubCommand = "delete_subaccount"
	maxSubName       = 50
	maxSubDesc       = 1000
	maxHomeDirectory = 999
	subaccountsPath  = "/storage_boxes/{id}/subaccounts"
	subaccountPath   = subaccountsPath + "/{subaccount_id}"
)

// homePattern is the pattern of home_directory in the description.
var homePattern = regexp.MustCompile(`^[a-zA-Z0-9 ./_-]+$`)

// errSubLimit reports a box at the subaccounts_limit of its type.
var errSubLimit = errors.New("subaccounts limit reached")

// subaccountRoutes registers the subaccounts of a box and their actions
// under /storage_boxes/{id}/subaccounts.
func (m *Module) subaccountRoutes(r resource.Registrar) {
	r.Handle("GET "+subaccountsPath, http.HandlerFunc(m.listSubaccounts))
	r.Handle("POST "+subaccountsPath, http.HandlerFunc(m.createSubaccount))
	r.Handle("GET "+subaccountPath, http.HandlerFunc(m.getSubaccount))
	r.Handle("PUT "+subaccountPath, http.HandlerFunc(m.updateSubaccount))
	r.Handle("DELETE "+subaccountPath, http.HandlerFunc(m.deleteSubaccount))
	m.subaccountActionRoutes(r)
}

// SubaccountAccess are the access settings of a subaccount.
type SubaccountAccess struct {
	ReachableExternally bool `json:"reachable_externally"`
	SambaEnabled        bool `json:"samba_enabled"`
	SSHEnabled          bool `json:"ssh_enabled"`
	WebDAVEnabled       bool `json:"webdav_enabled"`
	Readonly            bool `json:"readonly"`
}

// Subaccount is a subaccount of a box in Hetzner's API shape.
type Subaccount struct {
	ID             int64             `json:"id"`
	StorageBox     int64             `json:"storage_box"`
	Name           string            `json:"name"`
	HomeDirectory  string            `json:"home_directory"`
	AccessSettings SubaccountAccess  `json:"access_settings"`
	Description    string            `json:"description"`
	Labels         map[string]string `json:"labels"`
	Username       string            `json:"username"`
	Server         string            `json:"server"`
	Created        time.Time         `json:"created"`
}

// SubaccountRecord is a stored subaccount; HLab is never shown.
type SubaccountRecord struct {
	Subaccount
	HLab struct {
		Hashes storagepass.Hashes `json:"hashes"`
	} `json:"_hlab"`
}

func putSub(tx store.Tx, projectID int64, s SubaccountRecord) error {
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: SubaccountKind, ID: s.ID, ProjectID: projectID, Name: s.Name, Labels: s.Labels, Created: s.Created,
	}, s)
}

func subRef(id int64) actions.Ref { return actions.Ref{ID: id, Type: subRefType} }

// boxSubs returns the subaccounts of box boxID of projectID.
func boxSubs(tx store.Tx, projectID, boxID int64) ([]SubaccountRecord, error) {
	all, err := store.List[SubaccountRecord](tx, SubaccountKind, projectID)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(s SubaccountRecord) bool { return s.StorageBox != boxID }), nil
}

// loadSub returns subaccount subID of box boxID of projectID, or
// store.ErrNotFound when either does not exist.
func loadSub(tx store.Tx, projectID, boxID, subID int64) (SubaccountRecord, error) {
	if _, err := Load(tx, projectID, boxID); err != nil {
		return SubaccountRecord{}, err
	}
	s, err := store.Get[SubaccountRecord](tx, SubaccountKind, projectID, subID)
	if err == nil && s.StorageBox != boxID {
		err = store.ErrNotFound
	}
	return s, err
}

// subPathIDs returns the box and subaccount IDs of the path; when one is
// not a positive integer it writes not_found and returns false.
func subPathIDs(w http.ResponseWriter, req *http.Request) (int64, int64, bool) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return 0, 0, false
	}
	subID, err := strconv.ParseInt(req.PathValue("subaccount_id"), 10, 64)
	if err != nil || subID < 1 {
		api.NotFound(w)
		return 0, 0, false
	}
	return boxID, subID, true
}

// checkHome checks a home directory against the description: 1 to 999
// characters of its pattern, relative to the box's root.
func checkHome(h string) error {
	msg := ""
	switch {
	case h == "":
		msg = "home_directory is required"
	case len(h) > maxHomeDirectory:
		msg = fmt.Sprintf("home_directory is longer than %d characters", maxHomeDirectory)
	case strings.HasPrefix(h, "/"):
		msg = "home_directory must not start with a slash"
	case !homePattern.MatchString(h):
		msg = "home_directory may only contain a-z, A-Z, 0-9, space, '.', '/', '_' and '-'"
	}
	if msg != "" {
		return &api.FieldError{Field: "home_directory", Message: msg}
	}
	return nil
}

// checkSubName checks the length of a name and that no other subaccount
// of the box has it.
func checkSubName(subs []SubaccountRecord, name string, exceptID int64) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > maxSubName {
		return &api.FieldError{Field: "name", Message: fmt.Sprintf("name must have 1 to %d characters", maxSubName)}
	}
	for _, s := range subs {
		if s.Name == name && s.ID != exceptID {
			return &api.FieldError{Code: "uniqueness_error", Field: "name", Message: "name is already used in the storage box"}
		}
	}
	return nil
}

func checkSubDescription(d string) error {
	if utf8.RuneCountInString(d) > maxSubDesc {
		return &api.FieldError{Field: "description", Message: fmt.Sprintf("description is longer than %d characters", maxSubDesc)}
	}
	return nil
}

// subLimit returns the subaccounts_limit of the type of box r.
func (m *Module) subLimit(r Record) (int, error) {
	var t struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.StorageBoxType, &t); err != nil {
		return 0, fmt.Errorf("storage box %d: type: %w", r.ID, err)
	}
	st, ok := m.boxType(strconv.FormatInt(t.ID, 10))
	if !ok {
		return 0, fmt.Errorf("storage box %d: type %d not in the catalog", r.ID, t.ID)
	}
	return st.SubaccountsLimit, nil
}

var subListOptions = api.ListOptions[Subaccount]{
	Name:   func(s Subaccount) string { return s.Name },
	Labels: func(s Subaccount) map[string]string { return s.Labels },
	Sort: map[string]func(a, b Subaccount) int{
		"id":      func(a, b Subaccount) int { return cmp.Compare(a.ID, b.ID) },
		"created": func(a, b Subaccount) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Subaccount, []string) bool{
		"username": func(s Subaccount, v []string) bool { return slices.Contains(v, s.Username) },
	},
}

func (m *Module) listSubaccounts(w http.ResponseWriter, req *http.Request) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var recs []SubaccountRecord
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if _, err := Load(tx, p.ID, boxID); err != nil {
			return err
		}
		var err error
		recs, err = boxSubs(tx, p.ID, boxID)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	subs := make([]Subaccount, 0, len(recs))
	for _, s := range recs {
		subs = append(subs, s.Subaccount)
	}
	api.ListAllRequest(w, req, "subaccounts", subs, subListOptions)
}

func (m *Module) getSubaccount(w http.ResponseWriter, req *http.Request) {
	boxID, subID, ok := subPathIDs(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var s SubaccountRecord
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		s, err = loadSub(tx, p.ID, boxID, subID)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"subaccount": s.Subaccount})
}

type subCreateRequest struct {
	HomeDirectory  string            `json:"home_directory"`
	Password       string            `json:"password"`
	AccessSettings *subAccessRequest `json:"access_settings"`
	Name           *string           `json:"name"`
	Description    string            `json:"description"`
	Labels         map[string]string `json:"labels"`
}

// validate checks the fields of a create body that need no store.
func (b subCreateRequest) validate() error {
	if err := checkHome(b.HomeDirectory); err != nil {
		return err
	}
	if err := storagepass.Check(b.Password); err != nil {
		return &api.FieldError{Field: "password", Message: err.Error()}
	}
	if err := checkSubDescription(b.Description); err != nil {
		return err
	}
	return api.CheckLabels(b.Labels)
}

// createSubaccount stores the subaccount with username u<n>-sub<m>, m
// taken from the box's counter and never reused, and runs
// create_subaccount on the box.
func (m *Module) createSubaccount(w http.ResponseWriter, req *http.Request) {
	boxID, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body subCreateRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if err := body.validate(); err != nil {
		api.Fail(w, err)
		return
	}
	hashes, err := storagepass.Hash(body.Password)
	if err != nil {
		api.ServerError(w, err)
		return
	}
	s := SubaccountRecord{Subaccount: Subaccount{
		StorageBox:    boxID,
		HomeDirectory: body.HomeDirectory,
		Description:   body.Description,
		Labels:        body.Labels,
		Created:       time.Now().UTC().Truncate(time.Second),
	}}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.HLab.Hashes = hashes
	if body.AccessSettings != nil {
		body.AccessSettings.apply(&s.AccessSettings)
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var a *actions.Action
	limit := 0
	err = api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		box, err := Load(tx, p.ID, boxID)
		if err != nil {
			return err
		}
		subs, err := boxSubs(tx, p.ID, boxID)
		if err != nil {
			return err
		}
		if limit, err = m.subLimit(box); err != nil {
			return err
		}
		if len(subs) >= limit {
			return errSubLimit
		}
		s.Username = "u" + strconv.FormatInt(box.HLab.N, 10) + "-sub" + strconv.FormatInt(box.HLab.NextSub, 10)
		s.Server = s.Username + "." + serverDomain
		s.Name = s.Username
		if body.Name != nil {
			s.Name = *body.Name
		}
		if err := checkSubName(subs, s.Name, 0); err != nil {
			return err
		}
		if s.ID, err = store.NextID(tx, SubaccountKind); err != nil {
			return err
		}
		if a, err = begin(tx, acts, p.ID, boxID, createSubCommand, subRef(s.ID)); err != nil {
			return err
		}
		box.HLab.NextSub++
		if err := put(tx, p.ID, box); err != nil {
			return err
		}
		return putSub(tx, p.ID, s)
	})
	if errors.Is(err, errSubLimit) {
		api.Error(w, "resource_limit_exceeded",
			fmt.Sprintf("the storage box type allows %d subaccounts", limit),
			map[string]any{"limits": []any{map[string]any{"name": "subaccounts_limit"}}})
		return
	}
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.run(req, a.ID, boxID, func(store.Tx, int64, *Record) error { return nil })
	api.JSON(w, http.StatusCreated, map[string]any{
		"subaccount": map[string]any{"id": s.ID, "storage_box": boxID},
		"action":     a,
	})
}

// updateSubaccount changes the name, description and labels present in
// the body at once; labels, when present, replace the whole set.
func (m *Module) updateSubaccount(w http.ResponseWriter, req *http.Request) {
	boxID, subID, ok := subPathIDs(w, req)
	if !ok {
		return
	}
	var body struct {
		Name        *string            `json:"name"`
		Description *string            `json:"description"`
		Labels      *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var s SubaccountRecord
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if s, err = loadSub(tx, p.ID, boxID, subID); err != nil {
			return err
		}
		if body.Name != nil {
			subs, err := boxSubs(tx, p.ID, boxID)
			if err != nil {
				return err
			}
			if err := checkSubName(subs, *body.Name, subID); err != nil {
				return err
			}
			s.Name = *body.Name
		}
		if body.Description != nil {
			if err := checkSubDescription(*body.Description); err != nil {
				return err
			}
			s.Description = *body.Description
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			s.Labels = *body.Labels
		}
		return putSub(tx, p.ID, s)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"subaccount": s.Subaccount})
}

// deleteSubaccount runs delete_subaccount, which removes the subaccount.
func (m *Module) deleteSubaccount(w http.ResponseWriter, req *http.Request) {
	m.subAction(w, req, deleteSubCommand, func(tx store.Tx, projectID int64, s SubaccountRecord) error {
		return store.Delete(tx, SubaccountKind, projectID, s.ID)
	})
}

// subAction answers a request that runs command as an action on the
// subaccount of the path, referencing box and subaccount and locking the
// box; done stores the change when the action succeeds. Callers validate
// the request first. It answers 201 with the action.
func (m *Module) subAction(w http.ResponseWriter, req *http.Request, command string,
	done func(tx store.Tx, projectID int64, s SubaccountRecord) error,
) {
	boxID, subID, ok := subPathIDs(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var a *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if _, err := loadSub(tx, p.ID, boxID, subID); err != nil {
			return err
		}
		var err error
		a, err = begin(tx, acts, p.ID, boxID, command, subRef(subID))
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.run(req, a.ID, boxID, func(tx store.Tx, projectID int64, _ *Record) error {
		s, err := store.Get[SubaccountRecord](tx, SubaccountKind, projectID, subID)
		if err != nil {
			return err
		}
		return done(tx, projectID, s)
	})
	api.JSON(w, http.StatusCreated, map[string]any{"action": a})
}
