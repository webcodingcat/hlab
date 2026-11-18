// Package servers serves Hetzner's servers of a project. Handlers write the
// desired state and start an action; the VM work runs in the action
// through Machines.
package servers

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
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
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of servers.
const Kind = "servers"

// refType is the resource type of a server in actions.
const refType = "server"

// Server status values used in this phase.
const (
	StatusInitializing = "initializing"
	StatusStarting     = "starting"
	StatusRunning      = "running"
	StatusOff          = "off"
	StatusDeleting     = "deleting"
)

// Machines creates and deletes the VM of a server. Both run inside an
// action and read what they need from the store by the server ID.
type Machines interface {
	// Create creates the VM with its disk and seed and starts it when the
	// server asks for it.
	Create(ctx context.Context, id int64) error
	// Delete removes the VM and its files; a missing VM is no error.
	Delete(ctx context.Context, id int64) error
}

// Deps are what the module needs.
type Deps struct {
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	Machines    Machines
}

// Server is a server in Hetzner's API shape.
type Server struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	Created         time.Time         `json:"created"`
	PublicNet       PublicNet         `json:"public_net"`
	PrivateNet      []any             `json:"private_net"`
	ServerType      json.RawMessage   `json:"server_type"`
	Location        json.RawMessage   `json:"location"`
	Image           json.RawMessage   `json:"image"`
	ISO             json.RawMessage   `json:"iso"`
	RescueEnabled   bool              `json:"rescue_enabled"`
	Locked          bool              `json:"locked"`
	BackupWindow    *string           `json:"backup_window"`
	OutgoingTraffic int64             `json:"outgoing_traffic"`
	IngoingTraffic  int64             `json:"ingoing_traffic"`
	IncludedTraffic int64             `json:"included_traffic"`
	Protection      Protection        `json:"protection"`
	Labels          map[string]string `json:"labels"`
	Volumes         []int64           `json:"volumes"`
	LoadBalancers   []int64           `json:"load_balancers"`
	PrimaryDiskSize int               `json:"primary_disk_size"`
	// PlacementGroup is filled from the groups when a server is shown;
	// the groups alone record membership.
	PlacementGroup *placementgroups.Group `json:"placement_group"`
}

// PublicNet is the public network part of a server.
type PublicNet struct {
	IPv4        *IPv4   `json:"ipv4"`
	IPv6        *IPv6   `json:"ipv6"`
	FloatingIPs []int64 `json:"floating_ips"`
	Firewalls   []any   `json:"firewalls"`
}

// IPv4 is the public IPv4 of a server.
type IPv4 struct {
	IP      string `json:"ip"`
	Blocked bool   `json:"blocked"`
	DNSPtr  string `json:"dns_ptr"`
}

// IPv6 is the formal public IPv6 network of a server.
type IPv6 struct {
	IP      string `json:"ip"`
	Blocked bool   `json:"blocked"`
	DNSPtr  []any  `json:"dns_ptr"`
}

// Protection is the protection of a server.
type Protection struct {
	Delete  bool `json:"delete"`
	Rebuild bool `json:"rebuild"`
}

// Internal are the fields hlab keeps beside the API shape (`_hlab`).
type Internal struct {
	ProjectID        int64    `json:"project_id"`
	ServerType       string   `json:"server_type"`
	Image            string   `json:"image"`
	Location         string   `json:"location"`
	MAC              string   `json:"mac"`
	SSHKeys          []string `json:"ssh_keys"`
	UserData         string   `json:"user_data"`
	StartAfterCreate bool     `json:"start_after_create"`
}

// Record is the stored document of a server.
type Record struct {
	Server
	Hlab Internal `json:"_hlab"`
}

// IPv4 returns the server's public IPv4, or an invalid address.
func (r Record) IPv4() netip.Addr {
	if r.PublicNet.IPv4 == nil {
		return netip.Addr{}
	}
	a, _ := netip.ParseAddr(r.PublicNet.IPv4.IP)
	return a
}

// Module is the servers resource type.
type Module struct{ d Deps }

// New returns the module.
func New(d Deps) *Module { return &Module{d: d} }

