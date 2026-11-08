package state

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Project is a project of the lab, as on Hetzner: it holds resources, and its
// tokens reach only them.
type Project struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Limits   Limits    `json:"limits"`
	Internet bool      `json:"internet"` // whether its servers reach the Internet
}

// RecordID returns the project's ID.
func (p Project) RecordID() int64 { return p.ID }

// Limits are a project's resource limits, as Hetzner sets them per project.
// Zero means no limit: a field added later loads from older state files as
// zero, so it needs no migration.
type Limits struct {
	Servers       int `json:"servers"`
	Cores         int `json:"cores"`
	PrimaryIPs    int `json:"primary_ips"`
	FloatingIPs   int `json:"floating_ips"`
	VolumeGB      int `json:"volume_gb"`
	Networks      int `json:"networks"`
	LoadBalancers int `json:"load_balancers"`
}

// Permission is what a token may do.
type Permission string

// The permissions of a token, as on Hetzner.
const (
	PermissionRead      Permission = "read"
	PermissionReadWrite Permission = "read_write"
)

// Token is an API token of a project. Only its hash is kept: the token itself
// is shown once, when it is created.
type Token struct {
	ID         int64      `json:"id"`
	ProjectID  int64      `json:"project_id"`
	Name       string     `json:"name"`
	Created    time.Time  `json:"created"`
	Permission Permission `json:"permission"`
	Hash       string     `json:"hash"`   // SHA-256 of the token, hex
	Prefix     string     `json:"prefix"` // its first characters, to tell tokens apart
}

// RecordID returns the token's ID.
func (t Token) RecordID() int64 { return t.ID }

// RecordProject returns the token's project.
func (t Token) RecordProject() int64 { return t.ProjectID }

// RecordName implements Named.
func (t Token) RecordName() string { return t.Name }

// HashToken returns what the state keeps of a token: its SHA-256, hex.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SSHKey is an SSH key of a project.
type SSHKey struct {
	ID          int64             `json:"id"`
	ProjectID   int64             `json:"project_id"`
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Created     time.Time         `json:"created"`
	Fingerprint string            `json:"fingerprint"` // MD5, unique in the project
	PublicKey   string            `json:"public_key"`
}

// RecordID returns the key's ID.
func (k SSHKey) RecordID() int64 { return k.ID }

// RecordProject returns the key's project.
func (k SSHKey) RecordProject() int64 { return k.ProjectID }

// RecordName implements Named.
func (k SSHKey) RecordName() string { return k.Name }

// ActionStatus is how far an action is.
type ActionStatus string

// The statuses of an action, as on Hetzner.
const (
	ActionRunning ActionStatus = "running"
	ActionSuccess ActionStatus = "success"
	ActionError   ActionStatus = "error"
)

// Action is an action of a project: the API returns one for every change, and
// clients poll it while they wait.
type Action struct {
	ID        int64         `json:"id"`
	ProjectID int64         `json:"project_id"`
	Command   string        `json:"command"` // Hetzner's name, such as "stop_server"
	Status    ActionStatus  `json:"status"`
	Progress  int           `json:"progress"`
	Started   time.Time     `json:"started"`
	Finished  *time.Time    `json:"finished"`
	Error     *Failure      `json:"error"`
	Resources []ResourceRef `json:"resources"`
}

// RecordID returns the action's ID.
func (a Action) RecordID() int64 { return a.ID }

// RecordProject returns the action's project.
func (a Action) RecordProject() int64 { return a.ProjectID }

// Failure is why an action failed: Hetzner's "error" of an action.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResourceRef names a resource by its type, as actions name it ("server"), and
// its ID.
type ResourceRef struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

// ServerStatus is a server's status, as Hetzner shows it.
type ServerStatus string

// The statuses of a server that the lab uses, as on Hetzner.
const (
	ServerInitializing ServerStatus = "initializing"
	ServerStarting     ServerStatus = "starting"
	ServerRunning      ServerStatus = "running"
	ServerStopping     ServerStatus = "stopping"
	ServerOff          ServerStatus = "off"
	ServerDeleting     ServerStatus = "deleting"
)

// Protection is a resource's protection against delete (and, for servers,
// rebuild).
type Protection struct {
	Delete  bool `json:"delete"`
	Rebuild bool `json:"rebuild"`
}

