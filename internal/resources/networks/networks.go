// Package networks serves Hetzner's private networks of a project. Each
// network has a VLAN ID (2..4092, allocated in kv) and, on the appliance,
// a VRF whose gateway is the first host address of the network range.
package networks

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of networks.
const Kind = "networks"

// refType is the resource type of a network in actions.
const refType = "network"

// VLAN IDs a network can get.
const (
	minVLAN = 2
	maxVLAN = 4092
)

// Deps are what the module needs.
type Deps struct {
	// Store is what Delete works on outside a request (project deletion);
	// the handlers use the store of the request.
	Store   *store.Store
	Catalog *catalog.Catalog
	Servers *servers.Module
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
}

// Subnet is a subnet of a network in Hetzner's API shape.
type Subnet struct {
	Type        string `json:"type"` // "cloud" | "server"
	IPRange     string `json:"ip_range"`
	NetworkZone string `json:"network_zone"`
	Gateway     string `json:"gateway"` // the network gateway (C1)
	VSwitchID   *int64 `json:"vswitch_id"`
}

// Route is a route of a network.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}

// Protection is the protection of a network.
type Protection struct {
	Delete bool `json:"delete"`
}

// Network is a network in Hetzner's API shape.
type Network struct {
	ID                    int64             `json:"id"`
	Name                  string            `json:"name"`
	IPRange               string            `json:"ip_range"`
	Subnets               []Subnet          `json:"subnets"`
	Routes                []Route           `json:"routes"`
	Servers               []int64           `json:"servers"`
	LoadBalancers         []int64           `json:"load_balancers"`
	Protection            Protection        `json:"protection"`
	Labels                map[string]string `json:"labels"`
	Created               time.Time         `json:"created"`
	ExposeRoutesToVSwitch bool              `json:"expose_routes_to_vswitch"`
}

// Record is the stored document of a network.
type Record struct {
	Network // Hetzner's network object; `servers` and `load_balancers` are filled when shown
	Hlab    struct {
		ProjectID int64 `json:"project_id"`
		VLAN      int   `json:"vlan"`
		// Shown are the subnets and routes the API shows while a subnet or
		// route action runs, whose change shows at its success; nil shows
		// the record's own.
		Shown *shownParts `json:"shown,omitempty"`
	} `json:"_hlab"`
}

// shownParts are the subnets and routes the API shows of a network.
type shownParts struct {
	Subnets []Subnet `json:"subnets"`
	Routes  []Route  `json:"routes"`
}

// Module is the networks resource type.
type Module struct {
	d   Deps
	lbs LoadBalancerMembers // set by SetLoadBalancers; nil: no LBs
}

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return Kind }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /networks", http.HandlerFunc(m.list))
	r.Handle("POST /networks", http.HandlerFunc(m.create))
	r.Handle("GET /networks/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /networks/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /networks/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /networks/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /networks/{id}/actions/add_subnet", http.HandlerFunc(m.addSubnet))
	r.Handle("POST /networks/{id}/actions/delete_subnet", http.HandlerFunc(m.deleteSubnet))
	r.Handle("POST /networks/{id}/actions/add_route", http.HandlerFunc(m.addRoute))
	r.Handle("POST /networks/{id}/actions/delete_route", http.HandlerFunc(m.deleteRoute))
	r.Handle("POST /networks/{id}/actions/change_ip_range", http.HandlerFunc(m.changeIPRange))
	m.serverRoutes(r)
	resactions.RoutesFor(r, Kind, refType)
}

// Load returns network id of projectID, or store.ErrNotFound.
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

// view is the network as the API shows it.
func view(r Record) Network {
	n := r.Network
	if r.Hlab.Shown != nil {
		n.Subnets, n.Routes = r.Hlab.Shown.Subnets, r.Hlab.Shown.Routes
	}
	if n.Subnets == nil {
		n.Subnets = []Subnet{}
	}
	if n.Routes == nil {
		n.Routes = []Route{}
	}
	if n.Servers == nil {
		n.Servers = []int64{}
	}
	if n.LoadBalancers == nil {
		n.LoadBalancers = []int64{}
	}
	return n
}

