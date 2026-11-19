# Phase 08: Load balancers and certificates

Confirmed assumptions and the user's answers (Q9 to Q13): CONTEXT.md and
DECISIONS.md (Q9 to Q13 and the four phase 08 entries of 2026-11-19).
Research: RESEARCH.md ("R<n>" below means its section n). Where
RESEARCH.md left something UNCONFIRMED, CONTEXT.md and the decisions D1 to
D12 at the end settle it. Test map: TESTS.md.

## Interfaces

New or changed in this phase. Tests and implementers follow them.

```go
// internal/ca (08-01)
package ca

// CRLURL is the CRL distribution point of every leaf (Q12).
const CRLURL = "http://api.hetzner.internal/hlab/v1/ca.crl"
const (
	LeafValidity = 90 * 24 * time.Hour
	CAValidity   = 10 * 365 * 24 * time.Hour
	CRLValidity  = 8 * 24 * time.Hour // nextUpdate - thisUpdate
)

type CA struct{ /* key, cert, dir, now, crl */ }

// Open loads ca.pem, ca.key and ca.crl from dir. If ca.pem or ca.key is
// missing it creates a new CA: ECDSA P-256, CN name ("hlab CA" or
// "hlab-dev CA"), CA:true, keyUsage keyCertSign|cRLSign, valid CAValidity,
// no CDP. ca.key is written 0600; the folder ACL (SYSTEM only) comes from
// hlabd install. now nil means time.Now.
func Open(dir, name string, now func() time.Time) (*CA, error)
// Certificate returns the CA certificate as PEM.
func (c *CA) Certificate() []byte
// Issue signs a leaf for domains:
//   - ECDSA P-256 key, CN domains[0], DNS SANs = domains (wildcards kept)
//   - extKeyUsage serverAuth, CRLDistributionPoints [CRLURL]
//   - valid from now-1h to now+LeafValidity
// It returns the leaf PEM followed by the CA PEM (the chain), and the key
// as PKCS#8 PEM.
func (c *CA) Issue(domains []string) (certPEM, keyPEM []byte, err error)
// RenewCRL writes a new empty CRL (number +1, thisUpdate now, nextUpdate
// now+CRLValidity) to ca.crl.
func (c *CA) RenewCRL() error
// CRL returns the current CRL as DER (RenewCRL first if none exists yet).
func (c *CA) CRL() ([]byte, error)

// internal/agentapi (08-02): additions
type LoadBalancer struct {
	// ... existing fields ...
	MaxConn int // max_connections of the type
}
type LBHTTP struct {
	// ... existing fields; CertPEM[i] is leaf+chain, KeyPEM[i] its key ...
	TimeoutIdle int // seconds
}

// internal/agent (08-02): pure functions, built on every OS
// HAProxyConfig renders the haproxy.cfg of one LB, plus the files it
// references (name -> content, one "<listen_port>-<i>.pem" with
// leaf+chain+key per certificate), with paths below dir. Naming (D2):
//   - frontend "fe<listen_port>", backend "be<listen_port>"
//   - server name = LBTarget.Key
//   - redirect frontend "fe80redirect"
//   - stats socket dir/haproxy.sock
func HAProxyConfig(lb agentapi.LoadBalancer, dir string) (cfg string, files map[string][]byte)
// ParseStat reads `show stat` CSV of one instance: health per target key
// and listen port (first token of status: UP=healthy, DOWN=unhealthy,
// anything else or check_status INI/UNK=unknown), and counters summed over
// the frontends.
func ParseStat(csv []byte) ([]agentapi.TargetHealth, agentapi.LBCounters, error)

// internal/api (08-03)
type FieldError struct {
	Code, Field, Message string
	Status int // 0: Status(Code); set for operation-specific statuses (update_service 422)
}

// internal/resources/networks (08-07)
// LoadBalancerMembers is the hook the cloud sets to the loadbalancers module.
type LoadBalancerMembers interface {
	// InNetwork returns the IDs and IPs of the LBs attached to network netID.
	InNetwork(tx store.Tx, projectID, netID int64) ([]LBMember, error)
	// NetworkDeleted detaches every LB from netID.
	NetworkDeleted(tx store.Tx, netID int64) error
}
type LBMember struct { ID int64; IP netip.Addr }
func (m *Module) SetLoadBalancers(h LoadBalancerMembers)
// AttachLoadBalancer checks the network (same project and network zone)
// and returns ip (given, in a subnet, not used by a server, LB or alias)
// or the next free IP of ipRange or of the first subnet that has one,
// together with the network's VLAN. Errors: 422 ip_not_available,
// no_subnet_available, invalid_input.
func (m *Module) AttachLoadBalancer(tx store.Tx, projectID int64, zone string, netID int64, ip, ipRange string) (netip.Addr, int, error)

// internal/resources/certificates (08-08, 08-10)
package certificates
const Kind = "certificates"
type Issuer interface{ Issue(domains []string) (certPEM, keyPEM []byte, err error) }
type Deps struct {
	Store     *store.Store
	CA        Issuer                                                 // nil: managed creation answers 422 invalid_input
	PushAgent func(ctx context.Context) error
	UsedBy    func(tx store.Tx, projectID, certID int64) ([]int64, error) // LB IDs; nil: none
	Now       func() time.Time                                        // nil: time.Now
	Clock     scheduler.Clock                                         // renewal job (nil: real)
}
func New(d Deps) *Module // Type() "certificates"; resource.Scheduled (08-10: hourly renewal)
// KeyPair returns the certificate (leaf+chain) and key PEM of certificate
// id of projectID; store.ErrNotFound if unknown; ErrNotIssued for a
// managed certificate that has none yet.
func KeyPair(tx store.Tx, projectID, id int64) (certPEM, keyPEM string, err error)
var ErrNotIssued = errors.New("certificate not issued")

// internal/resources/primaryips (08-09)
const AssigneeLoadBalancer = "load_balancer"
// AssignLoadBalancer assigns the given primary IPs (nil: none) to lbID.
// Each must be in the project, unassigned (422 primary_ip_assigned), not
// blocked (422 primary_ip_blocked), in location (422
// primary_ip_location_mismatch) and of the right version (412
// primary_ip_version_mismatch). It returns the IPv4 and the IPv6 /64.
func (m *Module) AssignLoadBalancer(tx store.Tx, projectID, lbID int64, location string, ipv4, ipv6 *int64) (netip.Addr, netip.Prefix, error)
// LoadBalancerDeleted unassigns, or deletes when auto_delete is set, the
// primary IPs held by lbID.
func (m *Module) LoadBalancerDeleted(tx store.Tx, lbID int64) error

// internal/resources/loadbalancers (08-13 to 08-19)
package loadbalancers
const Kind = "load_balancers"
type PublicIPs interface { // primaryips
	AssignLoadBalancer(tx store.Tx, projectID, lbID int64, location string, ipv4, ipv6 *int64) (netip.Addr, netip.Prefix, error)
	LoadBalancerDeleted(tx store.Tx, lbID int64) error
}
type Networks interface { // networks
	AttachLoadBalancer(tx store.Tx, projectID int64, zone string, netID int64, ip, ipRange string) (netip.Addr, int, error)
}
type Deps struct {
	Store       *store.Store
	Catalog     *catalog.Catalog
	PublicRange netip.Prefix
	PushAgent   func(ctx context.Context) error
	AgentStatus func(ctx context.Context) (agentapi.Status, error) // health and metrics job
	PublicIPs   PublicIPs                                          // nil: public_net IDs answer invalid_input
	Networks    Networks                                           // nil: network attach answers invalid_input
	Clock       scheduler.Clock                                    // nil: real
}
func New(d Deps) *Module // Type() "load_balancers"; Deleter, NetContributor, Scheduled
// IPv6 is the formal IPv6 of an LB without an IPv6 primary IP (Q11).
func IPv6(id int64) netip.Addr // 2001:db8:<0x80000000|id as 32 bits>::1
// Hooks the cloud wires (08-19, 08-20):
func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error            // removes server targets
func (m *Module) ForServer(tx store.Tx, serverID int64) ([]int64, error)     // LBs with a server target on it
func (m *Module) UsedBy(tx store.Tx, projectID, certID int64) ([]int64, error) // LBs whose services use certID
func (m *Module) InNetwork(tx store.Tx, projectID, netID int64) ([]networks.LBMember, error)
func (m *Module) NetworkDeleted(tx store.Tx, netID int64) error

// internal/settings (08-06)
// Save writes s to path as TOML, replacing the file (D4).
func Save(path string, s Settings) error

// internal/mgmt (08-06)
type Deps struct {
	// ... existing ...
	CACert func() []byte                           // nil leaves GET /ca.pem out
	Trust  func(ctx context.Context, on bool) error // nil leaves PUT /trust out
}
// GET /mgmt/v1/ca.pem: 200, application/x-pem-file
// PUT /mgmt/v1/trust {"on":bool}: 200 {"on":bool}; calls Trust and on
// success saves [certificates] trust_ca to settings.toml

// internal/cli (08-06)
//   hlab ca export <file>      writes GET /ca.pem to file
//   hlab ca trust on|off       PUT /trust; --output json prints {"on":bool}

// internal/hostnet/windows (08-04, 08-12): TrustCA(ctx, pem, on) implements
// the hostnet interface. It sets the Windows store first, then WSL; without
// a logged-on user the WSL part is pending and is applied by the next
// WatchWSL change (D3).
```

