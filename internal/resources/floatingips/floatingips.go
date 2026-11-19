// Package floatingips serves Hetzner's floating IPs of a project. An IPv4
// floating IP holds an address of the public range (ipam) that the agent
// routes to the assigned server's public IPv4; an IPv6 one is a formal /64
// of the documentation range that carries no traffic. The guest configures
// the address itself.
package floatingips

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
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
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind and ID sequence of floating IPs.
const Kind = "floating_ips"

// refType is the resource type of a floating IP in actions.
const refType = "floating_ip"

// Address families.
const (
	typeIPv4 = "ipv4"
	typeIPv6 = "ipv6"
)

// Deps are what the module needs.
type Deps struct {
	// Store is what Delete works on outside a request; the handlers use
	// the store of the request.
	Store       *store.Store
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
}

// DNSPtr is a reverse DNS entry of a floating IP.
type DNSPtr = rdns.Entry

// Protection is the protection of a floating IP.
type Protection struct {
	Delete bool `json:"delete"`
}

// FloatingIP is a floating IP in Hetzner's API shape.
type FloatingIP struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Description  *string           `json:"description"`
	IP           string            `json:"ip"`
	Type         string            `json:"type"`
	Server       *int64            `json:"server"`
	DNSPtr       []DNSPtr          `json:"dns_ptr"`
	HomeLocation json.RawMessage   `json:"home_location"`
	Blocked      bool              `json:"blocked"`
	Protection   Protection        `json:"protection"`
	Labels       map[string]string `json:"labels"`
	Created      time.Time         `json:"created"`
}

// Record is the stored document of a floating IP.
type Record struct {
	FloatingIP
	Hlab struct {
		ProjectID    int64  `json:"project_id"`
		HomeLocation string `json:"home_location"`
	} `json:"_hlab"`
}

// Module is the floating IPs resource type.
type Module struct {
	d Deps
}

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /floating_ips", http.HandlerFunc(m.list))
	r.Handle("POST /floating_ips", http.HandlerFunc(m.create))
	r.Handle("GET /floating_ips/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /floating_ips/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /floating_ips/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /floating_ips/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /floating_ips/{id}/actions/assign", http.HandlerFunc(m.assign))
	r.Handle("POST /floating_ips/{id}/actions/unassign", http.HandlerFunc(m.unassign))
	r.Handle("POST /floating_ips/{id}/actions/change_dns_ptr", http.HandlerFunc(m.changeDNSPtr))
	resactions.RoutesFor(r, Kind, refType)
}

// IPv6Net is the formal IPv6 /64 of floating IP id:
// 2001:db8:<0xC0000000|id>::/64.
func IPv6Net(id int64) netip.Prefix {
	var b [16]byte
	b[0], b[1], b[2], b[3] = 0x20, 0x01, 0x0d, 0xb8
	binary.BigEndian.PutUint32(b[4:8], 0xC0000000|uint32(id))
	return netip.PrefixFrom(netip.AddrFrom16(b), 64)
}

// Load returns floating IP id of projectID, or store.ErrNotFound.
func Load(tx store.Tx, projectID, id int64) (Record, error) {
	return store.Get[Record](tx, Kind, projectID, id)
}

