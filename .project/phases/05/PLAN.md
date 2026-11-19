# Phase 05: Private networks, primary IPs and floating IPs, port isolation

Confirmed assumptions and answers: CONTEXT.md. Research: RESEARCH.md
("C<n>" below means its contradiction n, "R<n>" its numbered section).
Where RESEARCH.md contradicts CONTEXT.md, the research is followed (user
rule); those points are listed as decisions in the plans that apply them.
Test map: TESTS.md.

## Interfaces

New or changed in this phase; tests and implementers follow them.

```go
// internal/platform (05-01)
type Segment struct {
	PublicPVLAN, Promiscuous bool
	VLAN                     int  // private network: access VLAN 2..4092
	Trunk                    bool // the appliance's second adapter: VLANs 2..4092 tagged, native VLAN 0
}
type Platform interface {
	// ... unchanged methods; Metrics(ctx, vm) is removed (Q7) ...
	// AttachNIC and DetachNIC also work on a running VM (hot add/remove).
	// SetNICDeny replaces the MAC deny ACLs (Direction Both) of the VM's
	// adapter with MAC mac by deny; running or not; idempotent.
	SetNICDeny(ctx context.Context, vm, mac string, deny []net.HardwareAddr) error
}
// platformtest.Fake: keeps the adapters per VM (AttachNIC adds, DetachNIC
// removes, an unknown MAC is NotFound); SetNICDeny records
// Call{Op: "SetNICDeny", Arg: platformtest.Deny{MAC, Deny}} and stores the
// list; Fake.NICs(vm) []platform.NICSpec returns the current adapters with
// their deny lists.

// internal/resources/servers (05-04)
type PrivateNet struct { // Hetzner's server.private_net entry
	Network    int64    `json:"network"`
	IP         string   `json:"ip"`
	AliasIPs   []string `json:"alias_ips"`
	MACAddress string   `json:"mac_address"`
}
// Server.PrivateNet becomes []PrivateNet (stored docs with [] stay valid).
type Internal struct {
	// ... unchanged fields ...
	// Networks holds per attached network ID what adapter and seed need;
	// the range cannot change while servers are attached (C2).
	Networks map[int64]NetInternal `json:"networks,omitempty"`
}
type NetInternal struct {
	VLAN    int    `json:"vlan"`
	IPRange string `json:"ip_range"` // gateway = first host address
}
type PublicNetRequest struct { // create's public_net; IPv4/IPv6 0 = make a new one
	EnableIPv4, EnableIPv6 bool // default true
	IPv4, IPv6             int64
}
// PublicIPs makes or takes the primary IPs of a new server inside the
// create transaction (set by the cloud; nil = phase 04 behaviour: ipam and
// IDs from the primary_ips sequence, no records).
type PublicIPs interface {
	Allocate(tx store.Tx, projectID, serverID int64, location string, req PublicNetRequest) (PublicNet, error)
}
// Networks checks and allocates the networks of a server create inside the
// create transaction; api field errors as at Hetzner.
type Networks interface {
	AttachAtCreate(tx store.Tx, projectID, serverID int64, location string, ids []int64) ([]PrivateNet, map[int64]NetInternal, error)
}
func (m *Module) SetPublicIPs(h PublicIPs)
func (m *Module) SetNetworks(h Networks)
// OnDelete registers f; server deletion runs every f inside the
// transaction that deletes the record (primary IPs, floating IPs,
// network membership).
func (m *Module) OnDelete(f func(tx store.Tx, serverID int64) error)
// primaryips (05-07) and floatingips (05-08) each provide the hook as
//   func (m *Module) ServerDeleted(tx store.Tx, serverID int64) error
// New does not register hooks itself; the cloud (05-13) and the tests
// call srv.SetPublicIPs(pip), srv.OnDelete(pip.ServerDeleted),
// srv.OnDelete(fip.ServerDeleted). Create answers 201, DELETE 204.
// Constructors the tests use:
//   networks.New(networks.Deps{Store, Catalog, Servers, PushAgent})
//   primaryips.New(primaryips.Deps{Store, Catalog, PublicRange, Servers, PushAgent})
//   floatingips.New(floatingips.Deps{Store, Catalog, PublicRange, PushAgent})
// platformtest.Fake exposes NICs per VM with their deny lists (05-01).
// Update loads server id, applies f and stores it.
func Update(tx store.Tx, id int64, f func(*Record) error) error
// PrivateMAC is the MAC of private adapter number n (kv counter
// "mac.private.last"): 02:48:4c, nibble 0x2, low 20 bits of n.
func PrivateMAC(n int64) net.HardwareAddr
func NextPrivateMAC(tx store.Tx) (net.HardwareAddr, error)

// internal/resources/servers (05-10)
// SyncAdapters makes the VM's private adapters match the record
// (hot add with Segment{VLAN}, hot remove), sets the deny lists of every
// member of each touched network to the MACs of the other members, rewrites
// the seed (eject slot 0, write, insert) and pushes the agent document.
// A server without VM only gets its seed rewritten.
func (m *Module) SyncAdapters(ctx context.Context, stor *store.Store, id int64, touched []int64) error

// internal/resources/networks (05-05, 05-11, 05-12)
const Kind = "networks"
type Subnet struct {
	Type        string `json:"type"`          // "cloud" | "server"
	IPRange     string `json:"ip_range"`
	NetworkZone string `json:"network_zone"`
	Gateway     string `json:"gateway"`       // the network gateway (C1)
	VSwitchID   *int64 `json:"vswitch_id"`
}
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
}
type Record struct {
	Network // Hetzner's network object; `servers` and `load_balancers` are filled when shown
	Hlab struct {
		ProjectID int64 `json:"project_id"`
		VLAN      int   `json:"vlan"`
	} `json:"_hlab"`
}
func New(d Deps) *Module // Deps{Store, Catalog, Servers *servers.Module, PushAgent}; Module, Deleter, NetContributor, servers.Networks
func Load(tx store.Tx, projectID, id int64) (Record, error)
func Gateway(ipRange netip.Prefix) netip.Addr // first host address of the network range (C1)
// NextIP returns the lowest free host address of subnet, skipping the
// subnet's network and broadcast address, gw and 172.31.1.1.
func NextIP(subnet netip.Prefix, gw netip.Addr, used map[netip.Addr]bool) (netip.Addr, bool)

// internal/resources/primaryips (05-07)
const Kind = "primary_ips"
func New(d Deps) *Module // Deps{Store, Catalog, PublicRange, Servers *servers.Module, PushAgent}; Module, Deleter, Recoverer, servers.PublicIPs
func IPv6Net(id int64) netip.Prefix // standalone: 2001:db8:<0x80000000|id>::/64; created with a server: servers.IPv6Net(serverID)

// internal/resources/floatingips (05-08)
const Kind = "floating_ips"
func New(d Deps) *Module // Deps{Store, Catalog, PublicRange, PushAgent}; Module, Deleter, NetContributor
func IPv6Net(id int64) netip.Prefix // 2001:db8:<0xC0000000|id>::/64

// internal/agent (05-02, 05-09)
// VRF is the Applier of private networks on the trunk link (found by MAC
// 02:48:4c:00:00:02): per Network a VLAN subinterface t<vlan> enslaved to
// VRF device vrf<vlan> (table 1000+vlan) with the gateway /32-range address,
// /32 routes to members and alias IPs, Routes via their gateway; no default
// route; agent-owned links and routes only.
func NewVRF(trunkMAC net.HardwareAddr) *VRF
// Metadata answers by (VLAN, source IP); VLAN 0 is the public listener.
func (m *Metadata) HandlerFor(vlan int) http.Handler
```