## Plans

- [x] 08-01 hlab CA
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CERT-02, CERT-04 | Tests: internal/ca/ca_test.go
  - Reads: ARCHITECTURE "ca", "Files and formats"; decisions/managed-certificates.md; DECISIONS Q12; R6 (last point: certificate extensions); this file "Interfaces"
  - Size: ~300 lines in 3 files
  - Adjusts: -
  - [x] `Open` (create or load), `Certificate`, `Issue` (internal/ca/ca.go)
  - [x] `RenewCRL`, `CRL` (x509.CreateRevocationList, number in ca.crl) (internal/ca/crl.go)
  - [x] Rows of TESTS.md for 08-01 (internal/ca/ca_test.go, group `TestCA`)

- [x] 08-02 Agent: HAProxy configuration and stats
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LB-02, LB-03, LB-04, LB-05, LB-07 | Tests: internal/agent/haproxy_test.go
  - Reads: R1.2, R1.4, R3.1, R3.3; DECISIONS Q13; CONTEXT (HAProxy mapping); ARCHITECTURE "hlab-agent" (LoadBalancer types); decision D2 below
  - Size: ~400 lines in 4 files
  - Adjusts: -
  - [x] `MaxConn`, `LBHTTP.TimeoutIdle` (internal/agentapi/agentapi.go)
  - [x] `HAProxyConfig`:
    - global with `maxconn`, stats socket and `server-state-file`; one frontend and backend per service, `mode tcp|http`
    - `balance roundrobin|leastconn`; one server line per target on the destination port, with `send-proxy-v2` when proxyprotocol is on
    - http: `option forwardfor`, X-Forwarded-Proto/Port, sticky cookie `cookie <name> insert indirect nocache maxlife <lifetime>s`, `timeout client/server <timeout_idle>s`
    - https: `bind :<port> ssl crt` with every certificate file, the first being the default; `redirect_http` as frontend `fe80redirect` answering 301 to https
    - health check: `option httpchk`, `http-check send meth GET uri <path> ver HTTP/1.1 hdr Host <domain>`, `http-check expect rstatus` from the status patterns, `expect string` for `response`, `ssl` when tls is on, `inter`, `fall`, `rise`, `timeout check`
    - (internal/agent/haproxy.go)
  - [x] `ParseStat` (internal/agent/haproxy_stat.go)
  - [x] Rows of TESTS.md for 08-02 (internal/agent/haproxy_test.go, registered in `TestAgent` in internal/agent/metadata_test.go)