// Type implements resource.Module.
func (m *Module) Type() string { return "servers" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /servers", http.HandlerFunc(m.list))
	r.Handle("POST /servers", http.HandlerFunc(m.create))
	r.Handle("GET /servers/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /servers/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /servers/{id}", http.HandlerFunc(m.delete))
	r.Handle("POST /servers/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /servers/{id}/actions/request_console", http.HandlerFunc(m.requestConsole))
	r.Handle("POST /servers/{id}/actions/add_to_placement_group", http.HandlerFunc(m.addToPlacementGroup))
	r.Handle("POST /servers/{id}/actions/remove_from_placement_group", http.HandlerFunc(m.removeFromPlacementGroup))
	resactions.RoutesFor(r, "servers", refType)
}

// Contribute implements resource.NetContributor: every server not being
// deleted, with its public IPv4 and MAC.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Status == StatusDeleting {
			continue
		}
		doc.Servers = append(doc.Servers, agentapi.Server{
			ID:         r.ID,
			Location:   r.Hlab.Location,
			MAC:        r.Hlab.MAC,
			PublicIPv4: r.IPv4(),
		})
	}
	return nil
}

// Load returns the server id of any project, or store.ErrNotFound.
func Load(tx store.Tx, id int64) (Record, error) {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return Record{}, err
	}
	for _, r := range recs {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, store.ErrNotFound
}

// SetStatus changes the status of server id.
func SetStatus(tx store.Tx, id int64, status string) error {
	r, err := Load(tx, id)
	if err != nil {
		return err
	}
	r.Status = status
	return put(tx, r)
}

func put(tx store.Tx, r Record) error {
	return store.Put(tx, store.Meta{
		Kind: Kind, ID: r.ID, ProjectID: r.Hlab.ProjectID, Name: r.Name, Labels: r.Labels, Created: r.Created,
	}, r)
}

// show returns the API shape of r with its placement group.
func show(tx store.Tx, r Record) (Server, error) {
	g, ok, err := placementgroups.Of(tx, r.Hlab.ProjectID, r.ID)
	if err != nil {
		return Server{}, err
	}
	s := r.Server
	if ok {
		s.PlacementGroup = &g
	}
	return s, nil
}

// checkName refuses an empty name and one another server of the project has.
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

var listOptions = api.ListOptions[Server]{
	Name:   func(s Server) string { return s.Name },
	Labels: func(s Server) map[string]string { return s.Labels },
	Sort: map[string]func(a, b Server) int{
		"id":      func(a, b Server) int { return cmp.Compare(a.ID, b.ID) },
		"name":    func(a, b Server) int { return strings.Compare(a.Name, b.Name) },
		"created": func(a, b Server) int { return a.Created.Compare(b.Created) },
	},
	Filters: map[string]func(Server, []string) bool{
		"status": func(s Server, v []string) bool { return slices.Contains(v, s.Status) },
	},
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	var out []Server
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		recs, err := store.List[Record](tx, Kind, p.ID)
		if err != nil {
			return err
		}
		members, err := placementgroups.Members(tx, p.ID)
		if err != nil {
			return err
		}
		out = make([]Server, 0, len(recs))
		for _, r := range recs {
			s := r.Server
			if g, ok := members[r.ID]; ok {
				s.PlacementGroup = &g
			}
			out = append(out, s)
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
	var s Server
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		s, err = show(tx, r)
		return err
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		api.NotFound(w)
	case err != nil:
		api.ServerError(w, err)
	default:
		api.JSON(w, http.StatusOK, map[string]any{"server": s})
	}
}

// createRequest is the part of POST /servers this phase uses.
type createRequest struct {
	Name             string            `json:"name"`
	ServerType       json.RawMessage   `json:"server_type"`
	Image            json.RawMessage   `json:"image"`
	Location         json.RawMessage   `json:"location"`
	SSHKeys          []json.RawMessage `json:"ssh_keys"`
	UserData         string            `json:"user_data"`
	StartAfterCreate *bool             `json:"start_after_create"`
	Labels           map[string]string `json:"labels"`
	PlacementGroup   json.RawMessage   `json:"placement_group"`
}

