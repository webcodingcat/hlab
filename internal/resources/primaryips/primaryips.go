// Package primaryips serves Hetzner's primary IPs of a project. An IPv4
// primary IP holds an address of the public range (ipam); an IPv6 one is a
// formal /64 of the documentation range that carries no traffic. The
// server records carry the assigned addresses in public_net.
package primaryips

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind and ID sequence of primary IPs; servers created
// without the PublicIPs hook took their primary IP IDs from it too.
const Kind = "primary_ips"

// refType is the resource type of a primary IP in actions.
const refType = "primary_ip"

// Address families.
const (
	typeIPv4 = "ipv4"
	typeIPv6 = "ipv6"
)

// Deps are what the module needs.
type Deps struct {
	// Store is what Delete and Recover work on outside a request; the
	// handlers use the store of the request.
	Store       *store.Store
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	Servers     *servers.Module
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
}

// DNSPtr is a reverse DNS entry of a primary IP.
type DNSPtr = rdns.Entry

// Protection is the protection of a primary IP.
type Protection struct {
	Delete bool `json:"delete"`
}

// PrimaryIP is a primary IP in Hetzner's API shape.
type PrimaryIP struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Labels       map[string]string `json:"labels"`
	Created      time.Time         `json:"created"`
	Blocked      bool              `json:"blocked"`
	Location     json.RawMessage   `json:"location"`
	IP           string            `json:"ip"`
	DNSPtr       []DNSPtr          `json:"dns_ptr"`
	Protection   Protection        `json:"protection"`
	Type         string            `json:"type"`
	AutoDelete   bool              `json:"auto_delete"`
	AssigneeType string            `json:"assignee_type"`
	AssigneeID   *int64            `json:"assignee_id"`
}

// Record is the stored document of a primary IP.
type Record struct {
	PrimaryIP
	Hlab struct {
		ProjectID int64  `json:"project_id"`
		Location  string `json:"location"`
		// Before is the assignment before the assign or unassign running
		// on the primary IP: the API shows it until the action succeeds,
		// and a failed or interrupted action restores it; nil when no such
		// action runs.
		Before *Before `json:"before,omitempty"`
	} `json:"_hlab"`
}

// Before is the assignee of a primary IP before an assign or unassign.
type Before struct {
	AssigneeID *int64 `json:"assignee_id"`
}

// Module is the primary IPs resource type.
type Module struct {
	d Deps
}

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /primary_ips", http.HandlerFunc(m.list))
	r.Handle("POST /primary_ips", http.HandlerFunc(m.create))
	r.Handle("GET /primary_ips/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /primary_ips/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /primary_ips/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /primary_ips/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /primary_ips/{id}/actions/assign", http.HandlerFunc(m.assign))
	r.Handle("POST /primary_ips/{id}/actions/unassign", http.HandlerFunc(m.unassign))
	r.Handle("POST /primary_ips/{id}/actions/change_dns_ptr", http.HandlerFunc(m.changeDNSPtr))
	resactions.RoutesFor(r, Kind, refType)
}

// IPv6Net is the formal IPv6 /64 of a primary IP created on its own:
// 2001:db8:<0x80000000|id>::/64. One created with a server takes
// servers.IPv6Net of the server.
func IPv6Net(id int64) netip.Prefix {
	var b [16]byte
	b[0], b[1], b[2], b[3] = 0x20, 0x01, 0x0d, 0xb8
	binary.BigEndian.PutUint32(b[4:8], 0x80000000|uint32(id))
	return netip.PrefixFrom(netip.AddrFrom16(b), 64)
}

// Load returns primary IP id of projectID, or store.ErrNotFound.
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

// view is the primary IP as the API shows it.
func view(r Record) PrimaryIP {
	p := r.PrimaryIP
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	p.DNSPtr = rdns.Show(p.DNSPtr, owner(r))
	if b := r.Hlab.Before; b != nil {
		p.AssigneeID = b.AssigneeID
	}
	if p.AssigneeType == "" {
		p.AssigneeType = "server"
	}
	return p
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

// Contribute implements resource.NetContributor: the effective PTR name of
// every primary IPv4 not held by a load balancer, which contributes its own.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Type != typeIPv4 || heldByLB(r) {
			continue
		}
		a, _ := netip.ParseAddr(r.IP)
		if doc.PTR == nil {
			doc.PTR = map[netip.Addr]string{}
		}
		doc.PTR[a] = rdns.PTR(r.DNSPtr, a)
	}
	return nil
}