- [x] 08-03 API groundwork: error codes and load balancer types
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LB-01 | Tests: internal/resources/loadbalancertypes/loadbalancertypes_test.go
  - Reads: R3.4; DECISIONS "Load balancer networks, targets and services", "Load balancer public IPs and primary IPs", "HTTP status of error codes from the OpenAPI error table"; OpenAPI `/load_balancer_types`; internal/resources/servertypes/servertypes.go as pattern; decision D7 below
  - Size: ~250 lines in 5 files
  - Adjusts: -
  - [x] Status table entries:
    - 422: `load_balancer_already_attached`, `load_balancer_not_attached_to_network`, `targets_without_use_private_ip`, `load_balancer_public_interface_disabled`, `resolve_cloud_private_targets_error`, `resolve_cloud_public_targets_error`, `target_already_defined`, `invalid_load_balancer_type`, `primary_ip_blocked`, `primary_ip_location_mismatch`, `primary_ip_already_assigned`
    - 412: `source_port_already_used`
    - `FieldError.Status` used by `Fail`
    - (internal/api/errors.go)
  - [x] Module `loadbalancertypes`: `GET /load_balancer_types` (filter name, paging) and `/{id}` from `Catalog.LoadBalancerTypes` (internal/resources/loadbalancertypes/loadbalancertypes.go)
  - [x] The response validator adds `load_balancer` to the enum of `primary_ip.assignee_type` in the loaded description (D7) (internal/apitest/openapi.go)
  - [x] Rows of TESTS.md for 08-03 (internal/resources/loadbalancertypes/loadbalancertypes_test.go, group `TestLoadBalancerTypes`)

- [x] 08-04 Host: CA in the Windows trust store
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CERT-04 | Tests: - (e2e in 08-21)
  - Reads: R6; ARCHITECTURE "hostnet"; DECISIONS "HAProxy on the appliance and Windows trust store"
  - Size: ~150 lines in 2 files
  - Adjusts: -
  - [x] `TrustCA`:
    - decode the PEM
    - on: `CertOpenStore` LocalMachine ROOT, then `CertCreateCertificateContext` and `CertAddCertificateContextToStore` REPLACE_EXISTING
    - off: find by SHA-1 thumbprint with `CertFindCertificateInStore` and `CertDeleteCertificateFromStore`; a missing entry is no error
    - then call `h.trustWSL(ctx, pem, on)`, which stays a stub returning nil until 08-12
    - (internal/hostnet/windows/trust.go, internal/hostnet/windows/hostnet.go: remove the stub)

- [x] 08-05 Appliance: HAProxy packages
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LB-02 | Tests: - (e2e in 08-21)
  - Reads: R1.1; DECISIONS "Appliance debs as `<name>.deb` at the cidata root", "Missing appliance debs do not stop the cloud"; appliance/hlab-agent-install.sh
  - Size: ~60 lines in 2 files
  - Adjusts: -
  - [x] Five entries (haproxy, liblua5.4-0, libopentracing-c-wrapper0t64, libopentracing1, lsb-base) with pool and snapshot URLs; download the five files once and put the recomputed sha256 in the catalog (catalog/appliance-debs.json)
  - [x] Install script: when `command -v haproxy` fails, run one `dpkg -i` with all five files, then `systemctl disable --now haproxy`; create `/run/hlab` (appliance/hlab-agent-install.sh)

- [x] 08-06 Trust switch and CA export in the management API and CLI
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CERT-04, CERT-05 | Tests: internal/mgmt/mgmt_test.go, internal/cli/cli_test.go
  - Reads: ARCHITECTURE "hlab (management command)", "Management API"; DECISIONS Q10; this file "Interfaces"; decision D4 below
  - Size: ~300 lines in 5 files
  - Adjusts: -
  - [x] `settings.Save` (internal/settings/settings.go)
  - [x] `Deps.CACert`, `Deps.Trust`, `GET /ca.pem`, `PUT /trust`:
    - load the settings, call Trust, then save `trust_ca`
    - a Trust error answers `server_error` and saves nothing
    - a body without `on` answers `invalid_input`
    - (internal/mgmt/mgmt.go; the settings path comes from a new `Deps.SettingsPath`)
  - [x] `hlab ca export <file>`, `hlab ca trust on|off` (usage errors give exit code 2) (internal/cli/cli.go)
  - [x] Rows of TESTS.md for 08-06 (internal/mgmt/mgmt_test.go, internal/cli/cli_test.go)

