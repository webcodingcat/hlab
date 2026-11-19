// Package loadbalancers serves Hetzner's load balancers of a project. The
// appliance's agent runs one HAProxy per load balancer from the document
// this module contributes. The package is split per feature: create.go and
// actions.go hold creation, deletion and the type, algorithm and protection
// actions; network.go, services.go, targets.go, status.go and dnsptr.go the
// rest.
package loadbalancers

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind and ID sequence of load balancers.
const Kind = "load_balancers"

// refType is the resource type of a load balancer in actions.
const refType = "load_balancer"

// maxName is the longest load balancer name, as at Hetzner.
const maxName = 128

// Algorithms.
const (
	algoRoundRobin = "round_robin"
	algoLeastConn  = "least_connections"
)

// PublicIPs is what the module needs of the primary IPs module.
type PublicIPs interface {
	AssignLoadBalancer(tx store.Tx, projectID, lbID int64, location string, ipv4, ipv6 *int64) (netip.Addr, netip.Prefix, error)
	LoadBalancerDeleted(tx store.Tx, lbID int64) error
}

// Networks is what the module needs of the networks module.
type Networks interface {
	AttachLoadBalancer(tx store.Tx, projectID int64, zone string, netID int64, ip, ipRange string) (netip.Addr, int, error)
}

// Deps are what the module needs.
type Deps struct {
	// Store is what the hooks and jobs work on outside a request; the
	// handlers use the store of the request.
	Store       *store.Store
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
	// AgentStatus reads the agent's status (health and counters).
	AgentStatus func(ctx context.Context) (agentapi.Status, error)
	// PublicIPs assigns primary IPs; nil: public_net IDs answer
	// invalid_input.
	PublicIPs PublicIPs
	// Networks attaches to networks; nil: a network answers invalid_input.
	Networks Networks
	// Clock drives the jobs; nil: the real clock.
	Clock scheduler.Clock
}

// Protection is the protection of a load balancer.
type Protection struct {
	Delete bool `json:"delete"`
}

// Algorithm is the balancing algorithm of a load balancer.
type Algorithm struct {
	Type string `json:"type"`
}

// PrivateNet is the load balancer's address in a network.
type PrivateNet struct {
	Network int64  `json:"network"`
	IP      string `json:"ip"`
}

// Record is the stored document of a load balancer.
type Record struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Labels          map[string]string `json:"labels"`
	Created         time.Time         `json:"created"`
	Algorithm       string            `json:"algorithm"`
	Protection      Protection        `json:"protection"`
	PublicInterface bool              `json:"public_interface"`
	Services        []Service         `json:"services"`
	Targets         []Target          `json:"targets"`
	PrivateNet      []PrivateNet      `json:"private_net"`
	DNSPtr          []rdns.Entry      `json:"dns_ptr"`
	Hlab            struct {
		ProjectID int64  `json:"project_id"`
		Location  string `json:"location"`
		Type      string `json:"type"`
		// IPv4 is the public IPv4; IPv4ID the primary IP holding it (nil:
		// a system address of the public range).
		IPv4   string `json:"ipv4"`
		IPv4ID *int64 `json:"ipv4_id"`
		// IPv6Net is the /64 of an IPv6 primary IP (IPv6ID); "" without.
		IPv6Net string `json:"ipv6_net"`
		IPv6ID  *int64 `json:"ipv6_id"`
		// VLAN is the VLAN of the attached network.
		VLAN int `json:"vlan"`
	} `json:"_hlab"`
}

// IPv4 is the public IPv4 of r; it always parses, it is written only from
// netip values.
func (r Record) IPv4() netip.Addr {
	a, _ := netip.ParseAddr(r.Hlab.IPv4)
	return a
}

// IPv6 is the public IPv6 of r: <its /64>::1 with an IPv6 primary IP, the
// formal IPv6(id) otherwise.
func (r Record) IPv6() netip.Addr {
	if p, err := netip.ParsePrefix(r.Hlab.IPv6Net); err == nil {
		return p.Masked().Addr().Next()
	}
	return IPv6(r.ID)
}

