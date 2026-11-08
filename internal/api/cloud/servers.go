package cloud

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's texts about servers (measured 2026-11-08).
const (
	serverNameInvalid     = "name must be a valid hostname"
	serverNameTaken       = "server name is already used"
	serverDeleteProtected = "server deletion is protected"
	serverNoRoom          = "error during placement"
	serverBusy            = "server is locked by another action"
)

// userDataMax is the most user data a server takes, in bytes (measured
// 2026-11-08: "Length must be between 0 and 32768.").
const userDataMax = 32768

// hostname is a valid host name as Hetzner takes a server's name: labels
// of letters, digits and dashes, joined by dots (RFC 1123).
var hostname = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// validHostname tells whether a name is a valid host name.
func validHostname(name string) bool {
	return len(name) <= 253 && hostname.MatchString(name)
}

// idOrName is a reference by ID (a number) or by name (a string), as the
// create request takes server types, images and SSH keys.
type idOrName struct {
	ID   int64
	Name string
}

// UnmarshalJSON takes a number or a string.
func (r *idOrName) UnmarshalJSON(data []byte) error {
	if bytes.HasPrefix(data, []byte(`"`)) {
		return json.Unmarshal(data, &r.Name)
	}
	return json.Unmarshal(data, &r.ID)
}

// matches tells whether the reference names an entry of that ID and name.
func (r idOrName) matches(id int64, name string) bool {
	if r.Name != "" {
		return r.Name == name
	}
	return r.ID == id
}

// serverCreateRequest is the body of POST /servers. It is HLab's own type
// because schema.ServerCreateRequest cannot tell a missing field from an
// empty one. Volumes, networks, firewalls and placement groups come with
// their phases; until then the lab ignores them.
type serverCreateRequest struct {
	Name             *string                 `json:"name"`
	ServerType       *idOrName               `json:"server_type"`
	Image            *idOrName               `json:"image"`
	Location         *string                 `json:"location"`
	SSHKeys          []idOrName              `json:"ssh_keys"`
	UserData         *string                 `json:"user_data"`
	StartAfterCreate *bool                   `json:"start_after_create"`
	Labels           *map[string]string      `json:"labels"`
	PublicNet        *serverPublicNetRequest `json:"public_net"`
}

// serverPublicNetRequest is the public network a new server asks for:
// Primary IPs by ID, or new ones unless disabled.
type serverPublicNetRequest struct {
	EnableIPv4 *bool  `json:"enable_ipv4"`
	EnableIPv6 *bool  `json:"enable_ipv6"`
	IPv4       *int64 `json:"ipv4"`
	IPv6       *int64 `json:"ipv6"`
}

// serverUpdateRequest is the body of PUT /servers/{id}; a missing field
// stays as it is.
type serverUpdateRequest struct {
	Name   *string            `json:"name"`
	Labels *map[string]string `json:"labels"`
}

// serverAnswer is a server as Hetzner's API sends it, with its fields in
// the real API's order (measured 2026-11-07). It is HLab's own type because
// schema.Server cannot send a missing IPv4 or IPv6 as null.
type serverAnswer struct {
	ID              int64                     `json:"id"`
	Name            string                    `json:"name"`
	Status          string                    `json:"status"`
	ServerType      serverTypeAnswer          `json:"server_type"`
	Location        schema.Location           `json:"location"`
	Image           *imageAnswer              `json:"image"`
	ISO             *schema.ISO               `json:"iso"`
	PrimaryDiskSize int                       `json:"primary_disk_size"`
	Labels          map[string]string         `json:"labels"`
	Protection      schema.ServerProtection   `json:"protection"`
	BackupWindow    *string                   `json:"backup_window"`
	RescueEnabled   bool                      `json:"rescue_enabled"`
	Locked          bool                      `json:"locked"` // Hetzner's administrative lock: always false
	PlacementGroup  *schema.PlacementGroup    `json:"placement_group"`
	PublicNet       serverPublicNetAnswer     `json:"public_net"`
	PrivateNet      []schema.ServerPrivateNet `json:"private_net"`
	LoadBalancers   []int64                   `json:"load_balancers"`
	Volumes         []int64                   `json:"volumes"`
	IncludedTraffic uint64                    `json:"included_traffic"`
	IngoingTraffic  uint64                    `json:"ingoing_traffic"`
	OutgoingTraffic uint64                    `json:"outgoing_traffic"`
	Created         time.Time                 `json:"created"`
}