- [x] 08-07 Networks: load balancer members
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LB-01 | Tests: internal/resources/networks/networks_test.go
  - Reads: internal/resources/networks/attach.go (`allocate`, `usedIPs`), networks.go (`view`, delete); this file "Interfaces"
  - Size: ~200 lines in 4 files
  - Adjusts: -
  - [x] `LoadBalancerMembers`, `LBMember`, `SetLoadBalancers`, `AttachLoadBalancer`:
    - `usedIPs` also counts LB IPs
    - the network view fills `load_balancers` from `InNetwork`
    - network delete calls `NetworkDeleted`
    - (internal/resources/networks/attach.go, internal/resources/networks/networks.go, internal/resources/networks/lb.go)
  - [x] Rows of TESTS.md for 08-07 (internal/resources/networks/networks_test.go, with a fake `LoadBalancerMembers`)

- [x] 08-08 Certificates: uploaded certificates and the resource
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CERT-01 | Tests: internal/resources/certificates/certificates_test.go
  - Reads: R4; OpenAPI `/certificates`, `/certificates/{id}`, `/certificates/actions`; DECISIONS "Certificate issuance and status"; internal/resources/firewalls/firewalls.go as pattern
  - Size: ~380 lines in 3 files
  - Adjusts: -
  - [x] Module, `Record` (Hetzner shape; `_hlab.key` never shown), `Deps`, `KeyPair`, `ErrNotIssued`:
    - list (filter name, label_selector, type; sort id, name, created), GET, PUT (name and labels)
    - POST `uploaded` (201, `action` null, `status` null)
    - DELETE: 422 `resource_in_use` while `UsedBy` names an LB
    - `used_by` from `UsedBy`
    - action routes through `resactions.RoutesFor`
    - POST `managed` answers 422 `invalid_input` on `type` until 08-10
    - (internal/resources/certificates/certificates.go)
  - [x] PEM handling: parse the chain and the PKCS#1, PKCS#8 or EC key; the key must match the leaf, otherwise `invalid_input` on `private_key`; a bad chain gives `invalid_input` on `certificate`. The SHA-256 fingerprint is colon-separated hex; domain names come from the SANs, else the CN (internal/resources/certificates/pem.go)
  - [x] Rows of TESTS.md for 08-08 (internal/resources/certificates/certificates_test.go, group `TestCertificates`)

- [x] 08-09 Primary IPs held by load balancers
  - Wave: 2 | Depends on: 08-03 | Review: alone
  - Requirements: LB-01 | Tests: internal/resources/primaryips/primaryips_test.go
  - Reads: DECISIONS "Load balancer public IPs and primary IPs", "Primary IP details"; internal/resources/primaryips/actions.go (`assign`, `unassign`, `Allocate`, `ServerDeleted`)
  - Size: ~200 lines in 3 files
  - Adjusts: -
  - [x] `AssigneeLoadBalancer`, `AssignLoadBalancer`, `LoadBalancerDeleted`:
    - `assign` on an LB-held IP gives 422 `primary_ip_already_assigned`; `unassign` gives 422 `invalid_input`
    - DELETE of an LB-held IP gives `primary_ip_assigned`
    - `Contribute` skips LB-held IPs, because the LB contributes them
    - (internal/resources/primaryips/actions.go, internal/resources/primaryips/primaryips.go)
  - [x] Rows of TESTS.md for 08-09 (internal/resources/primaryips/primaryips_test.go)

- [x] 08-10 Certificates: managed issuance, retry and renewal
  - Wave: 2 | Depends on: 08-01, 08-08 | Review: alone
  - Requirements: CERT-02, CERT-03 | Tests: internal/resources/certificates/managed_test.go
  - Reads: R4; DECISIONS "Certificate issuance and status"; decisions/managed-certificates.md; internal/resources/zones/zones.go (`Record`, `Mode`); internal/scheduler/scheduler.go
  - Size: ~350 lines in 3 files
  - Adjusts: the 08-08 row case that expects `invalid_input` for `type: managed` (replaced by the CERT-02 row)
  - [x] POST `managed` and the `retry` action:
    - managed: `domain_names` required; 201 with a `create_certificate` action; `status` pending/unavailable
    - the action checks every domain against the project's zones: equal to the zone, or below it, wildcards included
    - no zone gives `dns_zone_not_found`; a secondary zone gives `dns_zone_is_secondary_zone`; both set issuance `failed` with `status.error` and end the action `error` with the same code
    - success: `CA.Issue` gives issuance completed, renewal scheduled, and the fields read from the leaf
    - `retry` (`retry_issuance`): 422 `invalid_input` unless issuance or renewal is failed
    - (internal/resources/certificates/managed.go)
  - [x] Hourly renewal job:
    - a managed, completed certificate with less than 30 days left gets renewal `pending`, then a new issuance
    - success gives renewal `scheduled`, the new leaf and `PushAgent`
    - failure gives renewal `failed` with `error` and keeps the old leaf
    - (internal/resources/certificates/renew.go)
  - [x] Rows of TESTS.md for 08-10 (internal/resources/certificates/managed_test.go; fake `Issuer` and fake clock)