var errNoAddress = errors.New("no free public IPv4 address")

func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	typ, img, loc, ierr := m.resolve(body)
	if ierr != nil {
		api.Fail(w, ierr)
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	labels := body.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	start := body.StartAfterCreate == nil || *body.StartAfterCreate

	var rec Record
	var shown Server
	var action *actions.Action
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(labels); err != nil {
			return err
		}
		var group *placementgroups.Group
		if ref := refString(body.PlacementGroup); ref != "" {
			g, err := placementgroups.Find(tx, p.ID, ref)
			if errors.Is(err, store.ErrNotFound) {
				return &api.FieldError{Field: "placement_group", Message: "placement group " + ref + " not found"}
			}
			if err != nil {
				return err
			}
			group = &g
		}
		var keys []string
		for _, raw := range body.SSHKeys {
			k, err := sshkeys.Find(tx, p.ID, refString(raw))
			if errors.Is(err, store.ErrNotFound) {
				return &api.FieldError{Field: "ssh_keys", Message: "ssh key " + refString(raw) + " not found"}
			}
			if err != nil {
				return err
			}
			keys = append(keys, k.PublicKey)
		}
		id, err := store.NextID(tx, Kind)
		if err != nil {
			return err
		}
		ip, err := ipam.NextPublic(tx, m.d.PublicRange)
		if errors.Is(err, ipam.ErrFull) {
			return errNoAddress
		}
		if err != nil {
			return err
		}
		rec = Record{
			Server: Server{
				ID:      id,
				Name:    body.Name,
				Status:  StatusInitializing,
				Created: time.Now().UTC().Truncate(time.Second),
				PublicNet: PublicNet{
					IPv4:        &IPv4{IP: ip.String(), DNSPtr: ""},
					IPv6:        &IPv6{IP: IPv6Net(id).String(), DNSPtr: []any{}},
					FloatingIPs: []int64{},
					Firewalls:   []any{},
				},
				PrivateNet:      []any{},
				ServerType:      typ.Raw,
				Location:        loc.Raw,
				Image:           img.Raw,
				Labels:          labels,
				Volumes:         []int64{},
				LoadBalancers:   []int64{},
				PrimaryDiskSize: typ.Disk,
			},
			Hlab: Internal{
				ProjectID:        p.ID,
				ServerType:       typ.Name,
				Image:            img.Name,
				Location:         loc.Name,
				MAC:              MAC(id).String(),
				SSHKeys:          keys,
				UserData:         body.UserData,
				StartAfterCreate: start,
			},
		}
		if err := put(tx, rec); err != nil {
			return err
		}
		if group != nil {
			if err := placementgroups.AddServer(tx, p.ID, group.ID, id); err != nil {
				return err
			}
		}
		if shown, err = show(tx, rec); err != nil {
			return err
		}
		action, err = acts.Start(tx, p.ID, "create_server", []actions.Ref{{ID: id, Type: refType}})
		return err
	})
	switch {
	case errors.Is(err, errNoAddress):
		api.Error(w, "resource_unavailable", "no free public IPv4 address in the public range", nil)
		return
	case err != nil:
		api.Fail(w, err)
		return
	}

	stor := api.StoreFrom(req.Context())
	id := rec.ID
	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		err := m.d.Machines.Create(ctx, id)
		final := StatusOff
		if err == nil && start {
			final = StatusRunning
		}
		if err == nil {
			progress(90)
		}
		if serr := stor.Tx(func(tx store.Tx) error { return SetStatus(tx, id, final) }); serr != nil && err == nil {
			err = serr
		}
		return err
	})
	api.JSON(w, http.StatusCreated, map[string]any{
		"server":        shown,
		"action":        action,
		"next_actions":  []any{},
		"root_password": nil,
	})
}