// serversByNetwork returns per network ID of projectID the IDs of its
// members, read from the servers' private_net.
func serversByNetwork(tx store.Tx, projectID int64) (map[int64][]int64, error) {
	docs, err := store.List[memberDoc](tx, servers.Kind, projectID)
	if err != nil {
		return nil, err
	}
	out := map[int64][]int64{}
	for _, d := range docs {
		for _, p := range d.shown() {
			out[p.Network] = append(out[p.Network], d.ID)
		}
	}
	return out, nil
}

// show is r as the API shows it, with its server and LB members.
func (m *Module) show(tx store.Tx, r Record) (Network, error) {
	by, err := serversByNetwork(tx, r.Hlab.ProjectID)
	if err != nil {
		return Network{}, err
	}
	r.Servers = by[r.ID]
	if r.LoadBalancers, err = m.lbIDs(tx, r.Hlab.ProjectID, r.ID); err != nil {
		return Network{}, err
	}
	return view(r), nil
}

// vlanKey is the kv key of VLAN n; its value is the network ID.
func vlanKey(n int) string { return "vlan." + strconv.Itoa(n) }

// allocVLAN takes the first free VLAN ID for network id.
func allocVLAN(tx store.Tx, id int64) (int, error) {
	for n := minVLAN; n <= maxVLAN; n++ {
		_, err := store.GetKV(tx, vlanKey(n))
		if errors.Is(err, store.ErrNotFound) {
			return n, store.SetKV(tx, vlanKey(n), strconv.FormatInt(id, 10))
		}
		if err != nil {
			return 0, err
		}
	}
	return 0, &api.FieldError{Code: "resource_limit_exceeded", Message: "no free VLAN ID left for a new network"}
}

// remove detaches the LBs of r, deletes its record and releases its VLAN.
func (m *Module) remove(tx store.Tx, r Record) error {
	if m.lbs != nil {
		if err := m.lbs.NetworkDeleted(tx, r.ID); err != nil {
			return err
		}
	}
	if err := store.Delete(tx, Kind, r.Hlab.ProjectID, r.ID); err != nil {
		return err
	}
	return store.DeleteKV(tx, vlanKey(r.Hlab.VLAN))
}

// push sends the agent document; a failure is logged, the state is
// already stored and the next push carries it.
func (m *Module) push(ctx context.Context) error {
	return m.d.PushAgent(ctx)
}

func (m *Module) pushLogged(ctx context.Context) {
	if err := m.push(ctx); err != nil {
		slog.Warn("push agent document after a network change", "err", err)
	}
}

// checkName refuses an empty name and one another network of the project has.
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

var listOptions = api.ListOptions[Network]{
	Name:   func(n Network) string { return n.Name },
	Labels: func(n Network) map[string]string { return n.Labels },
	Sort: map[string]func(a, b Network) int{
		"id":      func(a, b Network) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Network) int { return strings.Compare(a.Name, b.Name) },
		"created": func(a, b Network) int { return a.Created.Compare(b.Created) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []Network
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		if err != nil {
			return err
		}
		by, err := serversByNetwork(tx, p.ID)
		if err != nil {
			return err
		}
		out = make([]Network, 0, len(recs))
		for _, r := range recs {
			r.Servers = by[r.ID]
			if r.LoadBalancers, err = m.lbIDs(tx, p.ID, r.ID); err != nil {
				return err
			}
			out = append(out, view(r))
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
	var n Network
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		n, err = m.show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"network": n})
}

// createRequest is POST /networks.
type createRequest struct {
	Name                  string            `json:"name"`
	IPRange               string            `json:"ip_range"`
	Subnets               []subnetRequest   `json:"subnets"`
	Routes                []Route           `json:"routes"`
	Labels                map[string]string `json:"labels"`
	ExposeRoutesToVSwitch bool              `json:"expose_routes_to_vswitch"`
}

