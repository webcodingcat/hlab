# Phase 05 test map

Subtest names carry the requirement ID, in a group named after the
component. Unit tests of resource modules go through `apitest` (responses
validated against the frozen OpenAPI description) with `platformtest.Fake`
and a recording agent push; servers are created through `POST /servers` of
the servers module mounted beside the module under test. New groups:
`TestNetworks` (internal/resources/networks/networks_test.go, which also
runs the rows of attach_test.go and contribute_test.go), `TestPrimaryIPs`,
`TestFloatingIPs`. The row of internal/resources/servers/adapters_test.go
runs in `TestServers`; the agent row in `TestAgent`
(internal/agent/metadata_test.go).

End-to-end subtests live under `TestE2E` in `test/e2e` (build tag `e2e`,
registered in e2e_test.go). NET-02, NET-03, NET-04, HOST-01 and FIP-02 share
one fixture created once (lazily, like `sharedServer`): network N1
(`10.50.0.0/16`, subnet `10.50.1.0/24`, eu-central) with servers A
(attached at create) and B (attached while running), network N2
(`10.60.0.0/16`) with server C, all of the smallest x86 type; the fixture is
deleted when the parallel rows end. PIP-02 powers servers off and uses two
own servers through `ownServer`. Pings and SSH run from the host into the
guests. Routed paths are measured before direct ones (E1). No fixed waits:
every wait is a condition with a deadline.

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| NET-01 | create: a network with ip_range, subnets and routes is returned with id, name, ip_range, subnets (gateway = the network range's first host address), routes, labels, protection; refused with 422 `invalid_input` on the field: a range smaller than /24 or not RFC1918, a subnet outside the range, smaller than /30 or overlapping another, a route destination overlapping a subnet or `172.31.1.1`, a route gateway outside the range or equal to the network gateway; list, get, PUT (name, labels) and DELETE work; DELETE with delete protection answers 423 `protected` | unit | internal/resources/networks/networks_test.go: TestNetworks/NET-01/create | 05-05 |
| NET-01 | `add_subnet` adds a subnet (without ip_range: the first free /24), `delete_subnet` removes it and answers 422 `service_error` while a server holds an IP in it; `add_route` and `delete_route` add and remove a route, with the create validation | unit | internal/resources/networks/networks_test.go: TestNetworks/NET-01/subnets_routes | 05-05 |
| NET-01 | `change_ip_range` to a larger range with the same start address succeeds; a smaller range or another start address answers 422 `invalid_input`; with an attached server it answers 422 `service_error` and the range stays | unit | internal/resources/networks/networks_test.go: TestNetworks/NET-01/ip_range | 05-05 |
| NET-05 | a subnet needs a known `network_zone` (unknown: 422 `invalid_input` on `network_zone`) | unit | internal/resources/networks/networks_test.go: TestNetworks/NET-05/subnet | 05-05 |
| NET-05 | a server in fsn1 attaches to a subnet of eu-central; to a network with only a us-east subnet it answers 422 `no_subnet_available`, also on server create with `networks`; `ip_range` picks the subnet | unit | internal/resources/networks/attach_test.go: TestNetworks/NET-05/attach | 05-12 |
| NET-06 | a subnet of type `vswitch` on create and on `add_subnet` answers 422 `invalid_input`, field `type`, message `vswitch subnets are not supported by hlab`, and nothing is stored | unit | internal/resources/networks/networks_test.go: TestNetworks/NET-06 | 05-05 |
| NET-02 | `attach_to_network` gives the lowest free host address of the subnet (never gateway, subnet or broadcast address), an explicit free `ip` is taken, a used one answers 422 `ip_not_available`; a second attach to the same network answers 422 `server_already_attached`, a network overlapping one of the server's networks 422 `networks_overlap`, a fourth network 403 `resource_limit_exceeded`; the server's `private_net` and the network's `servers` show it; `detach_from_network` removes both; deleting the network detaches its servers | unit | internal/resources/networks/attach_test.go: TestNetworks/NET-02/attach | 05-12 |
| NET-02 | `SyncAdapters` on the fake platform: an attached network hot-adds an adapter with MAC `02:48:4c:2x:xx:xx` on the network's VLAN to the running VM, every member's deny list then holds exactly the MACs of the other members, the seed's network-config gets the `/32` with an onlink route to the network range via the gateway and no network route; a detach removes the adapter and the MAC from the others' deny lists | unit | internal/resources/servers/adapters_test.go: TestServers/NET-02 | 05-10 |
| NET-02 | A and B (B attached while running) get their private IPs as the API shows them and ping each other on them; C in N2 cannot reach A's private IP | e2e | test/e2e/network_test.go: TestE2E/NET-02 | 05-13 |
| NET-03 | `change_alias_ips` replaces the server's alias IPs (inside a subnet of the network, free; more than 5: 422 `invalid_input` on `alias_ips`), and the agent document lists them for the member | unit | internal/resources/networks/attach_test.go: TestNetworks/NET-03 | 05-12 |
| NET-03 | an alias IP set through the API and configured by hand on B answers pings from A | e2e | test/e2e/network_test.go: TestE2E/NET-03 | 05-13 |
| NET-04 | the agent document has one `Network` per network with VLAN, range, gateway, routes and members (MAC, IP, alias IPs), a deleting server left out; each member has a metadata entry with its VLAN and private IP; the servers' `private_networks` metadata carries name, subnet, gateway and range | unit | internal/resources/networks/contribute_test.go: TestNetworks/NET-04 | 05-11 |
| NET-04 | with a route `10.99.0.0/24` via B's private IP and `10.99.0.5/32` on B's loopback, A reaches `10.99.0.5` and the answer comes from B | e2e | test/e2e/network_test.go: TestE2E/NET-04 | 05-13 |
| HOST-01 | A reaches B on its public and private IP through the appliance; after A adds an onlink `/32` route to B on its public and on its private interface, A's neighbour entry for B never holds B's MAC (only the appliance's, or none) and B sees no frame from A's MAC addressed to B directly | e2e | test/e2e/network_test.go: TestE2E/HOST-01 | 05-13 |
| PIP-01 | create with `location` (IPv4 from the public range, IPv6 formal) or with `assignee_id`; `auto_delete` defaults to false; list, get, PUT (name, labels, auto_delete); DELETE of an assigned IP answers 422 `must_be_unassigned`, of an unassigned one removes it; delete protection answers 423 `protected`; `change_protection` with delete=true on an auto_delete IP answers 422 `invalid_input` | unit | internal/resources/primaryips/primaryips_test.go: TestPrimaryIPs/PIP-01/create_delete | 05-07 |
| PIP-01 | `assign` and `unassign` on a running server answer 422 `server_not_stopped`; on a stopped one they set and clear `assignee_id` and the server's `public_net`; assigning an IP assigned elsewhere answers 422 `primary_ip_already_assigned`, to a server that has an IPv4 422 `server_has_ipv4`, to a server in another location 422 `primary_ip_datacenter_mismatch` | unit | internal/resources/primaryips/primaryips_test.go: TestPrimaryIPs/PIP-01/assign | 05-07 |
| PIP-01 | server create makes IPv4 and IPv6 records with `auto_delete: true`; with `public_net.ipv4` it takes that record (assigned elsewhere: 422 `primary_ip_assigned`, IPv6 given as ipv4: 412 `primary_ip_version_mismatch`); `enable_ipv4: false` gives no IPv4; neither IP nor network answers 422 `invalid_input`; deleting the server deletes its auto_delete records and leaves the others unassigned | unit | internal/resources/primaryips/primaryips_test.go: TestPrimaryIPs/PIP-01/server | 05-07 |
| PIP-01 | `Recover` gives a server stored with primary IP IDs but no records (phase 04 state) its two records, assigned, auto_delete true; a second run changes nothing | unit | internal/resources/primaryips/primaryips_test.go: TestPrimaryIPs/PIP-01/recover | 05-07 |
| PIP-02 | with both servers off, the IPv4 of server X unassigned and assigned to server Y (whose own IPv4 was unassigned), Y powered on answers SSH on that address with Y's hostname | e2e | test/e2e/ip_test.go: TestE2E/PIP-02 | 05-13 |
| FIP-01 | create needs `type`; `server` and `home_location` together answer 422 `invalid_input`; with `server` the home location is the server's location and it is assigned; list, get, PUT; DELETE of an assigned IP answers 422 `must_be_unassigned`; delete protection answers 423 `protected`; deleting the assigned server unassigns it | unit | internal/resources/floatingips/floatingips_test.go: TestFloatingIPs/FIP-01/create_delete | 05-08 |
| FIP-01 | `assign` to a running server sets `server` and the server's `public_net.floating_ips`; to the same server again answers 422 `floating_ip_assigned`; to a server outside the home location's network zone 422 `invalid_input`; `unassign` clears both | unit | internal/resources/floatingips/floatingips_test.go: TestFloatingIPs/FIP-01/assign | 05-08 |
| FIP-02 | the agent document's `FloatingIPs` entry targets the assigned server's public IPv4, after reassignment the new server's, unassigned an invalid target | unit | internal/resources/floatingips/floatingips_test.go: TestFloatingIPs/FIP-02 | 05-08 |
| FIP-02 | with the floating IP configured on A and B, it answers SSH with A's hostname while assigned to A and with B's hostname after reassignment to B, without a change on A | e2e | test/e2e/ip_test.go: TestE2E/FIP-02 | 05-13 |
| META-01 | for two servers with the same private IP in two VLANs, `HandlerFor(vlan)` answers each server's metadata by VLAN and source IP; an unknown IP on a VLAN answers 404 | unit | internal/agent/metadata_test.go: TestAgent/META-01/private_vlan | 05-09 |
| API-06 | `/networks`, `/primary_ips` and `/floating_ips` filter by `name` and sort by `id:desc`, one case each | unit | internal/resources/conventions_test.go: TestResources/API-06 | 05-13 |