- [x] 08-11 Agent: load balancer applier
  - Wave: 2 | Depends on: 08-02 | Review: alone
  - Requirements: LB-02, LB-03, LB-04, LB-05 | Tests: - (e2e in 08-21; Linux-only code)
  - Reads: R1.2, R1.3, R2; internal/agent/vrf_linux.go, isolation_linux.go, network_linux.go; decision D1 below
  - Size: ~400 lines in 5 files
  - Adjusts: -
  - [x] Applier `loadbalancers` (after `vrf`, before `firewall`). Per LB:
    - netns `lb<ID>`
    - public veth `lbp<ID>`: root end 169.254.0.1/32 with route `<public IP>/32 dev lbp<ID>`; netns end with the public IP /32 and `default via 169.254.0.1 onlink`; only while `PublicIPv4` is valid
    - network veth `lbn<ID>`: root end enslaved to `vrf<vlan>` with 169.254.0.1/32 and VRF route `<private IP>/32`; netns end with the private IP /32 and route `<network range> via 169.254.0.1 onlink src <private IP>`
    - config and files in `/run/hlab/lb<ID>/`; start `haproxy -W -S master.sock`; on a changed config run `haproxy -c`, then the master CLI `reload`
    - LBs no longer in the document are stopped and their netns deleted
    - all steps idempotent
    - (internal/agent/lb_linux.go, internal/agent/lb_other.go)
  - [x] Conntrack zone: in the isolation raw chain, `iifname lbn<ID> ct zone set <vlan>` (internal/agent/isolation_linux.go)
  - [x] `GET /v1/status` fills `Targets` and `LBMetrics` through `show stat` on each instance's socket and `ParseStat`, by asking the applier through an optional interface (internal/agent/agent.go)
  - [x] Register the applier (cmd/hlab-agent/main.go)

- [x] 08-12 Host: CA in WSL2 distros
  - Wave: 2 | Depends on: 08-04 | Review: alone
  - Requirements: CERT-04 | Tests: internal/hostnet/windows/trust_test.go
  - Reads: R7; DECISIONS Q9; ARCHITECTURE "Network" (WSL2 paragraph); internal/hostnet/windows/wslconfig.go, wsl.go (WatchWSL); decision D3 below
  - Size: ~350 lines in 4 files
  - Adjusts: -
  - [x] Running WSL commands as the user:
    - `userRunner`: the console session's token through WTSQueryUserToken, then `CreateEnvironmentBlock` with `WSL_UTF8=1`, then `CreateProcessAsUser` (CREATE_NO_WINDOW, desktop `winsta0\default`, stdin and stdout pipes)
    - no token returns `errNoUser`
    - (internal/hostnet/windows/runas.go)
  - [x] `trustWSL` with an injectable runner:
    - list with `wsl.exe --list --quiet`, decoding UTF-8 or UTF-16LE with BOM, and skip `docker-desktop*`
    - per distro, on: `wsl.exe -d X -u root -e sh -c 'command -v update-ca-certificates >/dev/null || exit 3; cat > /usr/local/share/ca-certificates/hlab-ca.crt && update-ca-certificates'` with the PEM on stdin
    - off: `rm -f ... && update-ca-certificates --fresh`
    - exit code 3: skipped and logged; other failures: logged, and the remaining distros continue
    - `errNoUser`: remember the pending state; the `WatchWSL` callback path applies it at the next WSL change
    - (internal/hostnet/windows/trust_wsl.go, internal/hostnet/windows/wsl.go)
  - [x] Rows of TESTS.md for 08-12 (internal/hostnet/windows/trust_test.go, in `TestHostNet`)

- [x] 08-13 Load balancers: core resource
  - Wave: 2 | Depends on: 08-03 | Review: alone
  - Requirements: LB-01 | Tests: internal/resources/loadbalancers/loadbalancers_test.go
  - Reads: OpenAPI `/load_balancers`, `/load_balancers/{id}`, change_algorithm, change_type, change_protection; R3.1 (type limits), R5 (client reads); CONTEXT; DECISIONS "Load balancer public IPs and primary IPs", Q11; internal/resources/firewalls/firewalls.go as pattern; decision D5 below
  - Size: ~420 lines in 9 files (five of them stubs of a few lines)
  - Adjusts: -
  - [x] `Record`, `Deps`, `New`, `IPv6`, the full Hetzner LB shape (services, targets, private_net, public_net with dns_ptr, protection, `included_traffic`, `ingoing_traffic`/`outgoing_traffic` 0), `Routes`, list (filter name, label_selector; sort id, name, created), GET, PUT (name and labels) (internal/resources/loadbalancers/loadbalancers.go)
  - [x] Create, delete and actions:
    - POST: name unique (409); type and location or network_zone (zone alone takes its first location); algorithm (default round_robin)
    - POST: public_interface (default true); `public_net` IDs through `PublicIPs` (only with the public interface on, otherwise `invalid_input`); a system IPv4 from `ipam.NextPublic` otherwise
    - POST: network, services and targets through the stubs; 201 with a `create_load_balancer` action
    - DELETE: protection gives `protected`; releases the IP or calls `LoadBalancerDeleted`; `delete_load_balancer` with a push
    - `change_algorithm`; `change_type` (422 `invalid_load_balancer_type` over the new limits); `change_protection`
    - Deleter
    - the rows of TESTS.md for 08-13, in group `TestLoadBalancers` with a fake `PublicIPs` and the shared helper `newEnv`
    - (internal/resources/loadbalancers/create.go, internal/resources/loadbalancers/actions.go, internal/resources/loadbalancers/loadbalancers_test.go)
  - [x] Stub files, each owned and filled by its plan in wave 3; every stub refuses non-empty input with `invalid_input`:
    - `network.go`: `networkRoutes`, `attachAtCreate`
    - `services.go`: `serviceRoutes`, `createServices`
    - `targets.go`: `targetRoutes`, `createTargets`, `viewTargets`
    - `status.go`: `statusRoutes`, `Jobs`, `health`
    - `dnsptr.go`: `dnsPtrRoutes`
    - (all in internal/resources/loadbalancers)

