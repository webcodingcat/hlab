// Package servers serves Hetzner's servers of a project. Handlers write the
// desired state and start an action; the VM work runs in the action on
// the platform (resource.Reconciler).
package servers

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/ipam"
	"github.com/webcodingcat/hlab/internal/metrics"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resource"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of servers.
const Kind = "servers"

// refType is the resource type of a server in actions.
const refType = "server"

// primaryIPKind is the ID sequence of primary IPs; a server's public
// addresses take their IDs from it so later primary IPs do not collide.
const primaryIPKind = "primary_ips"

// Server status values used in this phase.
const (
	StatusInitializing = "initializing"
	StatusStarting     = "starting"
	StatusRunning      = "running"
	StatusOff          = "off"
	StatusStopping     = "stopping"
	StatusDeleting     = "deleting"
	StatusRebuilding   = "rebuilding"
	StatusMigrating    = "migrating"
)

// Images provides the base disk of an image by name, the catalog ISOs and
// the rescue base (*images.Store).
type Images interface {
	Ensure(ctx context.Context, name string) (string, error)
	EnsureISO(ctx context.Context, name string) (string, error)
	EnsureRescue(ctx context.Context) (string, error)
}

// DiskUse is a further thin disk the disk rule counts: it can grow from its
// file size up to NominalBytes.
type DiskUse struct {
	Path         string
	NominalBytes int64
}

// VolumeHooks are the volume operations of a server's life; the cloud sets
// them after both modules exist.
type VolumeHooks interface {
	// CheckAttach validates the volume IDs of a server create (exist, same
	// project and location, not attached) with api field errors.
	CheckAttach(tx store.Tx, projectID int64, location string, ids []int64) error
	Attach(ctx context.Context, stor *store.Store, serverID int64, ids []int64, automount bool) error
	// DetachAll detaches every volume of a server being deleted; the
	// volume files stay.
	DetachAll(ctx context.Context, stor *store.Store, serverID int64) error
}

// Settings are the settings the module reads.
type Settings struct {
	DataFolder    string
	StopBehaviour string // "save" | "shutdown"
	ReserveRAMMB  int64
	ReserveDiskGB int64
	VMPrefix      string // profile tag: "hlab" | "hlab-dev"
}

// Deps are what the module needs.
type Deps struct {
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	// Store is what Reconcile and Delete work on outside a request
	// (project deletion); the handlers use the store of the request.
	Store    *store.Store
	Platform platform.Platform
	Images   Images
	// PushAgent rebuilds and pushes the agent document.
	PushAgent func(ctx context.Context) error
	Settings  Settings
	// AgentStatus reads the agent's status (reset_password).
	AgentStatus func(ctx context.Context) (agentapi.Status, error)
	// ResetTimeout bounds the wait for a password reset; 0 = 120 s.
	ResetTimeout time.Duration
	// Clock is the module's source of time; nil = scheduler.RealClock().
	Clock scheduler.Clock
	// Metrics keeps the samples of the servers' VMs.
	Metrics *metrics.Store
	// ExtraDisks lists further thin disks for the disk rule (volumes); nil
	// = none.
	ExtraDisks func(tx store.Tx) ([]DiskUse, error)
	// RescueSeedHold is how long the rescue seed stays in the drive after
	// hlab started the VM into rescue, so the rescue system reads it before
	// the server's seed returns; 0 = none.
	RescueSeedHold time.Duration
	// HaltTimeout bounds the wait for the guests to shut down at cloud
	// stop before their VMs are turned off hard; 0 = 60 s.
	HaltTimeout time.Duration
}

// Server is a server in Hetzner's API shape.
type Server struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	Created         time.Time         `json:"created"`
	PublicNet       PublicNet         `json:"public_net"`
	PrivateNet      []PrivateNet      `json:"private_net"`
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
	// Firewalls is filled from the firewall hook when a server is shown.
	Firewalls []ServerFirewall `json:"firewalls"`
}

