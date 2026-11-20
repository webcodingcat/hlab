package zones

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/dnszone"
	"github.com/webcodingcat/hlab/internal/resource"
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

// errSOA refuses a change of the apex SOA records, which hlab manages.
var errSOA = &api.FieldError{Field: "type", Message: "the SOA RRSet is managed by HLab"}

// rrsetRoutes registers the RRSet routes below /zones/{id}.
func (m *Module) rrsetRoutes(r resource.Registrar) {
	const set = "/zones/{id}/rrsets/{rr_name}/{rr_type}"
	r.Handle("GET /zones/{id}/rrsets", http.HandlerFunc(m.listRRSets))
	r.Handle("POST /zones/{id}/rrsets", http.HandlerFunc(m.createRRSet))
	r.Handle("GET "+set, http.HandlerFunc(m.getRRSet))
	r.Handle("PUT "+set, http.HandlerFunc(m.updateRRSet))
	r.Handle("DELETE "+set, http.HandlerFunc(m.deleteRRSet))
	r.Handle("POST "+set+"/actions/change_protection", http.HandlerFunc(m.changeRRSetProtection))
	r.Handle("POST "+set+"/actions/change_ttl", http.HandlerFunc(m.changeRRSetTTL))
	r.Handle("POST "+set+"/actions/set_records", http.HandlerFunc(m.setRecords))
	r.Handle("POST "+set+"/actions/add_records", http.HandlerFunc(m.addRecords))
	r.Handle("POST "+set+"/actions/remove_records", http.HandlerFunc(m.removeRecords))
	r.Handle("POST "+set+"/actions/update_records", http.HandlerFunc(m.updateRecords))
}

// rrsetIndex returns the index of RRSet name/typ in r, or -1.
func rrsetIndex(r *Record, name, typ string) int {
	return slices.IndexFunc(r.RRSets, func(s RRSet) bool { return s.Name == name && s.Type == typ })
}

// pathRRSet returns the index of the RRSet of the path in r: a secondary
// zone answers incorrect_zone_mode, a missing RRSet not_found.
func pathRRSet(r *Record, req *http.Request) (int, error) {
	if r.Mode != ModePrimary {
		return -1, errMode
	}
	i := rrsetIndex(r, req.PathValue("rr_name"), req.PathValue("rr_type"))
	if i < 0 {
		return -1, store.ErrNotFound
	}
	return i, nil
}

// commit validates RRSet i of r and all RRSets of r together, then raises
// the SOA serial. An RRSet left without records is removed.
func (m *Module) commit(r *Record, i int) error {
	if len(r.RRSets[i].Records) == 0 {
		r.RRSets = slices.Delete(r.RRSets, i, i+1)
	} else if err := dnszone.CheckRRSet(r.Name, r.RRSets[i].Data()); err != nil {
		return fieldErr(err, "")
	}
	if err := dnszone.CheckConflicts(r.Sets()); err != nil {
		return fieldErr(err, "")
	}
	bumpSerial(r, m.d.Now())
	return nil
}

// listedRRSet is an RRSet in a list with its position in the zone, which
// is the order of creation.
type listedRRSet struct {
	RRSetView
	pos int
}

var rrsetListOptions = api.ListOptions[listedRRSet]{
	Name:   func(s listedRRSet) string { return s.Name },
	Labels: func(s listedRRSet) map[string]string { return s.Labels },
	Sort: map[string]func(a, b listedRRSet) int{
		"id":      func(a, b listedRRSet) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b listedRRSet) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b listedRRSet) int { return cmp.Compare(a.pos, b.pos) },
	},
	Filters: map[string]func(listedRRSet, []string) bool{
		"type": func(s listedRRSet, v []string) bool { return slices.Contains(v, s.Type) },
	},
}

