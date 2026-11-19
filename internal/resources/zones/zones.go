// Package zones is the resource module of Hetzner's DNS zones: zones with
// their RRSets stored in one document, their actions and their publishing
// to the agent and the host's NRPT rules.
package zones

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/dnszone"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind and ID sequence of zones.
const Kind = "zones"

// refType is the resource type of a zone in actions.
const refType = "zone"

// Zone modes.
const (
	ModePrimary   = "primary"
	ModeSecondary = "secondary"
)

// defaultTTL is the TTL of a zone created without one.
const defaultTTL = 3600

// ShadowWatch checks whether primary zones shadow a public domain.
type ShadowWatch interface {
	// Check starts a background check; a later call replaces the result.
	Check(zone, project string)
	Forget(zone string)
}

// Deps are what the module needs.
type Deps struct {
	// Store is what the action work uses outside a request.
	Store *store.Store
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
	// SetZones sets the NRPT rules to exactly these zone names.
	SetZones func(ctx context.Context, zones []string) error
	// Shadow is told about public primary zones; nil: none.
	Shadow ShadowWatch
	// Now is the clock of SOA serials and created times; nil: time.Now.
	Now func() time.Time
}

// ZoneRef names a zone and the name of its project.
type ZoneRef struct{ Zone, Project string }

// PrimaryNameserver is an entry of primary_nameservers.
type PrimaryNameserver struct {
	Address       string `json:"address"`
	Port          int    `json:"port"`
	TSIGKey       string `json:"tsig_key,omitempty"`
	TSIGAlgorithm string `json:"tsig_algorithm,omitempty"`
}

// Protection is the protection of a zone.
type Protection struct {
	Delete bool `json:"delete"`
}

// AuthoritativeNameservers are the nameservers of a zone.
type AuthoritativeNameservers struct {
	Assigned            []string   `json:"assigned"`
	Delegated           []string   `json:"delegated"`
	DelegationLastCheck *time.Time `json:"delegation_last_check"`
	DelegationStatus    string     `json:"delegation_status"`
}

// Zone is a zone in Hetzner's API shape.
type Zone struct {
	ID                       int64                    `json:"id"`
	Name                     string                   `json:"name"`
	Created                  time.Time                `json:"created"`
	Mode                     string                   `json:"mode"`
	PrimaryNameservers       []PrimaryNameserver      `json:"primary_nameservers"`
	Labels                   map[string]string        `json:"labels"`
	Protection               Protection               `json:"protection"`
	TTL                      int                      `json:"ttl"`
	Status                   string                   `json:"status"`
	RecordCount              int                      `json:"record_count"`
	AuthoritativeNameservers AuthoritativeNameservers `json:"authoritative_nameservers"`
	Registrar                string                   `json:"registrar"`
}

// RRSetProtection is the protection of an RRSet.
type RRSetProtection struct {
	Change bool `json:"change"`
}

// RRSetRecord is one record of an RRSet.
type RRSetRecord struct {
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

// RRSet is a stored RRSet of a zone, identified by name and type.
type RRSet struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	TTL        *int              `json:"ttl"`
	Labels     map[string]string `json:"labels"`
	Protection RRSetProtection   `json:"protection"`
	Records    []RRSetRecord     `json:"records"`
}

// Data is the RRSet without labels and protection.
func (s RRSet) Data() dnszone.RRSet {
	recs := make([]dnszone.Record, len(s.Records))
	for i, r := range s.Records {
		recs[i] = dnszone.Record{Value: r.Value, Comment: r.Comment}
	}
	return dnszone.RRSet{Name: s.Name, Type: s.Type, TTL: s.TTL, Records: recs}
}

// fromData is d as a stored RRSet without labels and protection.
func fromData(d dnszone.RRSet) RRSet {
	recs := make([]RRSetRecord, len(d.Records))
	for i, r := range d.Records {
		recs[i] = RRSetRecord{Value: r.Value, Comment: r.Comment}
	}
	return RRSet{Name: d.Name, Type: d.Type, TTL: d.TTL, Labels: map[string]string{}, Records: recs}
}

// Record is the stored document of a zone; its RRSets live in it.
// record_count, status, authoritative_nameservers and registrar are
// computed when shown.
type Record struct {
	Zone
	RRSets []RRSet `json:"rrsets"`
	Hlab   struct {
		ProjectID int64 `json:"project_id"`
	} `json:"_hlab"`
}

// Sets returns the RRSets of r without labels and protection.
func (r Record) Sets() []dnszone.RRSet {
	out := make([]dnszone.RRSet, len(r.RRSets))
	for i, s := range r.RRSets {
		out[i] = s.Data()
	}
	return out
}

// Module is the zones resource type.
type Module struct {
	d Deps
	// publishMu orders the publishes of all zone actions.
	publishMu sync.Mutex
}