// IPv4 is the public IPv4 of a server. ID is the ID of its primary IP,
// which clients such as hcloud-python read. A stored empty DNSPtr is shown
// as rdns.Default.
type IPv4 struct {
	ID      int64  `json:"id,omitempty"`
	IP      string `json:"ip"`
	Blocked bool   `json:"blocked"`
	DNSPtr  string `json:"dns_ptr"`
}

// IPv6 is the formal public IPv6 network of a server; ID as for IPv4.
type IPv6 struct {
	ID      int64        `json:"id,omitempty"`
	IP      string       `json:"ip"`
	Blocked bool         `json:"blocked"`
	DNSPtr  []rdns.Entry `json:"dns_ptr"`
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
	// RootHash is the SHA-512 crypt of the root password of a server
	// created without SSH keys; the clear text is never stored.
	RootHash string `json:"root_hash,omitempty"`
	// PasswordReset is the pending root password reset of reset_password,
	// served to the guest by the agent's metadata service.
	PasswordReset *agentapi.PasswordReset `json:"password_reset,omitempty"`
	// ImageID is the project image the server was created or rebuilt
	// from; Image is then empty.
	ImageID int64 `json:"image_id,omitempty"`
	// AutomountVolumes were attached at create with automount.
	AutomountVolumes []int64 `json:"automount_volumes,omitempty"`
	Rescue           *Rescue `json:"rescue,omitempty"`
	// ISO is the cache name of the attached ISO (a catalog ISO's name or
	// an own ISO's `_hlab.name`).
	ISO            string `json:"iso,omitempty"`
	BackupsEnabled bool   `json:"backups_enabled,omitempty"`
	// InstanceID is the NoCloud instance-id of the seed, set by each
	// rebuild to `<id>-<action id>` so cloud-init sees a new instance;
	// empty means the server ID.
	InstanceID string `json:"instance_id,omitempty"`
	// Networks holds per attached network ID what adapter and seed need;
	// the range cannot change while servers are attached.
	Networks map[int64]NetInternal `json:"networks,omitempty"`
	// Adapters are the MACs of the private adapters the VM has, so a
	// detach knows which adapter to remove.
	Adapters []string `json:"adapters,omitempty"`
	// NetBefore is the network state before the network action running on
	// the server: the API shows its private_net until the action succeeds,
	// and a failed or interrupted action restores it; nil when no such
	// action runs.
	NetBefore *NetState `json:"net_before,omitempty"`
}