- [ ] 08-14 Load balancers: network and public interface
  - Wave: 3 | Depends on: 08-07, 08-13 | Review: alone
  - Requirements: LB-01 | Tests: internal/resources/loadbalancers/network_test.go
  - Reads: OpenAPI attach_to_network, detach_from_network, enable/disable_public_interface; DECISIONS "Load balancer networks, targets and services"; R5 (Terraform and Ansible poll `private_net`)
  - Size: ~250 lines in 2 files
  - Adjusts: -
  - [ ] Network and public interface, with the networks module mounted in the test:
    - `attach_to_network` through `Networks` (a second network: 422 `load_balancer_already_attached`)
    - `detach_from_network`: `load_balancer_not_attached_to_network` when none; refused while targets use private IPs
    - `disable_public_interface` (422 `load_balancer_not_attached_to_network`, 422 `targets_without_use_private_ip`) and `enable_public_interface`
    - `attachAtCreate`; `InNetwork`, `NetworkDeleted`
    - (internal/resources/loadbalancers/network.go)
  - [ ] Rows of TESTS.md for 08-14 (internal/resources/loadbalancers/network_test.go)

- [ ] 08-15 Load balancers: services
  - Wave: 3 | Depends on: 08-08, 08-13 | Review: alone
  - Requirements: LB-02, LB-03, LB-04 | Tests: internal/resources/loadbalancers/services_test.go
  - Reads: R3.1, R3.3, R5 (CLI, Terraform, Ansible service fields); OpenAPI add_service, update_service, delete_service; DECISIONS "Load balancer networks, targets and services"
  - Size: ~380 lines in 2 files
  - Note: refuse control characters in cookie_name and in health-check path, domain and response with 422 `invalid_input` (the 08-02 renderer only drops them)
  - Adjusts: -
  - [ ] Services:
    - `add_service`, `update_service`, `delete_service`, `createServices`
    - validation of protocol, ports and http fields; certificates must exist in the project (`KeyPair`, else `invalid_input`)
    - the health check: omitted means tcp/destination port/15/10/3; protocol `https` accepted; status_codes default `["2??","3??"]`
    - `redirect_http` only for https, and it takes port 80
    - `source_port_already_used`: 412 on add, `FieldError.Status` 422 on update
    - limits `max_services` and `max_assigned_certificates`: 403 `resource_limit_exceeded`
    - an update with `cookie_lifetime: 0` or an empty `cookie_name` keeps the stored value
    - GET always shows the booleans
    - `UsedBy`
    - (internal/resources/loadbalancers/services.go)
  - [ ] Rows of TESTS.md for 08-15 (internal/resources/loadbalancers/services_test.go)

- [ ] 08-16 Load balancers: targets
  - Wave: 3 | Depends on: 08-13 | Review: alone
  - Requirements: LB-06 | Tests: internal/resources/loadbalancers/targets_test.go
  - Reads: R3.1 (targets), R3.4, R5 (target shapes); OpenAPI add_target, remove_target; DECISIONS "Load balancer networks, targets and services"; internal/labels
  - Size: ~380 lines in 2 files
  - Adjusts: -
  - [ ] Adding and removing targets:
    - `add_target` and `remove_target` for server, label_selector and ip; `createTargets`
    - server checks: `use_private_ip` needs the server in the LB's network (`resolve_cloud_private_targets_error`); a public server target needs an IPv4 (`resolve_cloud_public_targets_error`) and the public interface on (`load_balancer_public_interface_disabled`)
    - other checks: a server in another network zone gives `invalid_input`; ip targets must be unicast IPv4, not in the transit /30 and not the LB's own IP; duplicates give `target_already_defined`; `max_targets` gives 403
    - `resolve(tx, r) []resolved` (server to public or private IP, label selector to matching servers of the project, IP)
    - `viewTargets` with nested `targets` for label selectors and `health_status` from `m.health` (filled by 08-17)
    - `ServerDeleted`, `ForServer`
    - (internal/resources/loadbalancers/targets.go)
  - [ ] Rows of TESTS.md for 08-16 (internal/resources/loadbalancers/targets_test.go)