// New returns the module.
func New(d Deps) *Module {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Module{d: d}
}

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module. {id} is the zone's ID or name.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /zones", http.HandlerFunc(m.list))
	r.Handle("POST /zones", http.HandlerFunc(m.create))
	r.Handle("GET /zones/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /zones/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /zones/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /zones/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /zones/{id}/actions/change_ttl", http.HandlerFunc(m.changeTTL))
	r.Handle("POST /zones/{id}/actions/change_primary_nameservers", http.HandlerFunc(m.changePrimaryNameservers))
	r.Handle("GET /zones/{id}/zonefile", http.HandlerFunc(m.exportZonefile))
	m.rrsetRoutes(r)
	// GET /zones/actions/{id} conflicts with /zones/{id}/actions in
	// http.ServeMux; the less specific pattern takes it.
	r.Handle("GET /zones/{id}/{sub}", http.HandlerFunc(m.zoneAction))
	resactions.RoutesFor(r, Kind, refType)
}

// Load returns zone idOrName (an ID or a zone name) of projectID, or
// store.ErrNotFound.
func Load(tx store.Tx, projectID int64, idOrName string) (Record, error) {
	if id, err := strconv.ParseInt(idOrName, 10, 64); err == nil {
		return store.Get[Record](tx, Kind, projectID, id)
	}
	recs, err := store.List[Record](tx, Kind, projectID)
	if err != nil {
		return Record{}, err
	}
	for _, r := range recs {
		if r.Name == idOrName {
			return r, nil
		}
	}
	return Record{}, store.ErrNotFound
}

func put(tx store.Tx, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

// view is the zone as the API shows it.
func view(r Record) Zone {
	z := r.Zone
	if z.Labels == nil {
		z.Labels = map[string]string{}
	}
	if z.PrimaryNameservers == nil {
		z.PrimaryNameservers = []PrimaryNameserver{}
	}
	z.Status = "ok"
	z.Registrar = "unknown"
	z.RecordCount = 0
	for _, s := range r.RRSets {
		z.RecordCount += len(s.Records)
	}
	z.AuthoritativeNameservers = AuthoritativeNameservers{
		Assigned:         slices.Clone(dnszone.Nameservers),
		Delegated:        []string{},
		DelegationStatus: "unknown",
	}
	return z
}

// fieldErr turns a *dnszone.FieldError into an *api.FieldError whose field
// is prefixed; other errors stay as they are.
func fieldErr(err error, prefix string) error {
	var fe *dnszone.FieldError
	if !errors.As(err, &fe) {
		return err
	}
	return &api.FieldError{Code: fe.Code, Field: prefix + fe.Field, Message: fe.Message}
}

// errMode is the answer to an operation the zone's mode does not support.
var errMode = &api.FieldError{Code: "incorrect_zone_mode", Message: "This operation is not supported for this Zone's mode"}

// checkTTL refuses a zone TTL outside 60..2147483647.
func checkTTL(ttl int) error {
	if ttl < 60 || ttl > math.MaxInt32 {
		return &api.FieldError{Field: "ttl", Message: "ttl must be between 60 and 2147483647"}
	}
	return nil
}

var listOptions = api.ListOptions[Zone]{
	Name:   func(z Zone) string { return z.Name },
	Labels: func(z Zone) map[string]string { return z.Labels },
	Sort: map[string]func(a, b Zone) int{
		"id":      func(a, b Zone) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Zone) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b Zone) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Zone, []string) bool{
		"mode": func(z Zone, v []string) bool { return slices.Contains(v, z.Mode) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []Zone
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		out = make([]Zone, 0, len(recs))
		for _, r := range recs {
			out = append(out, view(r))
		}
		return err
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.ListRequest(w, req, Kind, out, listOptions)
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		r, err = Load(tx, p.ID, req.PathValue("id"))
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"zone": view(r)})
}

// rrsetRequest is an RRSet in a request body.
type rrsetRequest struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	TTL     *int              `json:"ttl"`
	Labels  map[string]string `json:"labels"`
	Records []RRSetRecord     `json:"records"`
}

// createRequest is POST /zones.
type createRequest struct {
	Name               string              `json:"name"`
	Mode               string              `json:"mode"`
	TTL                *int                `json:"ttl"`
	Labels             map[string]string   `json:"labels"`
	PrimaryNameservers []PrimaryNameserver `json:"primary_nameservers"`
	RRSets             []rrsetRequest      `json:"rrsets"`
	Zonefile           *string             `json:"zonefile"`
}

// create stores a zone and starts its create_zone action, which publishes
// the zones.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var r Record
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = m.newZone(tx, body); err != nil {
			return err
		}
		if r.ID, err = store.NextID(tx, Kind); err != nil {
			return err
		}
		r.Hlab.ProjectID = p.ID
		if err := put(tx, r); err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "create_zone", []actions.Ref{zoneRef(r.ID)})
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.publish(acts, action)
	if m.d.Shadow != nil && r.Mode == ModePrimary && !dnszone.PrivateUse(r.Name) {
		m.d.Shadow.Check(r.Name, p.Name)
	}
	api.JSON(w, http.StatusCreated, map[string]any{"zone": view(r), "action": action})
}

