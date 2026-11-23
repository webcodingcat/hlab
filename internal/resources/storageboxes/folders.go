package storageboxes

import (
	"errors"
	"net/http"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// folders answers GET /storage_boxes/{id}/folders?path= (default "."):
// the direct subdirectories of path in the box, from the storage VM. A box
// that is not active answers 423 locked.
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
	if m.d.Job == nil {
		api.ServerError(w, errors.New("no storage VM"))
		return
	}
	dir := req.URL.Query().Get("path")
	if dir == "" {
		dir = "."
	}
	res, err := m.d.Job(req.Context(), agentapi.StorageJob{Kind: agentapi.StorageJobFolders, Box: userName(r.HLab.N), Path: dir})
	if err != nil {
		api.Fail(w, err)
		return
	}
	if res.Folders == nil {
		res.Folders = []string{}
	}
	api.JSON(w, http.StatusOK, map[string]any{"folders": res.Folders})
}
