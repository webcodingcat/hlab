package zones

import (
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/store"
)

// RRSetView is an RRSet as the API shows it.
type RRSetView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	TTL        *int              `json:"ttl"`
	Labels     map[string]string `json:"labels"`
	Protection RRSetProtection   `json:"protection"`
	Records    []RRSetRecord     `json:"records"`
	Zone       int64             `json:"zone"`
}

// rrsetView is RRSet s of zone as the API shows it.
func rrsetView(zone int64, s RRSet) RRSetView {
	v := RRSetView{
		ID: s.Name + "/" + s.Type, Name: s.Name, Type: s.Type, TTL: s.TTL,
		Labels: s.Labels, Protection: s.Protection, Records: s.Records, Zone: zone,
	}
	if v.Labels == nil {
		v.Labels = map[string]string{}
	}
	if v.Records == nil {
		v.Records = []RRSetRecord{}
	}
	return v
}

// getRRSet answers GET /zones/{id}/rrsets/{rr_name}/{rr_type}.
func (m *Module) getRRSet(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var v RRSetView
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, req.PathValue("id"))
		if err != nil {
			return err
		}
		name, typ := req.PathValue("rr_name"), req.PathValue("rr_type")
		i := slices.IndexFunc(r.RRSets, func(s RRSet) bool { return s.Name == name && s.Type == typ })
		if i < 0 {
			return store.ErrNotFound
		}
		v = rrsetView(r.ID, r.RRSets[i])
		return nil
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"rrset": v})
}

// createRRSet answers POST /zones/{id}/rrsets. A secondary zone answers
// incorrect_zone_mode; creating RRSets of a primary zone is not built yet.
func (m *Module) createRRSet(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, req.PathValue("id"))
		if err != nil {
			return err
		}
		if r.Mode != ModePrimary {
			return errMode
		}
		return &api.FieldError{Code: "unsupported_error", Message: "creating RRSets is not available yet"}
	})
	api.Fail(w, err)
}