func put(tx store.Tx, r Record) error {
	if r.Labels == nil {
		r.Labels = map[string]string{}
	}
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

// remove deletes the record of r and releases its IPv4.
func remove(tx store.Tx, r Record) error {
	if err := store.Delete(tx, Kind, r.Hlab.ProjectID, r.ID); err != nil {
		return err
	}
	if r.Type == typeIPv4 {
		if a, err := netip.ParseAddr(r.IP); err == nil {
			return ipam.Release(tx, a)
		}
	}
	return nil
}

// view is the floating IP as the API shows it.
func view(r Record) FloatingIP {
	f := r.FloatingIP
	if f.Labels == nil {
		f.Labels = map[string]string{}
	}
	f.DNSPtr = rdns.Show(f.DNSPtr, owner(r))
	return f
}

// owner is the address range r owns: its IPv4 as /32 or its IPv6 /64.
// The stored IP always parses: it is written only from netip values.
func owner(r Record) netip.Prefix {
	if r.Type == typeIPv4 {
		a, _ := netip.ParseAddr(r.IP)
		return netip.PrefixFrom(a, 32)
	}
	p, _ := netip.ParsePrefix(r.IP)
	return p
}

// pushLogged sends the agent document; a failure is logged, the state is
// already stored and the next push carries it.
func (m *Module) pushLogged(ctx context.Context) {
	if err := m.d.PushAgent(ctx); err != nil {
		slog.Warn("push agent document after a floating IP change", "err", err)
	}
}

// location finds a catalog location by name or ID.
func (m *Module) location(ref string) (catalog.Location, bool) {
	if l, ok := m.d.Catalog.Location(ref); ok {
		return l, true
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	for _, l := range m.d.Catalog.Locations {
		if err == nil && l.ID == id {
			return l, true
		}
	}
	return catalog.Location{}, false
}

// refString returns a JSON string or number as text; anything else as "".
func refString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// checkName refuses a name another floating IP of the project has; an
// empty name is allowed only when allowEmpty (create gives a default).
func checkName(tx store.Tx, projectID int64, name string, exceptID int64, allowEmpty bool) error {
	if strings.TrimSpace(name) == "" {
		if allowEmpty && name == "" {
			return nil
		}
		return &api.FieldError{Field: "name", Message: "name must not be empty"}
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

var listOptions = api.ListOptions[FloatingIP]{
	Name:   func(f FloatingIP) string { return f.Name },
	Labels: func(f FloatingIP) map[string]string { return f.Labels },
	Sort: map[string]func(a, b FloatingIP) int{
		"id":      func(a, b FloatingIP) int { return cmp.Compare(a.ID, b.ID) },
		"created": func(a, b FloatingIP) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(FloatingIP, []string) bool{
		"ip": func(f FloatingIP, ips []string) bool { return slices.Contains(ips, f.IP) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []FloatingIP
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		out = make([]FloatingIP, 0, len(recs))
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
	api.JSON(w, http.StatusOK, map[string]any{"floating_ip": view(r)})
}

// createRequest is POST /floating_ips.
type createRequest struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Description  *string           `json:"description"`
	HomeLocation json.RawMessage   `json:"home_location"`
	Server       *int64            `json:"server"`
	Labels       map[string]string `json:"labels"`
}

// create stores a floating IP with a home location or assigned to a server
// (whose location becomes the home location); the latter starts an assign
// action.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	var r Record
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0, true); err != nil {
			return err
		}
		if err := api.CheckLabels(body.Labels); err != nil {
			return err
		}
		if body.Type != typeIPv4 && body.Type != typeIPv6 {
			return &api.FieldError{Field: "type", Message: "type must be ipv4 or ipv6"}
		}
		ref := refString(body.HomeLocation)
		hasServer := body.Server != nil && *body.Server != 0
		if hasServer == (ref != "") {
			return &api.FieldError{Field: "home_location", Message: "give either server or home_location"}
		}
		var srv servers.Record
		if hasServer {
			var err error
			if srv, err = loadServer(tx, p.ID, *body.Server); err != nil {
				return err
			}
			ref = srv.Hlab.Location
		}
		loc, ok := m.location(ref)
		if !ok {
			return &api.FieldError{Field: "home_location", Message: "location " + ref + " not found"}
		}
		var err error
		if r, err = m.newRecord(tx, p.ID, body.Name, body.Type, loc); err != nil {
			return err
		}
		r.Labels = body.Labels
		r.Description = body.Description
		if hasServer {
			if err := m.assignTo(tx, &r, srv); err != nil {
				return err
			}
			refs := []actions.Ref{{ID: r.ID, Type: refType}, {ID: srv.ID, Type: "server"}}
			if action, err = acts.Start(tx, p.ID, "assign_floating_ip", refs); err != nil {
				return err
			}
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	if action != nil {
		acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.d.PushAgent(ctx) })
	} else {
		m.pushLogged(req.Context())
	}
	api.JSON(w, http.StatusCreated, map[string]any{"floating_ip": view(r), "action": action})
}

// newRecord makes the record of a new floating IP of type typ with home
// location loc: an IPv4 from the public range, or the formal IPv6 /64.
func (m *Module) newRecord(tx store.Tx, projectID int64, name, typ string, loc catalog.Location) (Record, error) {
	var r Record
	id, err := store.NextID(tx, Kind)
	if err != nil {
		return r, err
	}
	if name == "" {
		name = "floating_ip-" + strconv.FormatInt(id, 10)
	}
	r.FloatingIP = FloatingIP{
		ID:           id,
		Name:         name,
		Labels:       map[string]string{},
		Created:      time.Now().UTC().Truncate(time.Second),
		HomeLocation: loc.Raw,
		DNSPtr:       []DNSPtr{},
		Type:         typ,
	}
	r.Hlab.ProjectID = projectID
	r.Hlab.HomeLocation = loc.Name
	if typ == typeIPv4 {
		a, err := ipam.NextPublic(tx, m.d.PublicRange)
		if errors.Is(err, ipam.ErrFull) {
			return r, &api.FieldError{Code: "resource_unavailable", Message: "no free public IPv4 address in the public range"}
		}
		if err != nil {
			return r, err
		}
		r.IP = a.String()
		return r, nil
	}
	r.IP = IPv6Net(id).String()
	return r, nil
}

// update changes only the fields present in the body; labels, when
// present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
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
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if r, err = Load(tx, p.ID, id); err != nil {
			return err
		}
		if body.Name != nil {
			if err := checkName(tx, p.ID, *body.Name, id, false); err != nil {
				return err
			}
			r.Name = *body.Name
		}
		if body.Description != nil {
			r.Description = body.Description
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
	api.JSON(w, http.StatusOK, map[string]any{"floating_ip": view(r)})
}

// delete removes an unassigned floating IP without delete protection and
// releases its IPv4.
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		if r.Server != nil {
			return &api.FieldError{Code: "must_be_unassigned", Message: "unassign the floating IP before deleting it"}
		}
		return remove(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.pushLogged(req.Context())
	w.WriteHeader(http.StatusNoContent)
}

// Delete implements resource.Deleter for project deletion: it removes
// floating IP id of any project and releases its IPv4. A floating IP
// already gone is no error.
func (m *Module) Delete(_ context.Context, id int64) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.ID == id {
				return remove(tx, r)
			}
		}
		return nil
	})
}