- [ ] 08-17 Load balancers: health status and metrics
  - Wave: 3 | Depends on: 08-13 | Review: alone
  - Requirements: LB-05, LB-08 | Tests: internal/resources/loadbalancers/status_test.go
  - Reads: R1.2 (counter resets), R3.1 (health_status), R3.5; OpenAPI `/load_balancers/{id}/metrics`; internal/metrics/metrics.go (step and point rules to mirror)
  - Size: ~350 lines in 3 files
  - Adjusts: -
  - [ ] Health and metrics data:
    - job `lb-status` every 10 s: `AgentStatus`, health cache per LB, counter samples per LB, 24 h retention
    - `m.health(lbID, key, port)` gives `unknown` when nothing is known
    - (internal/resources/loadbalancers/status.go)
  - [ ] `GET /load_balancers/{id}/metrics`:
    - `type` list required; start and end required (RFC3339)
    - the step rule of the server metrics: 200 points by default, at most 500
    - rates from deltas, where a smaller counter counts as a reset
    - series `open_connections`, `connections_per_second`, `requests_per_second`, `bandwidth.in`, `bandwidth.out`
    - an unknown type gives `invalid_input`
    - (internal/resources/loadbalancers/metrics.go)
  - [ ] Rows of TESTS.md for 08-17 (internal/resources/loadbalancers/status_test.go; fake `AgentStatus` and fake clock)

- [ ] 08-18 Load balancers: reverse DNS
  - Wave: 3 | Depends on: 08-13 | Review: alone
  - Requirements: LB-09 | Tests: internal/resources/loadbalancers/dnsptr_test.go
  - Reads: OpenAPI `/load_balancers/{id}/actions/change_dns_ptr`; internal/rdns/rdns.go; internal/resources/floatingips/actions.go (change_dns_ptr pattern)
  - Size: ~150 lines in 2 files
  - Adjusts: -
  - [ ] `change_dns_ptr` through `rdns.Change` on the LB's IPv4 /32 and IPv6 /64 entries (`servers.DecodeDNSPtr`); `public_net.ipv4.dns_ptr` and `ipv6.dns_ptr` through `rdns.Show`; push (internal/resources/loadbalancers/dnsptr.go)
  - [ ] Rows of TESTS.md for 08-18 (internal/resources/loadbalancers/dnsptr_test.go)

- [ ] 08-19 Load balancers: agent document
  - Wave: 4 | Depends on: 08-14, 08-15, 08-16, 08-18 | Review: alone
  - Requirements: LB-06, LB-09 | Tests: internal/resources/loadbalancers/contribute_test.go
  - Reads: ARCHITECTURE "hlab-agent" (`LoadBalancer` types); this file "Interfaces"
  - Size: ~200 lines in 2 files
  - Adjusts: -
  - [ ] `Contribute` (NetContributor) builds one `agentapi.LoadBalancer` per LB:
    - `PublicIPv4`, invalid while the interface is off
    - `Private` with the network's VLAN and IP; `Algorithm`; `MaxConn` from the type
    - services with health check and certificate PEMs and keys through `KeyPair`; a managed certificate not yet issued leaves the service out of TLS
    - targets from `resolve`, with `ViaNetworkID` set for private ones
    - `PTR` entries through `rdns.PTR` for the IPv4
    - (internal/resources/loadbalancers/contribute.go)
  - [ ] Rows of TESTS.md for 08-19 (internal/resources/loadbalancers/contribute_test.go)

- [ ] 08-20 Cloud wiring
  - Wave: 5 | Depends on: 08-05, 08-06, 08-09, 08-10, 08-11, 08-12, 08-17, 08-19 | Review: alone
  - Requirements: LB-01, CERT-04, CERT-05 | Tests: internal/resources/conventions_test.go
  - Reads: ARCHITECTURE "Cloud start and stop", "ca", "Management API"; DECISIONS Q9, Q12; internal/cloud/cloud.go; decision D6 below
  - Size: ~300 lines in 5 files
  - Adjusts: -
  - [ ] At start, before the modules:
    - `ca.Open(Paths.CA, "<tag> CA")` and `RenewCRL`
    - when `trust_ca` is on, `c.net.TrustCA(on)`; an error is logged as a problem and does not stop the start
  - [ ] Modules and hooks:
    - modules `loadbalancertypes`, `certificates` (CA, UsedBy = lb.UsedBy) and `loadbalancers` (PublicIPs = pips, Networks = nets, AgentStatus) after `fws`
    - `nets.SetLoadBalancers(lb)`, `srv.OnDelete(lb.ServerDeleted)`, and the server view's `load_balancers` through a new `srv.SetLoadBalancers(lb.ForServer)`
  - [ ] HTTP and jobs:
    - `GET /hlab/v1/ca.crl` on the API listener outside `/v1` (no token, `application/pkix-crl`)
    - a cloud job `crl` every 7 days that runs `RenewCRL`
    - mgmt `CACert` and `Trust` (TrustCA, then `c.set.Certificates.TrustCA` updated) and `SettingsPath`
    - (internal/cloud/cloud.go, internal/cloud/ca.go, internal/resources/servers/servers.go)
  - [ ] Conventions: load_balancers and certificates in `newFullEnv`, with the API-06, API-07 and API-11 cases (internal/resources/conventions_test.go)