// Rescue is the rescue state of a server.
type Rescue struct {
	Enabled bool `json:"enabled"` // rescue_enabled
	// Since is the enable time; rescue is disabled after 60 min without a
	// boot into it.
	Since time.Time `json:"since"`
	// Booted is set when hlab started the VM into rescue.
	Booted bool `json:"booted"`
	// DiskPath is the LUN 63 child disk, removed at the next hlab-driven
	// boot or when the VM is seen off.
	DiskPath string   `json:"disk_path"`
	RootHash string   `json:"root_hash"`
	SSHKeys  []string `json:"ssh_keys"`
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
type Module struct {
	d       Deps
	volumes VolumeHooks
	acts    *actions.Manager // set at mount (UseActions); the backup job starts actions with it
	// publicIPs and networks are the create hooks; nil = none.
	publicIPs PublicIPs
	networks  Networks
	firewalls Firewalls
	dnsPtrs   DNSPtrs
	// loadBalancers lists the load balancers targeting a server; nil = none.
	loadBalancers func(tx store.Tx, serverID int64) ([]int64, error)
	// onDelete run inside the transaction that deletes a server record.
	onDelete []func(tx store.Tx, serverID int64) error

	mu sync.Mutex
	// changes counts the status changes actions made per server, so the
	// status watch skips a server whose state it read before one.
	changes map[int64]uint64
}

// New returns the module.
func New(d Deps) *Module {
	if d.Clock == nil {
		d.Clock = scheduler.RealClock()
	}
	return &Module{d: d}
}

// SetVolumes sets the volume hooks.
func (m *Module) SetVolumes(h VolumeHooks) { m.volumes = h }

// SetLoadBalancers sets the hook that fills the load_balancers field of
// the server view.
func (m *Module) SetLoadBalancers(f func(tx store.Tx, serverID int64) ([]int64, error)) {
	m.loadBalancers = f
}

// SetVolumes sets the volumes field of server serverID.
func SetVolumes(tx store.Tx, serverID int64, ids []int64) error {
	r, err := Load(tx, serverID)
	if err != nil {
		return err
	}
	r.Volumes = append([]int64{}, ids...)
	return put(tx, r)
}

// Type implements resource.Module.
func (m *Module) Type() string { return "servers" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /servers", http.HandlerFunc(m.list))
	r.Handle("POST /servers", http.HandlerFunc(m.create))
	r.Handle("GET /servers/{id}", http.HandlerFunc(m.get))
	r.Handle("PUT /servers/{id}", http.HandlerFunc(m.update))
	r.Handle("DELETE /servers/{id}", http.HandlerFunc(m.delete))
	r.Handle("GET /servers/{id}/metrics", http.HandlerFunc(m.getMetrics))
	r.Handle("POST /servers/{id}/actions/change_protection", http.HandlerFunc(m.changeProtection))
	r.Handle("POST /servers/{id}/actions/poweron", http.HandlerFunc(m.poweron))
	r.Handle("POST /servers/{id}/actions/poweroff", http.HandlerFunc(m.poweroff))
	r.Handle("POST /servers/{id}/actions/shutdown", http.HandlerFunc(m.shutdown))
	r.Handle("POST /servers/{id}/actions/reboot", http.HandlerFunc(m.reboot))
	r.Handle("POST /servers/{id}/actions/reset", http.HandlerFunc(m.reset))
	r.Handle("POST /servers/{id}/actions/rebuild", http.HandlerFunc(m.rebuild))
	r.Handle("POST /servers/{id}/actions/change_type", http.HandlerFunc(m.changeType))
	r.Handle("POST /servers/{id}/actions/reset_password", http.HandlerFunc(m.resetPassword))
	r.Handle("POST /servers/{id}/actions/request_console", http.HandlerFunc(m.requestConsole))
	r.Handle("POST /servers/{id}/actions/add_to_placement_group", http.HandlerFunc(m.addToPlacementGroup))
	r.Handle("POST /servers/{id}/actions/remove_from_placement_group", http.HandlerFunc(m.removeFromPlacementGroup))
	r.Handle("POST /servers/{id}/actions/create_image", http.HandlerFunc(m.createImage))
	r.Handle("POST /servers/{id}/actions/enable_backup", http.HandlerFunc(m.enableBackup))
	r.Handle("POST /servers/{id}/actions/disable_backup", http.HandlerFunc(m.disableBackup))
	r.Handle("POST /servers/{id}/actions/enable_rescue", http.HandlerFunc(m.enableRescue))
	r.Handle("POST /servers/{id}/actions/disable_rescue", http.HandlerFunc(m.disableRescue))
	r.Handle("POST /servers/{id}/actions/attach_iso", http.HandlerFunc(m.attachISO))
	r.Handle("POST /servers/{id}/actions/detach_iso", http.HandlerFunc(m.detachISO))
	r.Handle("POST /servers/{id}/actions/change_dns_ptr", http.HandlerFunc(m.changeDNSPtr))
	resactions.RoutesFor(r, "servers", refType)
}

// powerOn marks server id starting and starts its VM.
func (m *Module) powerOn(ctx context.Context, stor *store.Store, id int64) error {
	if err := stor.Tx(func(tx store.Tx) error { return SetStatus(tx, id, StatusStarting) }); err != nil {
		return err
	}
	return m.d.Platform.Power(ctx, m.vmName(id), platform.PowerStart)
}

// Contribute implements resource.NetContributor: every server not being
// deleted, with its public IPv4 and MAC, and the metadata of each such
// server with a public IPv4.
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error {
	recs, err := store.ListAll[Record](tx, Kind)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Status == StatusDeleting {
			continue
		}
		ip := r.IPv4()
		doc.Servers = append(doc.Servers, agentapi.Server{
			ID:         r.ID,
			Location:   r.Hlab.Location,
			MAC:        r.Hlab.MAC,
			PublicIPv4: ip,
		})
		if !ip.IsValid() {
			continue
		}
		md, err := m.Metadata(tx, r)
		if err != nil {
			return err
		}
		md.IP = ip
		doc.Metadata = append(doc.Metadata, md)
	}
	return nil
}