// Server is a server of a project: its record holds what was asked for
// (WantRunning, its addresses); the VM is the system's.
type Server struct {
	ID         int64             `json:"id"`
	ProjectID  int64             `json:"project_id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	Created    time.Time         `json:"created"`
	Protection Protection        `json:"protection"`
	Type       string            `json:"type"`     // the server type's name, such as "cpx22"
	Location   string            `json:"location"` // the location's name
	ImageID    int64             `json:"image_id"` // the image it was created from
	Status     ServerStatus      `json:"status"`
	// WantRunning is whether the server should run: a start of the lab
	// starts it, a guest that shuts down clears it.
	WantRunning bool `json:"want_running"`
	// IPv4 and IPv6 are its Primary IPs, 0 for none.
	IPv4 int64  `json:"ipv4"`
	IPv6 int64  `json:"ipv6"`
	MAC  string `json:"mac"` // of its public NIC
	// PublicKeys are the SSH keys it was created with; RootPassword is set
	// for a server created without keys, as on Hetzner, and the guest gets
	// it through the seed.
	PublicKeys   []string `json:"public_keys"`
	RootPassword string   `json:"root_password"`
	UserData     string   `json:"user_data"`
	DiskGB       int      `json:"disk_gb"`
	// BaseImage is the cached system image its disk differs from.
	BaseImage string `json:"base_image"`
	// PrivateNets are its NICs in private networks, in the order they were
	// attached.
	PrivateNets []ServerPrivateNet `json:"private_nets"`
}

// ServerPrivateNet is a server's NIC in a private network: its address,
// its alias IPs and the NIC's MAC address.
type ServerPrivateNet struct {
	NetworkID int64    `json:"network_id"`
	IP        string   `json:"ip"`
	AliasIPs  []string `json:"alias_ips"`
	MAC       string   `json:"mac"`
}

// RecordID returns the server's ID.
func (s Server) RecordID() int64 { return s.ID }

// RecordProject returns the server's project.
func (s Server) RecordProject() int64 { return s.ProjectID }

// RecordName implements Named.
func (s Server) RecordName() string { return s.Name }

// IPType is the address family of a Primary or Floating IP.
type IPType string

// The types of Primary and Floating IPs.
const (
	IPv4 IPType = "ipv4"
	IPv6 IPType = "ipv6"
)

// PrimaryIP is a Primary IP of a project: an IPv4 address or an IPv6 /64.
type PrimaryIP struct {
	ID         int64             `json:"id"`
	ProjectID  int64             `json:"project_id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	Created    time.Time         `json:"created"`
	Protection Protection        `json:"protection"` // only Delete counts
	Type       IPType            `json:"type"`
	// IP is the address, or for IPv6 the /64 as "2001:db8:1:1::/64".
	IP         string `json:"ip"`
	Location   string `json:"location"`
	AssigneeID int64  `json:"assignee_id"` // the server, 0 for none
	AutoDelete bool   `json:"auto_delete"` // deleted with its server
}

// RecordID returns the Primary IP's ID.
func (p PrimaryIP) RecordID() int64 { return p.ID }

// RecordProject returns the Primary IP's project.
func (p PrimaryIP) RecordProject() int64 { return p.ProjectID }

// RecordName implements Named.
func (p PrimaryIP) RecordName() string { return p.Name }

// FloatingIP is a Floating IP of a project: an IPv4 address or an IPv6 /64
// that moves between servers. It comes from the same pool as the Primary
// IPs.
type FloatingIP struct {
	ID          int64             `json:"id"`
	ProjectID   int64             `json:"project_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels"`
	Created     time.Time         `json:"created"`
	Protection  Protection        `json:"protection"` // only Delete counts
	Type        IPType            `json:"type"`
	// IP is the address, or for IPv6 the /64 as "2001:db8:1:1::/64".
	IP           string `json:"ip"`
	HomeLocation string `json:"home_location"` // the location's name
	ServerID     int64  `json:"server_id"`     // the server it is assigned to, 0 for none
}

// RecordID returns the Floating IP's ID.
func (f FloatingIP) RecordID() int64 { return f.ID }

// RecordProject returns the Floating IP's project.
func (f FloatingIP) RecordProject() int64 { return f.ProjectID }

// RecordName implements Named.
func (f FloatingIP) RecordName() string { return f.Name }