- [ ] 08-21 End-to-end: load balancers and trust
  - Wave: 6 | Depends on: 08-20 | Review: alone | e2e
  - Requirements: LB-02, LB-03, LB-04, LB-05, CERT-04 | Tests: test/e2e/lb_test.go, test/e2e/cert_test.go
  - Reads: TESTS.md header; test/e2e/main_test.go (`sharedEnv`, server helpers), network_test.go (`sharedNetwork`), dns_test.go (zones), wsl_test.go; ROADMAP phase 08 success criteria
  - Size: ~400 lines in 3 files
  - Adjusts: -
  - [ ] Fixture `sharedLB`:
    - a per-run zone under `.test`, a network, and two servers in it whose cloud-init serves the hostname over HTTP on 80 and, from a second web server, on 8081
    - one LB attached to the network with private targets and these services: tcp 8080 to 80; tcp 8081 to 8081 (for LB-05); http 8000 to 80 with sticky sessions; https 443 with a managed certificate for `www.<zone>` and `redirect_http`
    - an A record for `www` pointing to the LB IP
    - (test/e2e/lb_test.go)
  - [ ] Rows LB-02, LB-03, LB-04, LB-05 (test/e2e/lb_test.go) and CERT-04 (test/e2e/cert_test.go; it switches trust off in `t.Cleanup` as well), registered in test/e2e/e2e_test.go

- [ ] 08-22 Documentation
  - Wave: 7 | Depends on: 08-21 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation", "Accounts and secrets" (`hlab-docs`); REQUIREMENTS DOC; CONTEXT; DECISIONS Q9 to Q13 and the phase 08 entries; decisions/managed-certificates.md (risk text)
  - Size: ~250 lines in 5 files
  - Adjusts: -
  - [ ] Deviations, in internal/api/deviations.go and docs/user/deviations.md:
    - IP targets
    - LB-held primary IPs (`assignee_type` load_balancer, assign and unassign answers)
    - formal LB IPv6
    - the hlab CA instead of Let's Encrypt, and the CRL URL
    - PROXY v2 unverified
    - the metrics resolution of 10 s
    - the codes chosen where Hetzner is unconfirmed: limits 403, zone mismatch, retry, delete in use, omitted health check
    - (internal/api/deviations.go, docs/user/deviations.md)
  - [ ] docs/user/commands.md (`hlab ca export`, `hlab ca trust on|off`), docs/user/configuration.md (`trust_ca`, appliance debs), README.md (load balancers, managed certificates, the trust switch with the risk text from decisions/managed-certificates.md, `curl https://<name>` on host and WSL2), docs/dev/components.md (ca, certificates, loadbalancers, agent HAProxy); examples run through `hcloud --context hlab-docs` as SPEC.md says

## Decisions taken in planning

- D1 The root end of every LB veth carries the same address, 169.254.0.1/32, like the guests' 172.31.1.1, and both sides use onlink routes. There is no transit addressing per LB. This applies to the public veth and to the network veth inside the VRF. Cost of change: low (appliance internal).
- D2 HAProxy object names:
  - frontend `fe<listen_port>`, backend `be<listen_port>`
  - server name = `LBTarget.Key` (`server:<id>`, `ip:<addr>`), so `ParseStat` maps rows back without a table
  - the redirect frontend is `fe80redirect`
  - Cost of change: low.
- D3 `TrustCA` without a logged-on user changes the Windows store, keeps the WSL part pending, and applies it when `WatchWSL` reports the next WSL change. A logon that starts WSL recreates its adapter. Cloud start and the next switch also apply it. Cost of change: low.
- D4 `settings.Save` rewrites the whole settings.toml from the parsed settings, so comments in the file are lost. The tray of phase 10 writes the file the same way. Cost of change: low.
- D5 The loadbalancers package is split into files per feature. 08-13 creates stubs for the route and hook functions of the later plans, so 08-14 to 08-18 run in parallel, each owning its own file and test file. The shared test helper `newEnv` lives in loadbalancers_test.go. Cost of change: low.
- D6 The CRL is served by hlabd on its API listener (`198.19.0.2:8780`, reached through the agent's port-80 proxy as `api.hetzner.internal`) outside the token-checked `/v1` tree. It is stored as `ca.crl` beside `ca.pem`. Cost of change: low.
- D7 The test response validator patches the loaded description: it adds `load_balancer` to `primary_ip.assignee_type`. That is the one documented deviation in a response shape; every other field is still validated. Cost of change: low.
- D8 `FieldError.Status` overrides the HTTP status of one answer. It is used only for `source_port_already_used` on update_service (422); all other codes keep the one table. Cost of change: low.
- D9 LB metrics keep their own samples in the loadbalancers module. `internal/metrics` is bound to `platform.Sample` and the server series. The step and point rules are those of the server metrics, and the retention is 24 h. Cost of change: low.
- D10 The CA's CN is "<profile tag> CA" (`hlab CA`, `hlab-dev CA`), so the store entries of the two profiles can be told apart. Cost of change: medium (the CA stays until uninstall).
- D11 `health_status` shows `listen_port` and `status` only; `detail` is left out because it is optional in the description. Cost of change: low.
- D12 A managed certificate that is not yet issued, or failed, is left out of the HTTPS service's TLS in the agent document. A service with no usable certificate is not rendered, until renewal or retry issues it. Cost of change: low.
