package storageboxes

import (
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/storagepass"
	"github.com/webcodingcat/hlab/internal/store"
)

const subActionsPath = subaccountPath + "/actions/"

// subaccountActionRoutes registers the actions of a subaccount.
func (m *Module) subaccountActionRoutes(r resource.Registrar) {
	r.Handle("POST "+subActionsPath+"change_home_directory", http.HandlerFunc(m.changeHomeDirectory))
	r.Handle("POST "+subActionsPath+"reset_subaccount_password", http.HandlerFunc(m.resetSubaccountPassword))
	r.Handle("POST "+subActionsPath+"update_access_settings", http.HandlerFunc(m.updateSubaccountAccess))
}

// subAccessRequest are the access settings of a subaccount request;
// absent flags stay unchanged. HomeDirectory is only read to refuse it.
type subAccessRequest struct {
	ReachableExternally *bool   `json:"reachable_externally"`
	SambaEnabled        *bool   `json:"samba_enabled"`
	SSHEnabled          *bool   `json:"ssh_enabled"`
	WebDAVEnabled       *bool   `json:"webdav_enabled"`
	Readonly            *bool   `json:"readonly"`
	HomeDirectory       *string `json:"home_directory"`
}

// apply sets the flags present in a on s.
func (a subAccessRequest) apply(s *SubaccountAccess) {
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&s.ReachableExternally, a.ReachableExternally)
	set(&s.SambaEnabled, a.SambaEnabled)
	set(&s.SSHEnabled, a.SSHEnabled)
	set(&s.WebDAVEnabled, a.WebDAVEnabled)
	set(&s.Readonly, a.Readonly)
}

func (m *Module) changeHomeDirectory(w http.ResponseWriter, req *http.Request) {
	var body struct {
		HomeDirectory string `json:"home_directory"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if err := checkHome(body.HomeDirectory); err != nil {
		api.Fail(w, err)
		return
	}
	m.subAction(w, req, "change_home_directory", func(tx store.Tx, projectID int64, s SubaccountRecord) error {
		s.HomeDirectory = cleanHome(body.HomeDirectory)
		return putSub(tx, projectID, s)
	})
}

func (m *Module) resetSubaccountPassword(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if err := storagepass.Check(body.Password); err != nil {
		api.Fail(w, &api.FieldError{Field: "password", Message: err.Error()})
		return
	}
	hashes, err := storagepass.Hash(body.Password)
	if err != nil {
		api.ServerError(w, err)
		return
	}
	m.subAction(w, req, "reset_subaccount_password", func(tx store.Tx, projectID int64, s SubaccountRecord) error {
		s.HLab.Hashes = hashes
		return putSub(tx, projectID, s)
	})
}

// updateSubaccountAccess sets the given flags; the home directory changes
// only through change_home_directory.
func (m *Module) updateSubaccountAccess(w http.ResponseWriter, req *http.Request) {
	var body subAccessRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.HomeDirectory != nil {
		api.InvalidInput(w, "home_directory", "home_directory is changed by change_home_directory")
		return
	}
	m.subAction(w, req, "update_subaccount_access_settings", func(tx store.Tx, projectID int64, s SubaccountRecord) error {
		body.apply(&s.AccessSettings)
		return putSub(tx, projectID, s)
	})
}