// Firewall is a Cloud Firewall of a project: rules that filter the public
// addresses of the servers it applies to.
type Firewall struct {
	ID        int64             `json:"id"`
	ProjectID int64             `json:"project_id"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	Created   time.Time         `json:"created"`
	Rules     []FirewallRule    `json:"rules"`
	// AppliedTo are the servers and label selectors it applies to, in the
	// order they were added.
	AppliedTo []FirewallTarget `json:"applied_to"`
}

// RecordID returns the firewall's ID.
func (f Firewall) RecordID() int64 { return f.ID }

// RecordProject returns the firewall's project.
func (f Firewall) RecordProject() int64 { return f.ProjectID }

// RecordName implements Named.
func (f Firewall) RecordName() string { return f.Name }

// FirewallRule is a rule of a firewall, with Hetzner's fields: an "in"
// rule names source IPs, an "out" rule destination IPs.
type FirewallRule struct {
	Description    *string  `json:"description"`
	Direction      string   `json:"direction"`
	Protocol       string   `json:"protocol"`
	Port           string   `json:"port"` // "" for icmp, esp and gre
	SourceIPs      []string `json:"source_ips"`
	DestinationIPs []string `json:"destination_ips"`
}

// FirewallTarget is what a firewall applies to: a server, or the servers
// of the project that a label selector matches.
type FirewallTarget struct {
	// ID is a label selector's own ID, which its actions name (Hetzner
	// gives each one an ID); 0 for a server.
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"` // 0 for a label selector
	Selector string `json:"selector"`  // "" for a server
	// Servers are the servers the selector matches and the firewall
	// applies to, by ID; the API keeps them up to date when servers or
	// their labels change.
	Servers []int64 `json:"servers"`
}

// Network is a private network of a project: its range with subnets, in
// which servers get their addresses, and its routes.
type Network struct {
	ID         int64             `json:"id"`
	ProjectID  int64             `json:"project_id"`
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels"`
	Created    time.Time         `json:"created"`
	Protection Protection        `json:"protection"` // only Delete counts
	IPRange    string            `json:"ip_range"`
	Subnets    []NetworkSubnet   `json:"subnets"`
	Routes     []NetworkRoute    `json:"routes"`
	// ExposeRoutesToVSwitch is kept and shown; the lab has no vSwitch.
	ExposeRoutesToVSwitch bool `json:"expose_routes_to_vswitch"`
	// VLAN is the lab's own: the VLAN that carries the network between the
	// appliance and the servers' NICs.
	VLAN int `json:"vlan"`
}

// RecordID returns the network's ID.
func (n Network) RecordID() int64 { return n.ID }

// RecordProject returns the network's project.
func (n Network) RecordProject() int64 { return n.ProjectID }

// RecordName implements Named.
func (n Network) RecordName() string { return n.Name }

// NetworkSubnet is a subnet of a network, with Hetzner's fields.
type NetworkSubnet struct {
	Type        string `json:"type"` // "cloud" or "server"
	IPRange     string `json:"ip_range"`
	NetworkZone string `json:"network_zone"`
}

// NetworkRoute is a route of a network: what goes to Destination goes to
// Gateway, an address in the network.
type NetworkRoute struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}

// Lab is the lab's own records, which the API does not show. Later phases add
// the Internet clients and the ACME keys.
type Lab struct {
	// Appliance is the appliance that runs, or ran last.
	Appliance Appliance               `json:"appliance"`
	Faults    map[int64]ProjectFaults `json:"faults"` // by project ID
	// Images is the image cache: the system images on the disk, by the
	// image's name ("debian-13").
	Images map[string]CachedImage `json:"images"`
	// Pools are where the next search for a free public address starts.
	Pools Pools `json:"pools"`
}

// Appliance is the appliance that runs: its release and its MAC set. Both
// are empty before its first start; the first start takes the set A.
type Appliance struct {
	Release string `json:"release"` // the release disk's SHA-256, hex
	MACSet  string `json:"mac_set"` // "A" or "B"
}

// CachedImage is a system image in the cache.
type CachedImage struct {
	Source string    `json:"source"` // the URL it was loaded from
	SHA256 string    `json:"sha256"` // of the file loaded, hex
	File   string    `json:"file"`   // the VHDX, a server disk's parent
	Loaded time.Time `json:"loaded"`
}

// Pools hold the next free public address of each family, as an offset
// into its range: addresses are handed out in order, and a released one
// comes back only once the pool has gone round, so that an address does not
// pass from one server to the next at once.
type Pools struct {
	NextIPv4 int `json:"next_ipv4"`
	NextIPv6 int `json:"next_ipv6"`
	// NextVLAN is the next private network's VLAN, as an offset into the
	// VLANs the lab hands out; NextPrivateMAC counts the servers' private
	// NICs, each of which gets a MAC address of its own.
	NextVLAN       int    `json:"next_vlan"`
	NextPrivateMAC uint32 `json:"next_private_mac"`
}

// ProjectFaults are the faults injected into a project.
type ProjectFaults struct {
	API APIFault `json:"api"`
}

// APIFault makes the project's next API calls fail: Count calls answer with
// Status, each after DelayMS.
type APIFault struct {
	Status  int `json:"status"`
	Count   int `json:"count"`
	DelayMS int `json:"delay_ms"`
}