// newZone validates body and returns the zone without ID and project.
func (m *Module) newZone(tx store.Tx, body createRequest) (Record, error) {
	var r Record
	if err := dnszone.CheckZoneName(body.Name); err != nil {
		return r, fieldErr(err, "")
	}
	if body.Mode != ModePrimary && body.Mode != ModeSecondary {
		return r, &api.FieldError{Field: "mode", Message: "mode must be primary or secondary"}
	}
	ttl := defaultTTL
	if body.TTL != nil {
		ttl = *body.TTL
		if err := checkTTL(ttl); err != nil {
			return r, err
		}
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	if err := api.CheckLabels(body.Labels); err != nil {
		return r, err
	}
	all, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return r, err
	}
	if slices.ContainsFunc(all, func(z Record) bool { return z.Name == body.Name }) {
		return r, &api.FieldError{Code: "uniqueness_error", Field: "name", Message: "zone name is already used"}
	}
	now := m.d.Now().UTC().Truncate(time.Second)
	r.Zone = Zone{
		Name:               body.Name,
		Created:            now,
		Mode:               body.Mode,
		PrimaryNameservers: []PrimaryNameserver{},
		Labels:             body.Labels,
		TTL:                ttl,
	}
	if body.Mode == ModeSecondary {
		r.PrimaryNameservers, err = checkNameservers(body.PrimaryNameservers)
		r.RRSets = []RRSet{}
		return r, err
	}
	if body.Zonefile != nil {
		return r, &api.FieldError{Field: "zonefile", Message: "zone file import is not available yet"}
	}
	r.RRSets, err = createSets(body.Name, now, body.RRSets)
	return r, err
}

// createSets returns the default SOA and NS RRSets of zone plus the given
// ones; a given apex NS replaces the default one.
func createSets(zone string, now time.Time, given []rrsetRequest) ([]RRSet, error) {
	var sets []RRSet
	for _, d := range dnszone.DefaultSets(zone, dnszone.NextSerial(0, now)) {
		sets = append(sets, fromData(d))
	}
	seen := map[string]bool{}
	for i, g := range given {
		prefix := fmt.Sprintf("rrsets[%d].", i)
		s := RRSet{Name: g.Name, Type: g.Type, TTL: g.TTL, Labels: g.Labels, Records: g.Records}
		if s.Labels == nil {
			s.Labels = map[string]string{}
		}
		if err := dnszone.CheckRRSet(zone, s.Data()); err != nil {
			return nil, fieldErr(err, prefix)
		}
		if err := api.CheckLabels(s.Labels); err != nil {
			return nil, err
		}
		if s.Type == "SOA" {
			return nil, &api.FieldError{Field: prefix + "type", Message: "the SOA RRSet is managed by hlab"}
		}
		key := s.Name + "/" + s.Type
		if seen[key] {
			return nil, &api.FieldError{Field: prefix + "name", Message: "RRSet " + key + " is given twice"}
		}
		seen[key] = true
		if i := slices.IndexFunc(sets, func(o RRSet) bool { return o.Name == s.Name && o.Type == s.Type }); i >= 0 {
			sets[i] = s
			continue
		}
		sets = append(sets, s)
	}
	data := make([]dnszone.RRSet, len(sets))
	for i, s := range sets {
		data[i] = s.Data()
	}
	if err := dnszone.CheckConflicts(data); err != nil {
		return nil, fieldErr(err, "")
	}
	return sets, nil
}

// update changes the labels of a zone; it answers at once without action.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Labels *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, req.PathValue("id")); err != nil {
			return err
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			r.Labels = *body.Labels
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"zone": view(r)})
}

// delete removes a zone without delete protection and starts its
// delete_zone action, which publishes the zones.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var r Record
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, req.PathValue("id")); err != nil {
			return err
		}
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		if action, err = acts.Start(tx, p.ID, "delete_zone", []actions.Ref{zoneRef(r.ID)}); err != nil {
			return err
		}
		return store.Delete(tx, Kind, p.ID, r.ID)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.publish(acts, action)
	if m.d.Shadow != nil {
		m.d.Shadow.Forget(r.Name)
	}
	api.JSON(w, http.StatusCreated, map[string]any{"action": action})
}

// Names returns the names of all zones of all projects, sorted.
func Names(tx store.Tx) ([]string, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return names, nil
}

// Primary returns the names and project names of all primary zones, sorted
// by zone.
func Primary(tx store.Tx) ([]ZoneRef, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return nil, err
	}
	projects, err := store.ListProjects(tx)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	out := []ZoneRef{}
	for _, r := range recs {
		if r.Mode == ModePrimary {
			out = append(out, ZoneRef{Zone: r.Name, Project: names[r.Hlab.ProjectID]})
		}
	}
	slices.SortFunc(out, func(a, b ZoneRef) int { return cmp.Compare(a.Zone, b.Zone) })
	return out, nil
}