// serverPublicNetAnswer is a server's public network; a missing address
// is null.
type serverPublicNetAnswer struct {
	Firewalls   []schema.ServerFirewall     `json:"firewalls"`
	FloatingIPs []int64                     `json:"floating_ips"`
	IPv4        *schema.ServerPublicNetIPv4 `json:"ipv4"`
	IPv6        *schema.ServerPublicNetIPv6 `json:"ipv6"`
}

// serverSortKeys are the sort values of GET /servers.
var serverSortKeys = web.SortKeys[serverAnswer]{
	"id":      func(a, b serverAnswer) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b serverAnswer) int { return cmp.Compare(a.Name, b.Name) },
	"created": func(a, b serverAnswer) int { return a.Created.Compare(b.Created) },
}

// serverStatuses are the values of the status filter (the spec's enum).
var serverStatuses = []string{"running", "initializing", "starting", "stopping", "off", "deleting", "migrating", "rebuilding", "unknown"}

// registerServers adds the routes of the servers and their actions.
func registerServers(api *web.API, l *lab.Lab) {
	table := serverActions(l)
	api.Handle("GET /servers", func(req *web.Request) (int, any, error) {
		query := req.URL.Query()
		for _, status := range query["status"] {
			if !slices.Contains(serverStatuses, status) {
				return 0, nil, web.InvalidValue("status")
			}
		}
		selector, err := web.ParseSelector(query.Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		statuses, filtered := query["status"]
		var servers []serverAnswer
		_ = l.View(func(tx *state.Tx) error {
			for _, server := range state.All(tx, state.Servers) {
				if server.ProjectID == req.Caller.ProjectID && selector.Matches(server.Labels) &&
					(!filtered || slices.Contains(statuses, string(server.Status))) {
					servers = append(servers, serverJSON(tx, l.Catalog(), server))
				}
			}
			return nil
		})
		servers = byName(req, servers, func(server serverAnswer) string { return server.Name })
		if err := web.Sort(req, servers, serverSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "servers", servers)
	})

	api.Handle("POST /servers", func(req *web.Request) (int, any, error) {
		return createServer(req, l, table)
	})

	api.Handle("GET /servers/{id}", func(req *web.Request) (int, any, error) {
		var out serverAnswer
		err := l.View(func(tx *state.Tx) error {
			server, err := callerServer(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err == nil {
				out = serverJSON(tx, l.Catalog(), server)
			}
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]serverAnswer{"server": out}, nil
	})

	api.Handle("PUT /servers/{id}", func(req *web.Request) (int, any, error) {
		var body serverUpdateRequest
		if err := web.DecodeJSON(req.Request, &body); err != nil {
			return 0, nil, err
		}
		check := web.Check{Update: true}
		if body.Name != nil && !validHostname(*body.Name) {
			check.Fail("name", serverNameInvalid)
		}
		if body.Labels != nil {
			check.Labels(*body.Labels)
		}
		if err := check.Err(); err != nil {
			return 0, nil, err
		}
		var out serverAnswer
		err := l.Change(func(tx *state.Tx) error {
			server, err := callerServer(tx, req.Caller.ProjectID, web.PathID(req, "id"))
			if err != nil {
				return err
			}
			if body.Name != nil && *body.Name != server.Name {
				if _, taken := state.ByName(tx, state.Servers, server.ProjectID, *body.Name); taken {
					return web.Refusal(http.StatusConflict, "uniqueness_error", serverNameTaken)
				}
				server.Name = *body.Name
			}
			if body.Labels != nil {
				server.Labels = *body.Labels
			}
			out = serverJSON(tx, l.Catalog(), server)
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		if body.Name != nil {
			// the metadata's hostname, also if the client goes away; if
			// sending fails, the appliance's watchdog sends the newest
			// pictures again. The seed keeps the old name: cloud-init reads
			// it only at the first boot.
			_ = l.UpdatePictures(context.WithoutCancel(req.Context()))
		}
		return http.StatusOK, map[string]serverAnswer{"server": out}, nil
	})

	// Hetzner answers a delete with 200 and its action, and the server is
	// gone at once (measured 2026-11-08)
	api.Handle("DELETE /servers/{id}", func(req *web.Request) (int, any, error) {
		action, err := act(l, table, "delete", req)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]schema.Action{"action": actionJSON(&action)}, nil
	})

	for _, command := range []string{"poweron", "poweroff", "shutdown", "reboot", "reset", "change_protection"} {
		api.Handle("POST /servers/{id}/actions/"+command, func(req *web.Request) (int, any, error) {
			action, err := act(l, table, command, req)
			if err != nil {
				return 0, nil, err
			}
			return http.StatusCreated, map[string]schema.Action{"action": actionJSON(&action)}, nil
		})
	}
	registerResourceActions(api, l, "servers", lab.ServerResource)
}

// createServer checks a create request, records the server with its
// Primary IPs and starts its create action, and its start unless
// start_after_create is false, all in one transaction.
func createServer(req *web.Request, l *lab.Lab, table lab.ActionTable) (int, any, error) {
	var body serverCreateRequest
	if err := web.DecodeJSON(req.Request, &body); err != nil {
		return 0, nil, err
	}
	check := web.Check{Singular: true}
	check.Required("name", body.Name != nil)
	if body.Name != nil && !validHostname(*body.Name) {
		check.Fail("name", serverNameInvalid)
	}
	check.Required("server_type", body.ServerType != nil)
	check.Required("image", body.Image != nil)
	if body.UserData != nil {
		check.Length("user_data", *body.UserData, 0, userDataMax)
	}
	if body.Labels != nil {
		check.Labels(*body.Labels)
	}
	if err := check.Err(); err != nil {
		return 0, nil, err
	}
	cat := l.Catalog()
	serverType, found := findServerType(cat, *body.ServerType)
	if !found {
		return 0, nil, web.NotFound("server type")
	}
	location, found := serverLocation(cat, serverType, body.Location)
	if !found {
		// Hetzner still names the datacenter here (measured 2026-11-08)
		return 0, nil, web.NotFound("datacenter")
	}
	image, found := findImage(cat, *body.Image)
	if !found {
		return 0, nil, web.Refusal(http.StatusUnprocessableEntity, "invalid_input", "image not found")
	}
	start := body.StartAfterCreate == nil || *body.StartAfterCreate
	server := &state.Server{
		ProjectID:   req.Caller.ProjectID,
		Name:        *body.Name,
		Labels:      map[string]string{},
		Created:     l.Now(),
		Type:        serverType.Name,
		Location:    location.Name,
		ImageID:     image.ID,
		Status:      state.ServerInitializing,
		WantRunning: start,
		DiskGB:      serverType.Disk,
	}
	if body.Labels != nil {
		server.Labels = *body.Labels
	}
	if body.UserData != nil {
		server.UserData = *body.UserData
	}
	if err := l.CheckDisk(req.Context()); err != nil {
		return 0, nil, actError(err)
	}
	commands := []string{"create"}
	if start {
		commands = append(commands, "poweron")
	}
	var out serverAnswer
	actions, err := l.Create(table, lab.Request{ProjectID: server.ProjectID}, func(tx *state.Tx) (int64, error) {
		keys, err := serverKeys(tx, server.ProjectID, body.SSHKeys)
		if err != nil {
			return 0, err
		}
		server.PublicKeys = keys
		if len(keys) == 0 {
			server.RootPassword = rootPassword() // shown once, as on Hetzner
		}
		if _, taken := state.ByName(tx, state.Servers, server.ProjectID, server.Name); taken {
			return 0, web.Refusal(http.StatusConflict, "uniqueness_error", serverNameTaken)
		}
		if start {
			if err := l.CheckRoom(tx, 0, serverType); err != nil {
				return 0, err
			}
		}
		server.ID = state.NewID(tx, state.Servers)
		server.MAC = lab.ServerMAC(server.ID)
		if err := assignNewServerIPs(tx, server, body.PublicNet); err != nil {
			return 0, err
		}
		state.Put(tx, state.Servers, server)
		out = serverJSON(tx, cat, server)
		return server.ID, nil
	}, commands...)
	if err != nil {
		return 0, nil, actError(err)
	}
	next := []schema.Action{}
	for i := 1; i < len(actions); i++ {
		next = append(next, actionJSON(&actions[i]))
	}
	answer := struct {
		Server       serverAnswer    `json:"server"`
		Action       schema.Action   `json:"action"`
		NextActions  []schema.Action `json:"next_actions"`
		RootPassword *string         `json:"root_password"`
	}{Server: out, Action: actionJSON(&actions[0]), NextActions: next}
	if server.RootPassword != "" {
		answer.RootPassword = &server.RootPassword
	}
	return http.StatusCreated, answer, nil
}

// findServerType returns the server type a reference names.
func findServerType(cat *catalog.Catalog, ref idOrName) (catalog.ServerType, bool) {
	for _, serverType := range cat.ServerTypes {
		if ref.matches(serverType.ID, serverType.Name) {
			return serverType, true
		}
	}
	return catalog.ServerType{}, false
}

// findImage returns the system image a reference names.
func findImage(cat *catalog.Catalog, ref idOrName) (catalog.Image, bool) {
	for _, image := range cat.Images {
		if ref.matches(image.ID, image.Name) {
			return image, true
		}
	}
	return catalog.Image{}, false
}

// serverLocation returns the location a new server asks for: by name or
// ID, or without one the first location of its type, where Hetzner picks
// one with room.
func serverLocation(cat *catalog.Catalog, serverType catalog.ServerType, name *string) (catalog.Location, bool) {
	if name == nil {
		if len(serverType.Locations) == 0 {
			return catalog.Location{}, false
		}
		return cat.LocationByName(serverType.Locations[0].Name)
	}
	for _, location := range cat.Locations {
		if location.Name == *name || strconv.FormatInt(location.ID, 10) == *name {
			return location, true
		}
	}
	return catalog.Location{}, false
}

// serverKeys returns the public keys of the SSH keys a request names, by
// ID or name, in the caller's project.
func serverKeys(tx *state.Tx, projectID int64, refs []idOrName) ([]string, error) {
	keys := []string{}
	for _, ref := range refs {
		key, found := state.Find(tx, state.SSHKeys, func(key *state.SSHKey) bool {
			return key.ProjectID == projectID && ref.matches(key.ID, key.Name)
		})
		if !found {
			return nil, web.NotFound("SSH key")
		}
		keys = append(keys, key.PublicKey)
	}
	return keys, nil
}

// rootPassword returns a new root password: 20 letters and digits, each
// as likely as the others.
func rootPassword() string {
	const characters = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	password := make([]byte, 20)
	for i := range password {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(characters))))
		password[i] = characters[n.Int64()]
	}
	return string(password)
}

// callerServer returns the server of an ID if it belongs to the project;
// another project's server is not found, as on Hetzner.
func callerServer(tx *state.Tx, projectID, id int64) (*state.Server, error) {
	server, err := state.Get(tx, state.Servers, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && server.ProjectID != projectID) {
		return nil, web.NotFound("server")
	}
	return server, err
}

// serverJSON returns a server as the API shows it.
func serverJSON(tx *state.Tx, cat *catalog.Catalog, server *state.Server) serverAnswer {
	out := serverAnswer{
		ID:              server.ID,
		Name:            server.Name,
		Status:          string(server.Status),
		PrimaryDiskSize: server.DiskGB,
		Labels:          server.Labels,
		Protection:      schema.ServerProtection{Delete: server.Protection.Delete, Rebuild: server.Protection.Rebuild},
		PublicNet:       serverPublicNetAnswer{Firewalls: []schema.ServerFirewall{}, FloatingIPs: []int64{}},
		PrivateNet:      []schema.ServerPrivateNet{},
		LoadBalancers:   []int64{},
		Volumes:         []int64{},
		Created:         server.Created,
	}
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	if serverType, found := cat.ServerTypeByName(server.Type); found {
		out.ServerType = serverTypeJSON(cat, serverType)
		// a new server shows no traffic yet (measured 2026-11-08)
		for _, price := range serverType.Prices {
			if price.Location == server.Location && server.Status != state.ServerInitializing {
				out.IncludedTraffic = price.IncludedTraffic
			}
		}
	}
	if location, found := cat.LocationByName(server.Location); found {
		out.Location = locationJSON(location)
	}
	if image, found := cat.ImageByID(server.ImageID); found {
		answer := imageJSON(image)
		out.Image = &answer
	}
	if ip, err := state.Get(tx, state.PrimaryIPs, server.IPv4); err == nil {
		out.PublicNet.IPv4 = &schema.ServerPublicNetIPv4{ID: ip.ID, IP: ip.IP, DNSPtr: dnsPtr(ip)}
	}
	if ip, err := state.Get(tx, state.PrimaryIPs, server.IPv6); err == nil {
		out.PublicNet.IPv6 = &schema.ServerPublicNetIPv6{ID: ip.ID, IP: ip.IP, DNSPtr: []schema.ServerPublicNetIPv6DNSPtr{}}
	}
	return out
}
