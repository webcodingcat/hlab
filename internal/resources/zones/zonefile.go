package zones

import (
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/dnszone"
	"github.com/webcodingcat/hlab/internal/store"
)

// exportZonefile answers GET /zones/{id}/zonefile with the zone file of a
// primary zone; a secondary zone answers incorrect_zone_mode.
func (m *Module) exportZonefile(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, req.PathValue("id")); err != nil {
			return err
		}
		if r.Mode != ModePrimary {
			return errMode
		}
		return nil
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"zonefile": dnszone.FormatZonefile(r.Name, r.TTL, r.Sets())})
}
