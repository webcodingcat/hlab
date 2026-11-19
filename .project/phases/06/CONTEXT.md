# Phase 06 context

Confirmed assumptions and answers from the discussion at the start of the
phase. Where RESEARCH.md contradicts an assumption, the research wins and
the difference is recorded in DECISIONS.md.

## Confirmed assumptions

- New module `internal/resources/firewalls`, built like floatingips: list, create, get, update, delete, actions `set_rules`, `apply_to_resources`, `remove_from_resources`, action endpoints via `resactions.RoutesFor`; no Reconciler or Deleter; project deletion removes the rows.
- Record in Hetzner shape: `rules[]` (description, direction, protocol, port, source_ips, destination_ips) and `applied_to[]` (type `server` or `label_selector`); `applied_to_resources` computed from the project's servers on every read.
- Validation as at Hetzner: port required for tcp/udp, forbidden for icmp/esp/gre, single port, range `a-b` or `any`; `source_ips` required for `in`, `destination_ips` for `out`, as CIDR; IPv6 accepted; errors `invalid_input` with `details.fields` naming `rules[i].<field>`.
- Actions `apply_firewall`, `set_firewall_rules`, `remove_firewall`; each ends `success` once the agent document is pushed, `error` if the push fails.
- Server `public_net.firewalls` becomes `[{id, status}]` with direct and selector firewalls; status always `applied`.
- Servers reach the module through a hook `srv.SetFirewalls(fw)`: shown firewalls, `firewalls` in the server create request, and `OnDelete` removing a deleted server from every `applied_to`.
- FW-05: `PUT /servers/{id}` pushes the agent document after commit when labels changed (failed push logged); selectors resolved on every push in `firewalls.Contribute`; a selector matches only servers of the same project with a public IPv4.
- Agent: new Applier `Firewall` with its own nftables table (e.g. `inet hlab_fw`), replaced atomically, runs after Isolation, leaves `inet hlab` untouched.
- Traffic semantics as at Hetzner: stateful; inbound dropped unless the union of `in` rules allows it; outbound allowed until any applied firewall has an `out` rule; ICMP needs a rule; IPv6 rules without effect; a server's addresses are its public IPv4 and assigned floating IPs.
- FW-04: only packets entering or leaving the appliance's public interface (forward chain) are matched; private-network traffic is never matched.
- Tests: pure, platform-independent compile function from `agentapi.Config` to a per-server plan, unit-tested for FW-04, merging several firewalls and the outbound default; nftables translation is glue.
- e2e rows FW-02 and FW-03 only; success criterion 2 proved at unit level as FW-05.
- Documentation: `deviations.md` (IPv6 without effect, status always `applied`, appliance services); D2, D3, D6 unchanged.

## Corrections by the user

- Request fields are `apply_to` (on `POST /firewalls` and `apply_to_resources`) and `remove_from` (on `remove_from_resources`); `applied_to` exists only in responses. All three actions answer `201` with `actions[]`.
- A firewall has at most 50 rules (validation). Hetzner's limit of 500 rules per server is a deviation in `deviations.md` (not enforced).
- Error codes, as in the spec:
  - `POST /firewalls`: `422 server_already_added`, `422 incompatible_network_type`, `404 firewall_resource_not_found`
  - `apply_to_resources`: `404 firewall_resource_not_found`, `422 firewall_already_applied`, `422 incompatible_network_type`, `422 private_net_only_server`
  - `remove_from_resources`: `404 firewall_resource_not_found`, `422 firewall_managed_by_label_selector` (server attached only through a selector)
  - `DELETE /firewalls/{id}`: `422 resource_in_use`

## Answer to Q1 (outbound rules and appliance services)

- Resolvers 185.12.64.1/2 are filtered by `out` rules as at Hetzner if the research confirms Hetzner does so; otherwise they are never filtered.
- The metadata service 169.254.169.254 and the API proxy 198.19.0.1 are never filtered by hlab, whatever Hetzner does (`hlab-guest` needs metadata for network changes and password reset). Where Hetzner differs, it goes to `deviations.md`.

## Where the research changed the above (research wins, see DECISIONS.md)

- `POST /firewalls` with an unknown server answers `422 firewall_resource_not_found` (not 404); `apply_to_resources` and `remove_from_resources` answer 404.
- Hetzner's 500 limit is 500 effective rules (one unique IP prefix per direction) per firewall, not per server; hlab does not enforce it and `deviations.md` says so. The 50 rules per firewall stay validated.
- Hetzner never filters DNS resolver and metadata traffic; in hlab both are appliance-local (input path), so the forward-only firewall never filters resolvers, metadata or the API proxy. Q1 resolves to "never filtered"; no deviation needed for them.
- `remove_from` accepts `label_selector` entries as well as servers.