// listRRSets answers GET /zones/{id}/rrsets.
func (m *Module) listRRSets(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []listedRRSet
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, req.PathValue("id"))
		if err != nil {
			return err
		}
		if r.Mode != ModePrimary {
			return errMode
		}
		out = make([]listedRRSet, len(r.RRSets))
		for i, s := range r.RRSets {
			out[i] = listedRRSet{RRSetView: rrsetView(r.ID, s), pos: i}
		}
		return nil
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.ListRequest(w, req, "rrsets", out, rrsetListOptions)
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
		i, err := pathRRSet(&r, req)
		if err != nil {
			return err
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

// updateRRSet changes the labels of an RRSet; it answers at once without
// action.
func (m *Module) updateRRSet(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Labels *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var v RRSetView
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, req.PathValue("id"))
		if err != nil {
			return err
		}
		i, err := pathRRSet(&r, req)
		if err != nil {
			return err
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			r.RRSets[i].Labels = *body.Labels
		}
		v = rrsetView(r.ID, r.RRSets[i])
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"rrset": v})
}

// createRRSet answers POST /zones/{id}/rrsets with the new RRSet and its
// create_rrset action; an existing RRSet answers uniqueness_error.
func (m *Module) createRRSet(w http.ResponseWriter, req *http.Request) {
	var body rrsetRequest
	if !api.Decode(w, req, &body) {
		return
	}
	s := RRSet{Name: body.Name, Type: body.Type, TTL: body.TTL, Labels: body.Labels, Records: body.Records}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	r, action, err := m.write(req, "create_rrset", func(r *Record) error {
		if r.Mode != ModePrimary {
			return errMode
		}
		if err := dnszone.CheckRRSet(r.Name, s.Data()); err != nil {
			return fieldErr(err, "")
		}
		if s.Type == "SOA" {
			return errSOA
		}
		if err := api.CheckLabels(s.Labels); err != nil {
			return err
		}
		if rrsetIndex(r, s.Name, s.Type) >= 0 {
			return &api.FieldError{Code: "uniqueness_error", Field: "name", Message: "RRSet " + s.Name + "/" + s.Type + " already exists"}
		}
		r.RRSets = append(r.RRSets, s)
		return m.commit(r, len(r.RRSets)-1)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusCreated, map[string]any{"rrset": rrsetView(r.ID, s), "action": action})
}

// rrsetChange is how an RRSet action treats protection and the apex SOA.
type rrsetChange struct {
	command string
	status  int
	// free: allowed on a change-protected RRSet.
	free bool
	// soa: allowed on the apex SOA.
	soa bool
	// data: changes DNS data, so the result is validated and the SOA
	// serial raised.
	data bool
}

// changeRRSet runs action c on the RRSet of the path and answers
// c.status {action}; f changes the RRSet.
func (m *Module) changeRRSet(w http.ResponseWriter, req *http.Request, c rrsetChange, f func(s *RRSet) error) {
	m.change(w, req, c.command, c.status, func(r *Record) error {
		i, err := pathRRSet(r, req)
		if err != nil {
			return err
		}
		s := &r.RRSets[i]
		if !c.free && s.Protection.Change {
			return actions.ErrProtected
		}
		if !c.soa && s.Name == "@" && s.Type == "SOA" {
			return errSOA
		}
		if err := f(s); err != nil {
			return err
		}
		if !c.data {
			return nil
		}
		return m.commit(r, i)
	})
}

// deleteRRSet removes an RRSet through its delete_rrset action.
func (m *Module) deleteRRSet(w http.ResponseWriter, req *http.Request) {
	m.changeRRSet(w, req, rrsetChange{command: "delete_rrset", status: http.StatusCreated, data: true}, func(s *RRSet) error {
		s.Records = nil
		return nil
	})
}

func (m *Module) changeRRSetProtection(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Change *bool `json:"change"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	c := rrsetChange{command: "change_rrset_protection", status: http.StatusCreated, free: true, soa: true}
	m.changeRRSet(w, req, c, func(s *RRSet) error {
		if body.Change != nil {
			s.Protection.Change = *body.Change
		}
		return nil
	})
}

// changeRRSetTTL sets the TTL of an RRSet; null means the zone's TTL.
func (m *Module) changeRRSetTTL(w http.ResponseWriter, req *http.Request) {
	var body struct {
		TTL *int `json:"ttl"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	c := rrsetChange{command: "change_rrset_ttl", status: http.StatusCreated, soa: true, data: true}
	m.changeRRSet(w, req, c, func(s *RRSet) error {
		s.TTL = body.TTL
		return nil
	})
}

// recordsBody is the body of the record actions.
type recordsBody struct {
	TTL     *int          `json:"ttl"`
	Records []RRSetRecord `json:"records"`
}

// decodeRecords decodes a records body and refuses an empty list.
func decodeRecords(w http.ResponseWriter, req *http.Request) (recordsBody, bool) {
	var body recordsBody
	if !api.Decode(w, req, &body) {
		return body, false
	}
	if len(body.Records) == 0 {
		api.Fail(w, &api.FieldError{Field: "records", Message: "records must not be empty"})
		return body, false
	}
	return body, true
}

// recordIndex returns the index of the record with value v in s or an
// invalid_input error on records[k].value.
func recordIndex(s *RRSet, k int, v string) (int, error) {
	j := slices.IndexFunc(s.Records, func(o RRSetRecord) bool { return o.Value == v })
	if j < 0 {
		return -1, &api.FieldError{Field: fmt.Sprintf("records[%d].value", k), Message: "record is not in the RRSet"}
	}
	return j, nil
}

// setRecords replaces the records of an RRSet.
func (m *Module) setRecords(w http.ResponseWriter, req *http.Request) {
	body, ok := decodeRecords(w, req)
	if !ok {
		return
	}
	m.changeRRSet(w, req, rrsetChange{command: "set_rrset_records", status: http.StatusCreated, data: true}, func(s *RRSet) error {
		s.Records = body.Records
		return nil
	})
}

// addRecords adds records to an RRSet and creates it if it is missing. A
// ttl must match the TTL the RRSet already has.
func (m *Module) addRecords(w http.ResponseWriter, req *http.Request) {
	body, ok := decodeRecords(w, req)
	if !ok {
		return
	}
	m.change(w, req, "add_rrset_records", http.StatusCreated, func(r *Record) error {
		if r.Mode != ModePrimary {
			return errMode
		}
		name, typ := req.PathValue("rr_name"), req.PathValue("rr_type")
		if typ == "SOA" {
			return errSOA
		}
		i := rrsetIndex(r, name, typ)
		if i < 0 {
			r.RRSets = append(r.RRSets, RRSet{Name: name, Type: typ, Labels: map[string]string{}})
			i = len(r.RRSets) - 1
		}
		s := &r.RRSets[i]
		if s.Protection.Change {
			return actions.ErrProtected
		}
		if body.TTL != nil {
			if s.TTL != nil && *s.TTL != *body.TTL {
				return &api.FieldError{Field: "ttl", Message: "ttl differs from the TTL of the RRSet"}
			}
			s.TTL = body.TTL
		}
		s.Records = append(slices.Clone(s.Records), body.Records...)
		return m.commit(r, i)
	})
}

// removeRecords removes records by value; removing the last record
// deletes the RRSet.
func (m *Module) removeRecords(w http.ResponseWriter, req *http.Request) {
	body, ok := decodeRecords(w, req)
	if !ok {
		return
	}
	m.changeRRSet(w, req, rrsetChange{command: "remove_rrset_records", status: http.StatusCreated, data: true}, func(s *RRSet) error {
		for k, rec := range body.Records {
			j, err := recordIndex(s, k, rec.Value)
			if err != nil {
				return err
			}
			s.Records = slices.Delete(slices.Clone(s.Records), j, j+1)
		}
		return nil
	})
}

// updateRecords changes the comments of records found by value; it
// answers 200.
func (m *Module) updateRecords(w http.ResponseWriter, req *http.Request) {
	body, ok := decodeRecords(w, req)
	if !ok {
		return
	}
	m.changeRRSet(w, req, rrsetChange{command: "update_rrset_records", status: http.StatusOK, data: true}, func(s *RRSet) error {
		s.Records = slices.Clone(s.Records)
		for k, rec := range body.Records {
			j, err := recordIndex(s, k, rec.Value)
			if err != nil {
				return err
			}
			s.Records[j].Comment = rec.Comment
		}
		return nil
	})
}