// IPv6Net is the /64 of r's IPv6.
func (r Record) IPv6Net() netip.Prefix {
	return netip.PrefixFrom(r.IPv6(), 64).Masked()
}

// IPv6 is the formal IPv6 of a load balancer without an IPv6 primary IP
// (Q14): 2001:db8:<0xA0000000|id as 32 bits>::1.
func IPv6(id int64) netip.Addr {
	return formalIPv6(0xA0000000, id)
}

// formalIPv6 is 2001:db8:<prefix|id as 32 bits>::1.
func formalIPv6(prefix uint32, id int64) netip.Addr {
	var b [16]byte
	b[0], b[1], b[2], b[3] = 0x20, 0x01, 0x0d, 0xb8
	binary.BigEndian.PutUint32(b[4:8], prefix|uint32(id))
	b[15] = 1
	return netip.AddrFrom16(b)
}

// IPView is the public IPv4 or IPv6 of a load balancer as the API shows it.
type IPView struct {
	ID      *int64  `json:"id"`
	IP      *string `json:"ip"`
	Blocked bool    `json:"blocked"`
	DNSPtr  *string `json:"dns_ptr"`
}

// PublicNet is public_net as the API shows it.
type PublicNet struct {
	Enabled bool   `json:"enabled"`
	IPv4    IPView `json:"ipv4"`
	IPv6    IPView `json:"ipv6"`
}

// LoadBalancer is a load balancer in Hetzner's API shape.
type LoadBalancer struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	PublicNet        PublicNet         `json:"public_net"`
	PrivateNet       []PrivateNet      `json:"private_net"`
	Location         json.RawMessage   `json:"location"`
	LoadBalancerType json.RawMessage   `json:"load_balancer_type"`
	Protection       Protection        `json:"protection"`
	Labels           map[string]string `json:"labels"`
	Created          time.Time         `json:"created"`
	Services         []Service         `json:"services"`
	Targets          []TargetView      `json:"targets"`
	Algorithm        Algorithm         `json:"algorithm"`
	OutgoingTraffic  int64             `json:"outgoing_traffic"`
	IngoingTraffic   int64             `json:"ingoing_traffic"`
	IncludedTraffic  int64             `json:"included_traffic"`
}

// Module is the load balancers resource type.
type Module struct {
	d    Deps
	stat lbStatus // health and counters, filled by status.go
}

// New returns the module.
func New(d Deps) *Module {
	if d.Clock == nil {
		d.Clock = scheduler.RealClock()
	}
	return &Module{d: d}
}

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /load_balancers", http.HandlerFunc(m.list))
	r.Handle("POST /load_balancers", http.HandlerFunc(m.create))
	r.Handle("GET /load_balancers/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /load_balancers/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /load_balancers/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /load_balancers/{id}/actions/change_algorithm", http.HandlerFunc(m.changeAlgorithm))
	r.Handle("POST /load_balancers/{id}/actions/change_type", http.HandlerFunc(m.changeType))
	r.Handle("POST /load_balancers/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	m.networkRoutes(r)
	m.serviceRoutes(r)
	m.targetRoutes(r)
	m.statusRoutes(r)
	m.dnsPtrRoutes(r)
	resactions.RoutesFor(r, Kind, refType)
}

// Load returns load balancer id of projectID, or store.ErrNotFound.
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

// Type is a load balancer type of the catalog.
type Type struct {
	ID                      int64  `json:"id"`
	Name                    string `json:"name"`
	MaxConnections          int    `json:"max_connections"`
	MaxServices             int    `json:"max_services"`
	MaxTargets              int    `json:"max_targets"`
	MaxAssignedCertificates int    `json:"max_assigned_certificates"`
	Prices                  []struct {
		Location        string `json:"location"`
		IncludedTraffic int64  `json:"included_traffic"`
	} `json:"prices"`
	Raw json.RawMessage `json:"-"`
}

