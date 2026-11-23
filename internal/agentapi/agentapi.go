// Package agentapi holds the documents hlabd and hlab-agent exchange over
// the agent's HTTP interface (PUT /v1/config, GET /v1/status, POST /v1/jobs),
// and hlabd and the storage agent (PUT /v1/storage, POST /v1/jobs).
package agentapi

import "net/netip"

// Schema is the version of Config this package describes.
const Schema = 1

// Config is the full desired state of the appliance, applied idempotently.
type Config struct {
	Schema, Generation int
	PublicRange        netip.Prefix // e.g. 198.19.0.0/16
	Servers            []Server
	FloatingIPs        []FloatingIP
	Networks           []Network
	Firewalls          []Firewall
	LoadBalancers      []LoadBalancer
	Zones              []Zone
	PTR                map[netip.Addr]string
	Metadata           []Metadata
	API                APIProxy         // listen 198.19.0.1:80, backend 198.19.0.2:8780
	Upstream           []netip.AddrPort // DNS forwarders: the host's DNS servers, else 1.1.1.1:53, 9.9.9.9:53
	Storage            StorageVM        // Addr invalid: no storage VM
	StorageBoxes       []StorageBoxRoute
}

// Server is a server's public side.
type Server struct {
	ID          int64
	Location    string     // "fsn1"
	MAC         string     // public adapter, "02:48:4c:..."
	PublicIPv4  netip.Addr // invalid if the server has no public IPv4
	FirewallIDs []int64    // applied firewalls, label selectors already resolved
}

// FloatingIP routes IP to the server's public IPv4 Target; unassigned:
// Target is invalid.
type FloatingIP struct {
	IP     netip.Addr
	Target netip.Addr
}

// Network is a private network.
type Network struct {
	ID      int64
	VLAN    int
	IPRange netip.Prefix
	Gateway netip.Addr // first address of IPRange
	Routes  []Route
	Members []Member
}

// Route is a route of a private network.
type Route struct {
	Destination netip.Prefix
	Gateway     netip.Addr
}

// Member is a server attached to a private network.
type Member struct {
	ServerID int64
	MAC      string
	IP       netip.Addr
	AliasIPs []netip.Addr
}

// Firewall is a firewall with its rules.
type Firewall struct {
	ID    int64
	Rules []Rule
}

// Rule is a firewall rule in Hetzner's shape.
type Rule struct {
	Direction      string         // "in" | "out"
	Protocol       string         // "tcp" | "udp" | "icmp" | "esp" | "gre"
	Port           string         // "80", "1000-2000", "any", "" for icmp/esp/gre
	SourceIPs      []netip.Prefix // direction in
	DestinationIPs []netip.Prefix // direction out
}

// LoadBalancer is a load balancer.
type LoadBalancer struct {
	ID         int64
	PublicIPv4 netip.Addr  // invalid if the public interface is disabled
	Private    []LBNetwork // one per attached network
	Algorithm  string      // "round_robin" | "least_connections"
	Services   []LBService
	Targets    []LBTarget // resolved from servers, label selectors, IPs
	MaxConn    int        // max_connections of the type
}

// LBNetwork is a load balancer's address in a private network.
type LBNetwork struct {
	NetworkID int64
	VLAN      int
	IP        netip.Addr
}

// LBService is a load balancer service.
type LBService struct {
	Protocol                    string // "tcp" | "http" | "https"
	ListenPort, DestinationPort int
	ProxyProtocol               bool
	HTTP                        *LBHTTP // nil for tcp
	HealthCheck                 LBHealthCheck
}

// LBHTTP holds the HTTP settings of a service.
type LBHTTP struct {
	CookieName                   string
	CookieLifetime               int
	StickySessions, RedirectHTTP bool
	CertPEM, KeyPEM              []string // CertPEM[i] is leaf+chain, KeyPEM[i] its key
	TimeoutIdle                  int      // seconds
}

// LBHealthCheck is the health check of a service.
type LBHealthCheck struct {
	Protocol                         string
	Port, Interval, Timeout, Retries int
	Path, Domain, Response           string
	StatusCodes                      []string
	TLS                              bool
}

// LBTarget is a resolved target; Key is "server:<id>" or "ip:<addr>",
// ViaNetworkID 0 is the public segment.
type LBTarget struct {
	Key          string
	IP           netip.Addr
	ViaNetworkID int64
}

// Zone is a DNS zone.
type Zone struct {
	Name   string
	TTL    int
	RRSets []RRSet
}

// RRSet is a record set in Hetzner's shape; Name is relative ("@", "www").
type RRSet struct {
	Name    string
	Type    string
	TTL     int
	Records []string
}

// Metadata is what the metadata service answers a server, found by source
// address and segment.
type Metadata struct {
	VLAN                                           int        // 0 = public segment
	IP                                             netip.Addr // source address of the asking server
	ServerID                                       int64
	Hostname, Region, AvailabilityZone, PublicIPv4 string
	PublicKeys                                     []string
	UserData                                       string
	PrivateNetworks                                []MetadataNetwork
	PasswordReset                                  *PasswordReset // pending root password reset, or nil
	Volumes                                        []int64        // attached volumes with automount
}

// MetadataNetwork is a private network as the metadata service shows it.
// InterfaceNum (0 = not shown) and Network (invalid = not shown) are
// Hetzner's interface_num and network fields.
type MetadataNetwork struct {
	IP           netip.Addr
	AliasIPs     []netip.Addr
	InterfaceNum int
	MAC          string
	NetworkID    int64
	NetworkName  string
	Network      netip.Prefix // the network's IP range
	Subnet       netip.Prefix
	Gateway      netip.Addr
}

// PasswordReset is a pending root password reset; SHA512Crypt is the
// crypt(3) hash, never the clear text.
type PasswordReset struct {
	ID          int64
	SHA512Crypt string
}

// APIProxy is the agent's reverse proxy to the hlabd API.
type APIProxy struct {
	Listen  netip.AddrPort
	Backend netip.AddrPort
}

// Job kinds.
const (
	JobMkfs          = "mkfs"
	JobRescuePrepare = "rescue-prepare"
)

// Job is work that needs Linux tools on the appliance.
type Job struct {
	Kind string // JobMkfs | JobRescuePrepare
	LUN  int    // disk attached to the appliance's SCSI controller 0
	FS   string // "ext4" | "xfs" (mkfs)
}

// JobResult is the outcome of a Job.
type JobResult struct {
	OK      bool
	Message string
}

// Status is the applied state of the agent.
type Status struct {
	Generation int
	Targets    map[int64][]TargetHealth // per load balancer
	LBMetrics  map[int64]LBCounters
	// PasswordResetsDone are the password reset IDs the guests confirmed,
	// kept while the reset is still in the config.
	PasswordResetsDone []int64
}

// TargetHealth is the health of one target of one service; Status is
// "healthy" | "unhealthy" | "unknown".
type TargetHealth struct {
	Key        string
	ListenPort int
	Status     string
}

// LBCounters are a load balancer's metrics counters.
type LBCounters struct {
	OpenConnections, ConnectionsTotal, RequestsTotal, BytesIn, BytesOut uint64
}