## Plans

- [x] 05-01 Platform: trunk, deny lists, hot-plug, Metrics removal
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: NET-02, HOST-01 | Tests: - (covered by test/e2e/network_test.go)
  - Reads: ARCHITECTURE "platform", "Network"; RESEARCH R3; EXPERIMENTS E1; DECISIONS "Port isolation by private VLAN", "Q7: drop the unused per-VM Metrics call"
  - Size: ~250 lines in 5 files
  - Adjusts: -
  - [x] Probe first, on the dev machine as a non-elevated Hyper-V Administrators member with two throwaway VMs on a throwaway internal switch (scratch script, not committed, everything removed afterwards): a trunk adapter (`-Trunk -AllowedVlanIdList 2-4092 -NativeVlanId 0`) beside a PVLAN promiscuous and isolated port on the same switch; hot-add and remove of an adapter on a running Gen2 VM; add and remove of a MAC deny ACL on a running adapter. Report the result. If trunk and PVLAN cannot coexist or hot-plug or ACL changes need elevation, the plan stops (the design in ARCHITECTURE "Network" would change)
  - [x] `Segment.Trunk`, `SetNICDeny` (Get-VMNetworkAdapterAcl, remove the deny entries not wanted, add the missing ones, MACs with dashes, E1), AttachNIC with the trunk mode and on running VMs; remove `Metrics` from interface, hyperv and fake (internal/platform/platform.go, hyperv/nics.go, hyperv/metrics.go, hyperv/hyperv.go)
  - [x] Fake: adapters per VM, `SetNICDeny`, `NICs` (internal/platform/platformtest/fake.go)