// resolve finds server type, image and location of a create request.
func (m *Module) resolve(b createRequest) (catalog.ServerType, catalog.Image, catalog.Location, *api.FieldError) {
	var none catalog.ServerType
	typ, ok := m.serverType(refString(b.ServerType))
	if !ok {
		return none, catalog.Image{}, catalog.Location{}, &api.FieldError{Field: "server_type", Message: "server type " + refString(b.ServerType) + " not found"}
	}
	if typ.Architecture == "arm" {
		return none, catalog.Image{}, catalog.Location{}, &api.FieldError{Code: "resource_unavailable", Message: "server type " + typ.Name + " is an arm type; hlab runs only x86 servers"}
	}
	img, ok := m.image(refString(b.Image))
	if !ok {
		return none, catalog.Image{}, catalog.Location{}, &api.FieldError{Field: "image", Message: "image " + refString(b.Image) + " not found"}
	}
	ref := refString(b.Location)
	if ref == "" && len(typ.Locations) > 0 {
		ref = typ.Locations[0].Name
	}
	loc, ok := m.location(ref)
	if !ok {
		return none, catalog.Image{}, catalog.Location{}, &api.FieldError{Field: "location", Message: "location " + ref + " not found"}
	}
	if !slices.ContainsFunc(typ.Locations, func(l catalog.ServerTypeLocation) bool { return l.Name == loc.Name }) {
		return none, catalog.Image{}, catalog.Location{}, &api.FieldError{Field: "location", Message: "server type " + typ.Name + " is not offered in " + loc.Name}
	}
	return typ, img, loc, nil
}

func (m *Module) serverType(ref string) (catalog.ServerType, bool) {
	return m.d.Catalog.ServerTypeByRef(ref)
}

func (m *Module) image(ref string) (catalog.Image, bool) {
	if img, ok := m.d.Catalog.Image(ref); ok {
		return img, true
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	for _, img := range m.d.Catalog.Images {
		if err == nil && img.ID == id {
			return img, true
		}
	}
	return catalog.Image{}, false
}

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

// update changes only the fields present in the body; labels, when
// present, replace the whole set.
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
	var s Server
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
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
		s, err = show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]any{"server": s})
}

func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	stor := api.StoreFrom(req.Context())
	var action *actions.Action
	err := stor.Tx(func(tx store.Tx) error {
		r, err := store.Get[Record](tx, Kind, p.ID, id)
		if err != nil {
			return err
		}
		if r.Protection.Delete {
			return actions.ErrProtected
		}
		action, err = acts.Start(tx, p.ID, "delete_server", []actions.Ref{{ID: id, Type: refType}})
		if err != nil {
			return err
		}
		r.Status = StatusDeleting
		return put(tx, r)
	})
	if err != nil {
		api.Fail(w, err)
		return
	}

	acts.Run(action.ID, func(ctx context.Context, _ func(int)) error {
		if err := m.d.Machines.Delete(ctx, id); err != nil {
			if serr := stor.Tx(func(tx store.Tx) error { return SetStatus(tx, id, StatusOff) }); serr != nil {
				err = errors.Join(err, serr)
			}
			return err
		}
		return stor.Tx(func(tx store.Tx) error {
			r, err := Load(tx, id)
			if err != nil {
				return err
			}
			if a := r.IPv4(); a.IsValid() {
				if err := ipam.Release(tx, a); err != nil {
					return err
				}
			}
			if err := leaveGroup(tx, r); err != nil {
				return err
			}
			return store.Delete(tx, Kind, r.Hlab.ProjectID, id)
		})
	})
	api.JSON(w, http.StatusOK, map[string]any{"action": action})
}

// MAC is the public adapter's MAC of server id: 02:48:4c, then 0x1 and the
// low 20 bits of the ID (02:48:4c:00:00:01 is the appliance's).
func MAC(id int64) net.HardwareAddr {
	v := uint32(id)&0xfffff | 0x100000
	return net.HardwareAddr{0x02, 0x48, 0x4c, byte(v >> 16), byte(v >> 8), byte(v)}
}

// IPv6Net is the formal IPv6 /64 of server id in the documentation range:
// 2001:db8:<id as 32 bits>::/64.
func IPv6Net(id int64) netip.Prefix {
	var b [16]byte
	b[0], b[1], b[2], b[3] = 0x20, 0x01, 0x0d, 0xb8
	binary.BigEndian.PutUint32(b[4:8], uint32(id))
	return netip.PrefixFrom(netip.AddrFrom16(b), 64)
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