// create stores a network with its subnets and routes. Creating a network
// needs no work, so Hetzner answers without an action.
func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	p, _ := api.ProjectFrom(req.Context())
	var r Record
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(body.Labels); err != nil {
			return err
		}
		rng, err := checkRange(body.IPRange, "ip_range")
		if err != nil {
			return err
		}
		subnets := []Subnet{}
		for i, s := range body.Subnets {
			sub, err := m.checkSubnet(rng, s, subnets, nil, "subnets["+strconv.Itoa(i)+"].")
			if err != nil {
				return err
			}
			subnets = append(subnets, sub)
		}
		routes := []Route{}
		for i, rt := range body.Routes {
			route, err := checkRoute(rng, rt, subnets, routes, "routes["+strconv.Itoa(i)+"].")
			if err != nil {
				return err
			}
			routes = append(routes, route)
		}
		r.Network = Network{
			Name:                  body.Name,
			IPRange:               rng.String(),
			Subnets:               subnets,
			Routes:                routes,
			Labels:                body.Labels,
			Created:               time.Now().UTC().Truncate(time.Second),
			ExposeRoutesToVSwitch: body.ExposeRoutesToVSwitch,
		}
		r.Hlab.ProjectID = p.ID
		if r.ID, err = store.NextID(tx, Kind); err != nil {
			return err
		}
		if r.Hlab.VLAN, err = allocVLAN(tx, r.ID); err != nil {
			return err
		}
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.pushLogged(req.Context())
	api.JSON(w, http.StatusCreated, map[string]any{"network": view(r)})
}

// update changes only the fields present in the body; labels, when
// present, replace the whole set.
func (m *Module) update(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	var body struct {
		Name                  *string            `json:"name"`
		Labels                *map[string]string `json:"labels"`
		ExposeRoutesToVSwitch *bool              `json:"expose_routes_to_vswitch"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var n Network
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
		if body.ExposeRoutesToVSwitch != nil {
			r.ExposeRoutesToVSwitch = *body.ExposeRoutesToVSwitch
		}
		if err := put(tx, r); err != nil {
			return err
		}
		n, err = m.show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"network": n})
}

// delete removes a network without delete protection and releases its
// VLAN; its servers are detached first (records, then their adapters).
func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var detached []int64
	err := stor.Tx(func(tx store.Tx) error {
		r, err := Load(tx, p.ID, id)
		if err != nil {
			return err
		}
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		if detached, err = detachAll(tx, r); err != nil {
			return err
		}
		return m.remove(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	m.syncDetached(req.Context(), stor, id, detached)
	m.pushLogged(req.Context())
	w.WriteHeader(http.StatusNoContent)
}

// Delete implements resource.Deleter for project deletion: it removes
// network id of any project and releases its VLAN. A network already gone
// is no error.
func (m *Module) Delete(_ context.Context, id int64) error {
	return m.d.Store.Tx(func(tx store.Tx) error {
		recs, err := store.ListAll[Record](tx, Kind)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.ID == id {
				return m.remove(tx, r)
			}
		}
		return nil
	})
}

// Gateway is the first host address of the network range ipRange (C1).
func Gateway(ipRange netip.Prefix) netip.Addr {
	return ipRange.Masked().Addr().Next()
}

// publicGateway is the guests' public gateway, never a private address.
var publicGateway = netip.MustParseAddr("172.31.1.1")

// NextIP returns the lowest free host address of subnet, skipping the
// subnet's network and broadcast address, gw and 172.31.1.1.
func NextIP(subnet netip.Prefix, gw netip.Addr, used map[netip.Addr]bool) (netip.Addr, bool) {
	subnet = subnet.Masked()
	for a := subnet.Addr().Next(); a.IsValid() && subnet.Contains(a); a = a.Next() {
		if !subnet.Contains(a.Next()) {
			break // a is the broadcast address
		}
		if a == gw || a == publicGateway || used[a] {
			continue
		}
		return a, true
	}
	return netip.Addr{}, false
}
