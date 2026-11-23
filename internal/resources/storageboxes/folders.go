package storageboxes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/agentclient"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// errFolderPath refuses an absolute folders path or one with "..".
var errFolderPath = &api.FieldError{Field: "path", Message: "path must be relative and must not contain .."}

// checkFolderPath refuses an absolute path or one with a ".." segment
// before the storage VM is asked.
func checkFolderPath(p string) error {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return errFolderPath
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return errFolderPath
		}
	}
	return nil
}

// folders answers GET /storage_boxes/{id}/folders?path= (default "."):
// the direct subdirectories of path in the box, from the storage VM. A box
// that is not active answers 423 locked, an absolute or escaping path 422
// invalid_input on path, a missing one 404 not_found.
func (m *Module) folders(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, id); err != nil {
			return err
		}
		return checkActive(r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	dir := req.URL.Query().Get("path")
	if dir == "" {
		dir = "."
	}
	if err := checkFolderPath(dir); err != nil {
		api.Fail(w, err)
		return
	}
	if m.d.Job == nil {
		api.ServerError(w, errors.New("no storage VM"))
		return
	}
	res, err := m.d.Job(req.Context(), agentapi.StorageJob{Kind: agentapi.StorageJobFolders, Box: userName(r.HLab.N), Path: dir})
	var je *agentclient.JobError
	switch {
	case errors.As(err, &je) && je.Code == agentapi.JobErrNotFound:
		api.NotFound(w)
		return
	case errors.As(err, &je) && je.Code == agentapi.JobErrInvalidPath:
		api.Fail(w, errFolderPath)
		return
	case err != nil:
		api.ServerError(w, err)
		return
	}
	if res.Folders == nil {
		res.Folders = []string{}
	}
	api.JSON(w, http.StatusOK, map[string]any{"folders": res.Folders})
}