// newRecord makes the record of a new primary IP of type typ in location
// loc: an IPv4 from the public range, or ipv6 as the formal network.
func (m *Module) newRecord(tx store.Tx, projectID int64, name, typ string, loc catalog.Location, ipv6 netip.Prefix) (Record, error) {
	var r Record
	id, err := store.NextID(tx, Kind)
	if err != nil {
		return r, err
	}
	if name == "" {
		name = defaultName(id)
	}
	r.PrimaryIP = PrimaryIP{
		ID:           id,
		Name:         name,
		Labels:       map[string]string{},
		Created:      time.Now().UTC().Truncate(time.Second),
		Location:     loc.Raw,
		DNSPtr:       []DNSPtr{},
		Type:         typ,
		AssigneeType: "server",
	}
	r.Hlab.ProjectID = projectID
	r.Hlab.Location = loc.Name
	switch typ {
	case typeIPv4:
		a, err := ipam.NextPublic(tx, m.d.PublicRange)
		if errors.Is(err, ipam.ErrFull) {
			return r, &api.FieldError{Code: "resource_unavailable", Message: "no free public IPv4 address in the public range"}
		}
		if err != nil {
			return r, err
		}
		r.IP = a.String()
	default:
		if !ipv6.IsValid() {
			ipv6 = IPv6Net(id)
		}
		r.IP = ipv6.String()
	}
	return r, nil
}

// defaultName is the name of a primary IP made without one.
func defaultName(id int64) string { return "primary_ip-" + strconv.FormatInt(id, 10) }

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

// checkName refuses an empty name and one another primary IP of the
// project has.
func checkName(tx store.Tx, projectID int64, name string, exceptID int64) error {
	if strings.TrimSpace(name) == "" {
		return &api.FieldError{Field: "name", Message: "name is required"}
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

var listOptions = api.ListOptions[PrimaryIP]{
	Name:   func(p PrimaryIP) string { return p.Name },
	Labels: func(p PrimaryIP) map[string]string { return p.Labels },
	Sort: map[string]func(a, b PrimaryIP) int{
		"id":      func(a, b PrimaryIP) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b PrimaryIP) int { return strings.Compare(a.Name, b.Name) },
		"created": func(a, b PrimaryIP) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(PrimaryIP, []string) bool{
		"ip": func(p PrimaryIP, ips []string) bool { return slices.Contains(ips, p.IP) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []PrimaryIP
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		out = make([]PrimaryIP, 0, len(recs))
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
	api.JSON(w, http.StatusOK, map[string]any{"primary_ip": view(r)})
}

// createRequest is POST /primary_ips.
type createRequest struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   json.RawMessage   `json:"location"`
	AssigneeID *int64            `json:"assignee_id"`
	AutoDelete bool              `json:"auto_delete"`
	Labels     map[string]string `json:"labels"`
}

// create stores a primary IP in a location or assigned to a stopped
// server (whose location it takes); the latter starts an assign action.
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
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(body.Labels); err != nil {
			return err
		}
		if body.Type != typeIPv4 && body.Type != typeIPv6 {
			return &api.FieldError{Field: "type", Message: "type must be ipv4 or ipv6"}
		}
		ref := refString(body.Location)
		hasServer := body.AssigneeID != nil && *body.AssigneeID != 0
		if hasServer == (ref != "") {
			return &api.FieldError{Field: "location", Message: "give either assignee_id or location"}
		}
		var srv servers.Record
		if hasServer {
			var err error
			if srv, err = loadServer(tx, p.ID, *body.AssigneeID); err != nil {
				return err
			}
			ref = srv.Hlab.Location
		}
		loc, ok := m.location(ref)
		if !ok {
			return &api.FieldError{Field: "location", Message: "location " + ref + " not found"}
		}
		var err error
		if r, err = m.newRecord(tx, p.ID, body.Name, body.Type, loc, netip.Prefix{}); err != nil {
			return err
		}
		r.Labels = body.Labels
		r.AutoDelete = body.AutoDelete
		if hasServer {
			if err := assignTo(tx, &r, srv); err != nil {
				return err
			}
			refs := []actions.Ref{{ID: r.ID, Type: refType}, {ID: srv.ID, Type: "server"}}
			if action, err = acts.Start(tx, p.ID, "assign_primary_ip", refs); err != nil {
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
		sid := *body.AssigneeID
		acts.Run(action.ID, func(ctx context.Context, _ func(int)) error { return m.applyTo(ctx, sid) })
	}
	api.JSON(w, http.StatusCreated, map[string]any{"primary_ip": view(r), "action": action})
}

// update changes only the fields present in the body; labels, when
// present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name       *string            `json:"name"`
		Labels     *map[string]string `json:"labels"`
		AutoDelete *bool              `json:"auto_delete"`
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
			if err := checkName(tx, p.ID, *body.Name, id); err != nil {
				return err
			}
			r.Name = *body.Name
		}
		if body.Labels != nil {
			if err := api.CheckLabels(*body.Labels); err != nil {
				return err
			}
			r.Labels = *body.Labels
		}
		if body.AutoDelete != nil {
			r.AutoDelete = *body.AutoDelete
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"primary_ip": view(r)})
}

// delete removes an unassigned primary IP without delete protection and
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
		if heldByLB(r) {
			return &api.FieldError{Code: "primary_ip_assigned", Message: "the primary IP is held by a load balancer"}
		}
		if r.AssigneeID != nil {
			return &api.FieldError{Code: "must_be_unassigned", Message: "unassign the primary IP before deleting it"}
		}
		return remove(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete implements resource.Deleter for project deletion: it removes
// primary IP id of any project and releases its IPv4. A primary IP
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