- [x] 05-02 Agent: private networks in VRFs
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: NET-02, NET-03, NET-04 | Tests: - (covered by test/e2e/network_test.go)
  - Reads: ARCHITECTURE "hlab-agent", "Network"; RESEARCH C6, R4 (VRF, sysctls, netlink)
  - Size: ~300 lines in 3 files
  - Adjusts: -
  - [x] `VRF` Applier: find the trunk link by MAC and set it up; per Network create or keep VRF `vrf<vlan>` (table 1000+vlan) and VLAN link `t<vlan>`, enslave, gateway address, sysctls on `t<vlan>` (`forwarding=1`, `proxy_arp=1`, `proxy_arp_pvlan=1`, `send_redirects=0`, `accept_redirects=0`, `rp_filter=0`); routes in the VRF table with the agent's route protocol: `/32` per member IP and alias IP on `t<vlan>`, each network route via its gateway (onlink); remove the VRFs, links and routes of networks no longer in the document (internal/agent/vrf_linux.go, internal/agent/vrf_other.go)
  - [x] Register the applier with the trunk MAC `02:48:4c:00:00:02` (cmd/hlab-agent/main.go)

- [x] 05-03 Agent: floating IP routes and isolation per segment
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FIP-02, HOST-01, NET-02 | Tests: - (covered by test/e2e/ip_test.go, network_test.go)
  - Reads: ARCHITECTURE "hlab-agent", "Network"; RESEARCH R1 (unicast RPF), R4 (nftables, conntrack); DECISIONS "Metadata service binds IP and MAC on its input path"
  - Size: ~250 lines in 2 files
  - Adjusts: -
  - [x] Floating IPs: `syncRoutes` also keeps one onlink `/32` route per assigned floating IP via the target's public IPv4 on the public interface (internal/agent/network_linux.go)
  - [x] Isolation: forwarded IPv4 from a member MAC on `t<vlan>` is dropped unless its source is the member's IP, an alias IP, or inside a route destination whose gateway is that member (one accept rule per route before the drop); a raw-priority prerouting chain sets `ct zone` = VLAN by iifname on `t<vlan>` links, so overlapping ranges never share conntrack entries; the public rules stay as they are; still one atomic table replace (internal/agent/isolation_linux.go)

- [x] 05-04 Servers: network fields and hooks
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: PIP-01, NET-02, FIP-01 | Tests: - (covered by primaryips_test.go, adapters_test.go, attach_test.go)
  - Reads: ARCHITECTURE "resource", "cidata", "Data schema"; RESEARCH R2 (create_server, delete_server); CONTEXT "Corrected guest config", Q3; DECISIONS "Server public_net carries primary IP IDs", "Server MACs, IPv6 and root login"
  - Size: ~350 lines in 4 files
  - Adjusts: -
  - [x] Types and hooks of the Interfaces section (`PrivateNet`, `Internal.Networks`, `NetInternal`, `PublicNetRequest`, `PublicIPs`, `Networks`, `OnDelete`, `Update`, `PrivateMAC`, `NextPrivateMAC`); create parses `public_net` (enable_ipv4/6, ipv4/ipv6 IDs) and `networks`, calls the hooks when set, refuses a server with neither public IPv4, IPv6 nor network (422 `invalid_input`, field `public_net`); without hooks the old allocation stays; delete runs the `OnDelete` functions in its record transaction (internal/resources/servers/servers.go, internal/resources/servers/network.go)
  - [x] Seed and reconcile: a server without public IPv4 keeps its public adapter without address and is no error; the seed gets one interface per `private_net` entry with its `/32` and a route to the network range via the network gateway (first host address of `Internal.Networks[id].IPRange`, onlink), not the network's routes (internal/resources/servers/reconcile.go, internal/resources/servers/network.go)
  - [x] The apitest servers deps take the new hooks unset (internal/apitest/apitest.go)

