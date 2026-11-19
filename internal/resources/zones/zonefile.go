package zones

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
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

// importZonefile runs the import_zonefile action: the zone file replaces
// all RRSets of a primary zone and its $TTL sets the zone's TTL. A bad
// file fails before any action starts.
func (m *Module) importZonefile(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Zonefile *string `json:"zonefile"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	r, action, err := m.write(req, "import_zonefile", func(r *Record) error {
		if r.Mode != ModePrimary {
			return errMode
		}
		if body.Zonefile == nil {
			return &api.FieldError{Field: "zonefile", Message: "zonefile is required"}
		}
		if slices.ContainsFunc(r.RRSets, func(s RRSet) bool { return s.Protection.Change }) {
			return actions.ErrProtected
		}
		return importSets(r, *body.Zonefile, m.d.Now())
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.checkShadow(req, r)
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// checkShadow starts the shadow check of a primary zone outside the
// private-use suffixes.
func (m *Module) checkShadow(req *http.Request, r Record) {
	if m.d.Shadow == nil || r.Mode != ModePrimary || dnszone.PrivateUse(r.Name) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	m.d.Shadow.Check(r.Name, p.Name)
}

// importSets replaces the RRSets of r with those of zone file text and
// sets the zone TTL from its $TTL. The SOA serial continues from the
// zone's current one (0 for a new zone).
func importSets(r *Record, text string, now time.Time) error {
	ttl, sets, err := dnszone.ParseZonefile(r.Name, text)
	if err != nil {
		return fieldErr(err, "")
	}
	if ttl != 0 {
		if checkTTL(ttl) != nil {
			return &api.FieldError{Field: "zonefile", Message: "$TTL must be between 60 and 2147483647"}
		}
		r.TTL = ttl
	}
	old := "0"
	if i := rrsetIndex(r, "@", "SOA"); i >= 0 {
		old = strings.Fields(r.RRSets[i].Records[0].Value)[2]
	}
	r.RRSets = make([]RRSet, len(sets))
	for i, s := range sets {
		r.RRSets[i] = fromData(s)
	}
	i := rrsetIndex(r, "@", "SOA")
	f := strings.Fields(r.RRSets[i].Records[0].Value)
	f[2] = old
	r.RRSets[i].Records[0].Value = strings.Join(f, " ")
	bumpSerial(r, now)
	return nil
}
