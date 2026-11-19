# Phase 05 context

Confirmed with the user at the start of the phase (2026-11-19).

## Assumptions confirmed

- New modules `internal/resources/networks`, `primary_ips`, `floating_ips`, each one package, one line in `Cloud.modules()`, each a `NetContributor`; servers get `attach_to_network`, `detach_from_network`, `change_alias_ips` and fill `private_net`.
- NET-01: create, update, delete, `add_subnet`, `delete_subnet`, `add_route`, `delete_route`, `change_ip_range` (grow only), `change_protection`, following the frozen OpenAPI description; `expose_routes_to_vswitch` is stored and has no effect; subnet types `cloud` and `server` behave the same; `vswitch` gets the fixed `invalid_input` answer (NET-06).
- Corrected: `DELETE /networks/{id}` detaches attached servers automatically ("Attached resources will be detached automatically"); only delete protection blocks it.
- Corrected: `delete_subnet` is refused while resources are attached to the subnet; `delete_route` has no such lock (only `conflict` during a running change).
- NET-05: a subnet carries a `network_zone`; a server attaches only to a subnet in its location's zone, otherwise Hetzner's error.
- Private IPs: Hetzner's allocation, the lowest free host address of the subnet, never gateway (first address of the range), subnet or broadcast address; `attach_to_network` takes `ip`, `alias_ips`, `ip_range`; server create takes `networks`.
- One VLAN per network from 2..4092 in `kv`, first free, released on delete; one VRF per network on the appliance on a VLAN subinterface of a new second appliance adapter (trunk 2..4092); cloud start adds the trunk adapter to an existing appliance if missing.
- Platform: `Segment` gets a trunk mode; new `SetNICDeny(ctx, vm, mac, deny)` and hot-add/remove of private adapters on running VMs; `Metrics(ctx, vm)` is removed from interface, hyperv and fake (Q7).
- Deny lists: whenever a network's membership changes, every member's private adapter gets the MACs of the other members (networks module reconcile).
- Corrected guest config: per private adapter only its `/32` and the route to the network range via the network gateway. The network's routes are not given to the guest; they act only in the agent's VRF. A default route via a NAT gateway is set by the user, as at Hetzner. The seed ISO is rebuilt from state on every addressing change.
- Agent: per network a VRF with the gateway address, `/32` routes to members and their alias IPs (NET-03), routes to route destinations via the gateway server (NET-04), no default route, no leak between VRFs (NET-02); proxy ARP on and redirects off on the subinterfaces; metadata service answers per VRF; isolation backstop gets MAC/IP pairs per VLAN including alias IPs.
- Primary IPs are real records: server create makes IPv4/IPv6 primary IPs with `auto_delete: true`, or uses `public_net.ipv4`/`ipv6` IDs, or `enable_ipv4/ipv6: false`; a server with neither public IPv4 nor a network is refused as at Hetzner; assign/unassign need the server off; server delete removes `auto_delete` primary IPs and unassigns the others; IPv4 from `ipam.NextPublic`; standalone IPv6 /64 is `2001:db8:<0x80000000 | primary IP ID>::/64` (formal).
- Floating IPs: `home_location` (default: assigned server's location); IPv4 from the public pool via ipam; IPv6 formal `2001:db8:<0xC0000000 | floating IP ID>::/64`; assign/unassign on running servers; agent routes the `/32` onlink via the target's public IPv4 on the PVLAN interface; the guest configures the address itself (FIP-02); location/zone rules from Hetzner's docs.
- Every module change triggers `PushAgent`; project delete removes networks, floating IPs and primary IPs through `Deleter`.
- HOST-01 is an e2e row: a hand-made route on a guest's public and private interface to a peer does not reach it; the routed path still works (routed first, then direct, per E1).
- Deviations in `docs/user/deviations.md`: formal IPv6 for primary and floating IPs; `expose_routes_to_vswitch` has no effect; subnet type `vswitch` is refused; private MACs are `02:48:4c:2x:xx:xx` where Hetzner uses `86:00:00:xx:xx:xx`.

## Answers

- Q1 (guest picks up network changes): live, without reboot. The `hlab-guest` timer configures attached and detached private adapters from the metadata service, like Hetzner's hc-utils; the seed is rebuilt too so the next boot is right. The timer touches only adapters hlab manages and sets only their primary private IP with the route to the network range. It does not configure alias IPs (per `attach_to_network`, the user sets them in the guest). Primary IPs are assigned only to stopped servers (OpenAPI), so they need nothing of their own.
- Q2 (servers without primary IP records): at cloud start create the missing records idempotently, assigned, `auto_delete: true`.
- Q3 (private adapter MAC): `02:48:4c` plus nibble `0x2` plus a 20-bit per-attachment counter; deviations.md notes Hetzner's `86:00:00:xx:xx:xx`.

## Research before planning

- How Hetzner brings a private adapter into a running guest (hc-utils, hotplug, DHCP, which routes come via DHCP), the form of Hetzner's private MACs, and whether Hetzner distributes a network's routes to guests. Where research contradicts the answers above, the implementation follows the research and records it in DECISIONS.md.