// lbType finds a catalog load balancer type by name or ID.
func (m *Module) lbType(ref string) (Type, bool) {
	for _, raw := range m.d.Catalog.LoadBalancerTypes {
		var t Type
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		if t.Name == ref || strconv.FormatInt(t.ID, 10) == ref {
			t.Raw = raw
			return t, true
		}
	}
	return Type{}, false
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

// show is load balancer r as the API shows it.
func (m *Module) show(tx store.Tx, r Record) (LoadBalancer, error) {
	targets, err := m.viewTargets(tx, r)
	if err != nil {
		return LoadBalancer{}, err
	}
	loc, _ := m.location(r.Hlab.Location)
	typ, _ := m.lbType(r.Hlab.Type)
	var included int64
	for _, p := range typ.Prices {
		if p.Location == r.Hlab.Location {
			included = p.IncludedTraffic
		}
	}
	v4, v6 := r.IPv4(), r.IPv6()
	ip4, ip6 := v4.String(), v6.String()
	ptr4 := rdns.PTR(r.DNSPtr, v4)
	var ptr6 *string
	for _, e := range r.DNSPtr {
		if e.IP == ip6 {
			ptr6 = &e.DNSPtr
		}
	}
	l := LoadBalancer{
		ID:   r.ID,
		Name: r.Name,
		PublicNet: PublicNet{
			Enabled: r.PublicInterface,
			IPv4:    IPView{ID: r.Hlab.IPv4ID, IP: &ip4, DNSPtr: &ptr4},
			IPv6:    IPView{ID: r.Hlab.IPv6ID, IP: &ip6, DNSPtr: ptr6},
		},
		PrivateNet:       r.PrivateNet,
		Location:         loc.Raw,
		LoadBalancerType: typ.Raw,
		Protection:       r.Protection,
		Labels:           r.Labels,
		Created:          r.Created,
		Services:         r.Services,
		Targets:          targets,
		Algorithm:        Algorithm{Type: r.Algorithm},
		IncludedTraffic:  included,
	}
	if l.Labels == nil {
		l.Labels = map[string]string{}
	}
	if l.PrivateNet == nil {
		l.PrivateNet = []PrivateNet{}
	}
	if l.Services == nil {
		l.Services = []Service{}
	}
	if l.Targets == nil {
		l.Targets = []TargetView{}
	}
	return l, nil
}

// checkName refuses an empty or too long name and one another load
// balancer of the project has.
func checkName(tx store.Tx, projectID int64, name string, exceptID int64) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
		return &api.FieldError{Field: "name", Message: "name must not be empty or start or end with white space"}
	}
	if utf8.RuneCountInString(name) > maxName {
		return &api.FieldError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxName)}
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

// checkAlgorithm refuses an unknown algorithm.
func checkAlgorithm(field, a string) error {
	if a != algoRoundRobin && a != algoLeastConn {
		return &api.FieldError{Field: field, Message: "algorithm must be round_robin or least_connections"}
	}
	return nil
}

var listOptions = api.ListOptions[LoadBalancer]{
	Name:   func(l LoadBalancer) string { return l.Name },
	Labels: func(l LoadBalancer) map[string]string { return l.Labels },
	Sort: map[string]func(a, b LoadBalancer) int{
		"id":      func(a, b LoadBalancer) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b LoadBalancer) int { return cmp.Compare(a.Name, b.Name) },
		"created": func(a, b LoadBalancer) int { return a.Created.Compare(b.Created) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []LoadBalancer
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		if err != nil {
			return err
		}
		out = make([]LoadBalancer, 0, len(recs))
		for _, r := range recs {
			l, err := m.show(tx, r)
			if err != nil {
				return err
			}
			out = append(out, l)
		}
		return nil
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
	var l LoadBalancer
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		l, err = m.show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"load_balancer": l})
}

// update changes only the name and labels present in the body; labels,
// when present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name   *string            `json:"name"`
		Labels *map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var l LoadBalancer
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
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
		if err := put(tx, r); err != nil {
			return err
		}
		l, err = m.show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"load_balancer": l})
}