- [x] 05-05 Networks: resource, subnets, routes
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: NET-01, NET-05, NET-06 | Tests: internal/resources/networks/networks_test.go
  - Reads: REQUIREMENTS NET-01, NET-05, NET-06; SPEC "Fixed API answers"; ARCHITECTURE "resource", "Network"; RESEARCH C1, C2, C7, R2; decisions/locations.md; DECISIONS "PUT changes only the fields present", "Status of codes outside the frozen error table"
  - Size: ~400 lines in 4 files
  - Adjusts: -
  - [x] Module, Record, `Load`, `Gateway`, `NextIP`; `POST /networks` (name, ip_range RFC1918 at least /24, subnets, routes, labels, expose_routes_to_vswitch), list (name, label_selector; sort id, name, created), get, PUT, DELETE (delete protection 423 `protected`; detaching members comes with 05-12), `change_protection`, action endpoints; VLAN from 2..4092 in `kv`, first free, released on delete (internal/resources/networks/networks.go)
  - [x] Validation and actions: subnet (type cloud/server alike, `vswitch` 422 `invalid_input` field `type` with SPEC's message, network_zone known from the catalog, inside the range, at least /30, no overlap, default the first free /24), route (destination RFC1918 or 0.0.0.0/0, no overlap with subnets, other destinations or 172.31.1.1; gateway inside the range, not the network gateway, not 172.31.1.1), `add_subnet`, `delete_subnet` (422 `service_error` while a server holds an IP in it), `add_route`, `delete_route`, `change_ip_range` (same start address, larger range only, 422 `service_error` with attached servers) (internal/resources/networks/validate.go, internal/resources/networks/actions.go)
  - [x] Status table: every code this phase uses that is missing (`must_be_unassigned`, `primary_ip_assigned`, `primary_ip_already_assigned`, `primary_ip_datacenter_mismatch`, `primary_ip_version_mismatch`, `server_has_ipv4`, `server_has_ipv6`, `floating_ip_assigned`, `server_already_attached`, `ip_not_available`, `no_subnet_available`, `networks_overlap`) (internal/api/errors.go)

- [x] 05-06 Servers: guest hook for private adapters
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: NET-02 | Tests: - (covered by test/e2e/network_test.go)
  - Reads: ARCHITECTURE "Guest integration"; CONTEXT Q1; RESEARCH C5, R5; DECISIONS "Vendor-data as multipart MIME", "Guest password hook in python3"
  - Size: ~200 lines in 1 file
  - Adjusts: -
  - [x] `hlab-guest` also reads `/hetzner/v1/metadata/private-networks` (PyYAML, a cloud-init dependency) and, per entry whose MAC matches a link, writes `/run/systemd/network/05-hlab-<mac>.network` (address `/32`, route to the network range via the gateway with `GatewayOnLink=yes`) and runs `networkctl reload` and `networkctl reconfigure <link>`; without active networkd it uses `ip addr replace` and `ip route replace ... onlink`; it removes what it set for links whose entry is gone; it touches only links with MAC `02:48:4c:2x:xx:xx` and sets no alias IPs; the vendor script writes `/etc/cloud/cloud.cfg.d/99-hlab-network.cfg` with `updates: network: when: [boot-new-instance, boot]` so each later boot renders the rewritten seed's network-config (internal/resources/servers/vendordata.go)

- [x] 05-07 Primary IPs
  - Wave: 2 | Depends on: 05-04 | Review: alone
  - Requirements: PIP-01 | Tests: internal/resources/primaryips/primaryips_test.go
  - Reads: REQUIREMENTS PIP-01; ARCHITECTURE "resource", "Network"; RESEARCH C3, R2 (primary IP, create_server, delete_server); CONTEXT Q2; DECISIONS "Public IPv4 allocation continues after the last address", "IPv6 assigned formally, not routed"
  - Size: ~400 lines in 3 files
  - Adjusts: -
  - [x] Module, Record (Hetzner's primary IP: ip, type, assignee_id, assignee_type, auto_delete default false, blocked, datacenter, location, dns_ptr, protection, labels), create (`assignee_id` or `location`, IPv4 by `ipam.NextPublic`, IPv6 formal), list (name, label_selector, ip; sort id, name, created), get, PUT (name, labels, auto_delete), DELETE (assigned: 422 `must_be_unassigned`; protection 423; releases the IPv4), `change_protection` (delete=true with auto_delete: 422 `invalid_input`), action endpoints (internal/resources/primaryips/primaryips.go)
  - [x] `assign` (server `off` else 422 `server_not_stopped`, other assignee `primary_ip_already_assigned`, `server_has_ipv4`/`server_has_ipv6`, other location `primary_ip_datacenter_mismatch`) and `unassign` (server `off`), each updating the server's `public_net` through `servers.Update` and pushing the agent; `servers.PublicIPs` implementation (new records with auto_delete true, given IDs checked as at Hetzner) and an `OnDelete` function (auto_delete records go, others become unassigned) (internal/resources/primaryips/actions.go)
  - [x] `Recover`: every server whose `public_net` carries an ID without record gets its record (assigned, auto_delete true), once and idempotently (Q2) (internal/resources/primaryips/recover.go)

- [x] 05-08 Floating IPs
  - Wave: 2 | Depends on: 05-04 | Review: alone
  - Requirements: FIP-01, FIP-02 | Tests: internal/resources/floatingips/floatingips_test.go
  - Reads: REQUIREMENTS FIP-01, FIP-02; ARCHITECTURE "resource", "hlab-agent" (FloatingIP); RESEARCH C3, C4, R2 (floating IP); decisions/locations.md
  - Size: ~380 lines in 3 files
  - Adjusts: -
  - [x] Module, Record (Hetzner's floating IP: type, ip, home_location, server, blocked, dns_ptr, protection, labels, description, name), create (`type` required, `server` or `home_location` exclusive, home location of the server by default, IPv4 by ipam, IPv6 formal), list, get, PUT, DELETE (assigned: 422 `must_be_unassigned`; protection), `change_protection`, action endpoints (internal/resources/floatingips/floatingips.go)
  - [x] `assign` on a running or stopped server (server in another network zone than the home location: 422 `invalid_input` field `server`; assigned to the same server: 422 `floating_ip_assigned`; assigned elsewhere: moves), `unassign`; the servers' `public_net.floating_ips` kept through `servers.Update`; `OnDelete` unassigns; `Contribute` fills `FloatingIPs` with the target's public IPv4 (invalid when unassigned or the server has none or is deleting); every change pushes the agent (internal/resources/floatingips/actions.go)

- [x] 05-09 Agent: metadata on private networks
  - Wave: 2 | Depends on: 05-02 | Review: alone
  - Requirements: META-01 | Tests: internal/agent/metadata_test.go
  - Reads: ARCHITECTURE "hlab-agent" (metadata); RESEARCH R4 (metadata per VRF); DECISIONS "Metadata service binds IP and MAC on its input path"
  - Size: ~180 lines in 3 files
  - Adjusts: -
  - [x] `Metadata` indexes entries by (VLAN, IP); `HandlerFor(vlan)` (internal/agent/metadata.go)
  - [x] The VRF applier keeps one listener on `169.254.169.254:80` per VRF, bound with `SO_BINDTODEVICE` to `vrf<vlan>`, serving `HandlerFor(vlan)`, closed with its VRF; the VRF gets `169.254.169.254/32` (internal/agent/vrf_linux.go, cmd/hlab-agent/main.go)

- [x] 05-10 Servers: private adapters
  - Wave: 2 | Depends on: 05-01, 05-04 | Review: alone
  - Requirements: NET-02, HOST-01 | Tests: internal/resources/servers/adapters_test.go
  - Reads: ARCHITECTURE "platform", "Network" (deny lists); RESEARCH R3; EXPERIMENTS E1; DECISIONS "Port isolation by private VLAN"
  - Size: ~300 lines in 3 files
  - Adjusts: -
  - [x] `SyncAdapters` as in the Interfaces section; deny lists from the `private_net` MACs of the other servers in the same network (not deleting) (internal/resources/servers/adapters.go)
  - [x] `createVM` attaches the public adapter and one private adapter per `private_net` entry (Segment VLAN from `Internal.Networks`, deny list); deletion needs no detach (the VM goes) (internal/resources/servers/reconcile.go)

- [x] 05-11 Networks: agent document
  - Wave: 2 | Depends on: 05-04, 05-05 | Review: alone
  - Requirements: NET-02, NET-03, NET-04 | Tests: internal/resources/networks/contribute_test.go
  - Reads: ARCHITECTURE "hlab-agent" (Network, Metadata); RESEARCH R1 (routes not given to guests)
  - Size: ~200 lines in 2 files
  - Adjusts: -
  - [x] `Contribute`: one `Network` per network (VLAN, range, gateway, routes, members with MAC, IP, alias IPs from the servers' `private_net`, deleting servers left out); per member a `Metadata` entry with the VLAN and private IP (copy of the server's public entry); `PrivateNetworks` of every server's metadata filled with name, subnet, gateway, range and interface number (internal/resources/networks/contribute.go)

- [ ] 05-12 Networks: attach, detach, alias IPs
  - Wave: 3 | Depends on: 05-05, 05-10 | Review: alone
  - Requirements: NET-02, NET-03, NET-05 | Tests: internal/resources/networks/attach_test.go
  - Reads: REQUIREMENTS NET-02, NET-03, NET-05; RESEARCH R1 (limits), R2 (attach_server_to_network, change_server_alias_ips, delete_network); decisions/locations.md
  - Size: ~400 lines in 3 files
  - Adjusts: -
  - [ ] `POST /servers/{id}/actions/attach_to_network` (network, ip, alias_ips, ip_range; subnet of the server's network zone else 422 `no_subnet_available`; `server_already_attached`, `ip_not_available`, `networks_overlap`; a fourth network 403 `resource_limit_exceeded`; more than 5 alias IPs 422 `invalid_input`), `detach_from_network`, `change_alias_ips` (replaces the list): record changes through `servers.Update` with a private MAC from `NextPrivateMAC`, then `SyncAdapters` in the action; actions lock server and network; `servers.Networks` for create (internal/resources/networks/attach.go)
  - [ ] `DELETE /networks/{id}` detaches every member first (records, then `SyncAdapters`); network `servers` shown from the members' `private_net`; server deletion needs no hook here, since membership lives on the server record (internal/resources/networks/networks.go)

- [ ] 05-13 Cloud wiring, appliance trunk and end-to-end
  - Wave: 4 | Depends on: 05-03, 05-06, 05-07, 05-08, 05-09, 05-12 | Review: alone
  - Requirements: NET-02, NET-03, NET-04, PIP-02, FIP-02, HOST-01 | Tests: test/e2e/network_test.go, test/e2e/ip_test.go, internal/resources/conventions_test.go
  - Reads: ARCHITECTURE "resource", "Cloud start and stop", "Network"; RESEARCH C6, R3; EXPERIMENTS E1 (measure routed before direct); TESTS.md header
  - Size: ~350 lines in 6 files
  - Adjusts: -
  - [ ] Modules `networks`, `primary_ips`, `floating_ips` after `servers` in `modules()`, `networks` before `volumes` so that `volumes.Contribute` also fills the `Volumes` of each member's private metadata entry (check it does; 05-11 review); `SetPublicIPs`, `SetNetworks`, `OnDelete` wiring; the appliance gets its trunk adapter (MAC `02:48:4c:00:00:02`, `Segment{Trunk: true}`) at creation and, when missing, at cloud start; the three modules in `newFullEnv` (internal/cloud/cloud.go, internal/cloud/appliance.go, internal/resources/conventions_test.go)
  - [ ] End-to-end rows of TESTS.md (test/e2e/network_test.go, test/e2e/ip_test.go, test/e2e/e2e_test.go)

- [ ] 05-14 Documentation
  - Wave: 5 | Depends on: 05-13 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC; CONTEXT "Deviations"; the decisions of this phase
  - Size: ~150 lines in 4 files
  - Adjusts: -
  - [ ] docs/user/deviations.md: formal IPv6 of primary and floating IPs, `expose_routes_to_vswitch` without effect, `vswitch` subnets refused, private MACs `02:48:4c:2x:xx:xx` instead of `86:00:00:xx:xx:xx`, private adapters configured by the hlab guest hook instead of DHCP and only on hlab-created disks, MTU 1500 on private networks, the chosen error codes where Hetzner documents none (delete_subnet, floating IP zone, limits, create without any interface, change_protection with auto_delete)
  - [ ] README.md (networks, primary and floating IPs, configuring a floating IP or alias IP in the guest), docs/dev/components.md (VRFs, trunk adapter, isolation per segment, metadata per VRF)