// Metadata is the metadata entry of server r without segment and source
// address; the caller sets VLAN and IP.
func (m *Module) Metadata(tx store.Tx, r Record) (agentapi.Metadata, error) {
	loc, _ := m.d.Catalog.Location(r.Hlab.Location)
	reset, err := runningReset(tx, r.Hlab.PasswordReset)
	if err != nil {
		return agentapi.Metadata{}, err
	}
	md := agentapi.Metadata{
		ServerID:         r.ID,
		Hostname:         r.Name,
		Region:           loc.NetworkZone,
		AvailabilityZone: availabilityZone(r.Hlab.Location),
		PublicKeys:       r.Hlab.SSHKeys,
		UserData:         r.Hlab.UserData,
		PasswordReset:    reset,
	}
	if ip := r.IPv4(); ip.IsValid() {
		md.PublicIPv4 = ip.String()
	}
	return md, nil
}

// runningReset returns reset while its reset_password action runs; a reset
// whose action ended (a restart ends it with server_error) is stale.
func runningReset(tx store.Tx, reset *agentapi.PasswordReset) (*agentapi.PasswordReset, error) {
	if reset == nil {
		return nil, nil
	}
	a, err := store.GetAction(tx, reset.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if a.Status != actions.StatusRunning {
		return nil, nil
	}
	return reset, nil
}

// datacenterNumbers are the numbers of Hetzner's datacenters per location
// (fsn1-dc14 ...); the datacenter catalog itself left the API.
var datacenterNumbers = map[string]int{"fsn1": 14, "nbg1": 3, "hel1": 2, "ash": 1, "hil": 1, "sin": 1}

// availabilityZone is the metadata availability zone of a location,
// Hetzner's datacenter name `<location>-dc<n>`; an unknown location gets 1.
func availabilityZone(location string) string {
	n, ok := datacenterNumbers[location]
	if !ok {
		n = 1
	}
	return location + "-dc" + strconv.Itoa(n)
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

// show returns the API shape of r with its placement group and firewalls.
func (m *Module) show(tx store.Tx, r Record) (Server, error) {
	g, ok, err := placementgroups.Of(tx, r.Hlab.ProjectID, r.ID)
	if err != nil {
		return Server{}, err
	}
	s := r.Server
	s.PublicNet = showPublic(s.PublicNet)
	s.PrivateNet = r.shownPrivateNet()
	if ok {
		s.PlacementGroup = &g
	}
	if s.PublicNet.Firewalls, err = m.firewallsOf(tx, r); err != nil {
		return Server{}, err
	}
	if m.loadBalancers != nil {
		lbs, err := m.loadBalancers(tx, r.ID)
		if err != nil {
			return Server{}, err
		}
		s.LoadBalancers = append([]int64{}, lbs...)
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
			s.PublicNet = showPublic(s.PublicNet)
			s.PrivateNet = r.shownPrivateNet()
			if g, ok := members[r.ID]; ok {
				s.PlacementGroup = &g
			}
			if s.PublicNet.Firewalls, err = m.firewallsOf(tx, r); err != nil {
				return err
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
		s, err = m.show(tx, r)
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
	Volumes          []int64           `json:"volumes"`
	Automount        *bool             `json:"automount"`
	PublicNet        *struct {
		EnableIPv4 *bool  `json:"enable_ipv4"`
		EnableIPv6 *bool  `json:"enable_ipv6"`
		IPv4       *int64 `json:"ipv4"`
		IPv6       *int64 `json:"ipv6"`
	} `json:"public_net"`
	Networks  []int64       `json:"networks"`
	Firewalls []firewallRef `json:"firewalls"`
}

// publicNetRequest is the public_net of b with Hetzner's defaults.
func (b createRequest) publicNetRequest() PublicNetRequest {
	req := PublicNetRequest{EnableIPv4: true, EnableIPv6: true}
	if pn := b.PublicNet; pn != nil {
		if pn.EnableIPv4 != nil {
			req.EnableIPv4 = *pn.EnableIPv4
		}
		if pn.EnableIPv6 != nil {
			req.EnableIPv6 = *pn.EnableIPv6
		}
		if pn.IPv4 != nil {
			req.IPv4 = *pn.IPv4
		}
		if pn.IPv6 != nil {
			req.IPv6 = *pn.IPv6
		}
	}
	return req
}

var errNoAddress = errors.New("no free public IPv4 address")

func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body createRequest
	if !api.Decode(w, req, &body) {
		return
	}
	typ, loc, ierr := m.resolve(body)
	if ierr != nil {
		api.Fail(w, ierr)
		return
	}
	pubReq := body.publicNetRequest()
	if !pubReq.EnableIPv4 && !pubReq.EnableIPv6 && len(body.Networks) == 0 {
		api.Fail(w, &api.FieldError{Field: "public_net", Message: "a server needs a public IPv4, a public IPv6 or a network"})
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	acts := api.ActionsFrom(req.Context())
	labels := body.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	start := body.StartAfterCreate == nil || *body.StartAfterCreate
	var automountVolumes []int64
	if body.Automount != nil && *body.Automount {
		automountVolumes = body.Volumes
	}
	stor := api.StoreFrom(req.Context())
	var img source
	if err := stor.Tx(func(tx store.Tx) error {
		var err error
		if img, err = m.findImage(tx, p.ID, refString(body.Image), false); err != nil {
			return err
		}
		return img.fits(typ.Disk, "server_type")
	}); err != nil {
		api.Fail(w, err)
		return
	}
	// A server created without starting holds no RAM until poweron.
	var ramMB int64
	if start {
		ramMB = int64(typ.Memory * 1024)
	}
	if err := m.checkResources(req.Context(), stor, typ.Cores, ramMB, int64(typ.Disk)<<30); err != nil {
		api.Fail(w, err)
		return
	}

	// Without SSH keys root gets a random password; only its hash is kept.
	var rootPassword any
	var rootHash string
	if len(body.SSHKeys) == 0 {
		pw, hash, err := newRootPassword()
		if err != nil {
			api.ServerError(w, err)
			return
		}
		rootPassword, rootHash = pw, hash
	}

	var rec Record
	var shown Server
	var action *actions.Action
	next := []*actions.Action{}
	err := stor.Tx(func(tx store.Tx) error {
		if err := checkName(tx, p.ID, body.Name, 0); err != nil {
			return err
		}
		if err := api.CheckLabels(labels); err != nil {
			return err
		}
		if err := m.checkVolumes(tx, p.ID, loc.Name, body.Volumes); err != nil {
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
		publicNet, err := m.allocatePublic(tx, p.ID, id, loc.Name, pubReq)
		if err != nil {
			return err
		}
		privateNet, nets, err := m.attachAtCreate(tx, p.ID, id, loc.Name, body.Networks)
		if err != nil {
			return err
		}
		rec = Record{
			Server: Server{
				ID:              id,
				Name:            body.Name,
				Status:          StatusInitializing,
				Created:         m.d.Clock.Now().UTC().Truncate(time.Second),
				PublicNet:       publicNet,
				PrivateNet:      privateNet,
				ServerType:      typ.Raw,
				Location:        loc.Raw,
				Image:           img.raw,
				Labels:          labels,
				Volumes:         []int64{},
				LoadBalancers:   []int64{},
				PrimaryDiskSize: typ.Disk,
			},
			Hlab: Internal{
				ProjectID:        p.ID,
				ServerType:       typ.Name,
				Image:            img.name,
				ImageID:          img.imageID,
				Location:         loc.Name,
				MAC:              MAC(id).String(),
				SSHKeys:          keys,
				UserData:         body.UserData,
				StartAfterCreate: start,
				RootHash:         rootHash,
				AutomountVolumes: automountVolumes,
				Networks:         nets,
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
		if action, err = acts.Start(tx, p.ID, "create_server", []actions.Ref{{ID: id, Type: refType}}); err != nil {
			return err
		}
		fwActions, err := m.attachFirewalls(tx, acts, rec, body.Firewalls)
		if err != nil {
			return err
		}
		next = append(next, fwActions...)
		shown, err = m.show(tx, rec)
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

	id := rec.ID
	acts.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		progress(10)
		// The new server is stored now, so its disk counts as growth.
		err := m.checkResources(ctx, stor, typ.Cores, ramMB, 0)
		if err == nil {
			err = m.reconcile(ctx, stor, id)
		}
		if err == nil && len(body.Volumes) > 0 {
			err = m.volumes.Attach(ctx, stor, id, body.Volumes, len(automountVolumes) > 0)
		}
		final := StatusOff
		if err == nil && start {
			err = m.powerOn(ctx, stor, id)
			if err == nil {
				final = StatusRunning
			}
		}
		if err == nil {
			progress(90)
		}
		if serr := m.setStatus(stor, id, final); serr != nil && err == nil {
			err = serr
		}
		return err
	})
	api.JSON(w, http.StatusCreated, map[string]any{
		"server":        shown,
		"action":        action,
		"next_actions":  next,
		"root_password": rootPassword,
	})
}

// allocatePublic returns the public_net of new server id: from the
// PublicIPs hook, or without it an IPv4 from ipam and IDs from the
// primary_ips sequence.
func (m *Module) allocatePublic(tx store.Tx, projectID, id int64, location string, req PublicNetRequest) (PublicNet, error) {
	var pn PublicNet
	if m.publicIPs != nil {
		var err error
		if pn, err = m.publicIPs.Allocate(tx, projectID, id, location, req); err != nil {
			return PublicNet{}, err
		}
	} else {
		if req.EnableIPv4 {
			ip, err := ipam.NextPublic(tx, m.d.PublicRange)
			if errors.Is(err, ipam.ErrFull) {
				return PublicNet{}, errNoAddress
			}
			if err != nil {
				return PublicNet{}, err
			}
			ipID, err := store.NextID(tx, primaryIPKind)
			if err != nil {
				return PublicNet{}, err
			}
			pn.IPv4 = &IPv4{ID: ipID, IP: ip.String(), DNSPtr: ""}
		}
		if req.EnableIPv6 {
			ipID, err := store.NextID(tx, primaryIPKind)
			if err != nil {
				return PublicNet{}, err
			}
			pn.IPv6 = &IPv6{ID: ipID, IP: IPv6Net(id).String(), DNSPtr: []rdns.Entry{}}
		}
	}
	if pn.FloatingIPs == nil {
		pn.FloatingIPs = []int64{}
	}
	if pn.Firewalls == nil {
		pn.Firewalls = []ServerFirewall{}
	}
	return pn, nil
}

// attachAtCreate allocates the networks of new server id through the
// Networks hook; without networks the server has none.
func (m *Module) attachAtCreate(tx store.Tx, projectID, id int64, location string, ids []int64) ([]PrivateNet, map[int64]NetInternal, error) {
	if len(ids) == 0 {
		return []PrivateNet{}, nil, nil
	}
	if m.networks == nil {
		return nil, nil, &api.FieldError{Field: "networks", Message: "networks are not available"}
	}
	pn, nets, err := m.networks.AttachAtCreate(tx, projectID, id, location, ids)
	if err != nil {
		return nil, nil, err
	}
	if pn == nil {
		pn = []PrivateNet{}
	}
	return pn, nets, nil
}

// checkVolumes validates the volumes of a create request in location; a
// create without volumes needs no hooks.
func (m *Module) checkVolumes(tx store.Tx, projectID int64, location string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return m.volumes.CheckAttach(tx, projectID, location, ids)
}

// resolve finds server type and location of a create request.
func (m *Module) resolve(b createRequest) (catalog.ServerType, catalog.Location, *api.FieldError) {
	var none catalog.ServerType
	typ, ok := m.serverType(refString(b.ServerType))
	if !ok {
		return none, catalog.Location{}, &api.FieldError{Field: "server_type", Message: "server type " + refString(b.ServerType) + " not found"}
	}
	if typ.Architecture == "arm" {
		return none, catalog.Location{}, &api.FieldError{Code: "resource_unavailable", Message: "server type " + typ.Name + " is an arm type; HLab runs only x86 servers"}
	}
	ref := refString(b.Location)
	if ref == "" && len(typ.Locations) > 0 {
		ref = typ.Locations[0].Name
	}
	loc, ok := m.location(ref)
	if !ok {
		return none, catalog.Location{}, &api.FieldError{Field: "location", Message: "location " + ref + " not found"}
	}
	if !slices.ContainsFunc(typ.Locations, func(l catalog.ServerTypeLocation) bool { return l.Name == loc.Name }) {
		return none, catalog.Location{}, &api.FieldError{Field: "location", Message: "server type " + typ.Name + " is not offered in " + loc.Name}
	}
	return typ, loc, nil
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
	var relabelled bool
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
			relabelled = !maps.Equal(r.Labels, *body.Labels)
			r.Labels = *body.Labels
		}
		if err := put(tx, r); err != nil {
			return err
		}
		s, err = m.show(tx, r)
		return err
	})
	if err != nil {
		api.Fail(w, err)
		return
	}
	// Labels decide which label selectors of firewalls reach the server.
	if relabelled {
		if err := m.d.PushAgent(req.Context()); err != nil {
			slog.Warn("push agent document after server labels changed", "id", id, "err", err)
		}
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
		return m.remove(ctx, stor, id)
	})
	api.JSON(w, http.StatusOK, map[string]any{"action": action})
}

// Delete implements resource.Deleter for project deletion: it deletes the
// server's VM and disk, releases its IPv4, removes it from its placement
// group and deletes its row. A server already gone is no error.
func (m *Module) Delete(ctx context.Context, id int64) error {
	return m.remove(ctx, m.d.Store, id)
}

// remove marks server id as deleting (the agent document leaves it out),
// detaches its volumes, deletes its VM and then its row with its address,
// group membership and backups (with their files), running the OnDelete
// functions in that transaction; snapshots stay. When
// the VM cannot be deleted, the server stays with status off.
func (m *Module) remove(ctx context.Context, stor *store.Store, id int64) error {
	err := stor.Tx(func(tx store.Tx) error { return SetStatus(tx, id, StatusDeleting) })
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.volumes != nil {
		err = m.volumes.DetachAll(ctx, stor, id)
	}
	if err == nil {
		err = m.deleteMachine(ctx, id)
	}
	if err != nil {
		if serr := stor.Tx(func(tx store.Tx) error { return SetStatus(tx, id, StatusOff) }); serr != nil {
			err = errors.Join(err, serr)
		}
		return err
	}
	m.d.Metrics.Forget(id)
	var backups []imagesres.Record
	if err := stor.Tx(func(tx store.Tx) error {
		r, err := Load(tx, id)
		if err != nil {
			return err
		}
		// With the PublicIPs hook the primary IPs own the address and
		// release it in their OnDelete function.
		if a := r.IPv4(); a.IsValid() && m.publicIPs == nil {
			if err := ipam.Release(tx, a); err != nil {
				return err
			}
		}
		for _, f := range m.onDelete {
			if err := f(tx, id); err != nil {
				return err
			}
		}
		if err := leaveGroup(tx, r); err != nil {
			return err
		}
		if backups, err = dropBackups(tx, id); err != nil {
			return err
		}
		return store.Delete(tx, Kind, r.Hlab.ProjectID, id)
	}); err != nil {
		return err
	}
	removeImageFiles(backups)
	return nil
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
