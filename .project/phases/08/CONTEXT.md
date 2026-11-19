# Phase 08 context

Confirmed by the user at the start of the phase. Decisions are recorded in
DECISIONS.md (Q9 to Q13 and the phase 08 entries of 2026-11-19).

## Assumptions (confirmed)

- Modules `loadbalancers`, `loadbalancertypes` (`GET /load_balancer_types` from the catalog), `certificates`, package `internal/ca`, HAProxy in hlab-agent, a real `hostnet.TrustCA`.
- load_balancers serves every endpoint and action of the OpenAPI description; it is a NetContributor, a Deleter and Scheduled.
- Unique name per project (409 `uniqueness_error`); the type exists; `location` or `network_zone` (zone alone picks its first location in catalog order); type limits enforced; `max_connections` only as HAProxy `maxconn`.
- Public IPs: `public_net.ipv4`/`ipv6` take primary IP IDs only with the public interface on (422 `primary_ip_assigned`, 422 `primary_ip_blocked`, 422 `primary_ip_location_mismatch`, 412 `primary_ip_version_mismatch`); without them a system-managed IPv4 from the public pool, no primary IP, deleted with the LB.
- A primary IP held by an LB shows `assignee_type` "load_balancer" and the LB ID; `assign` on it gives 422 `primary_ip_already_assigned`, `unassign` 422 `invalid_input`; LB deletion frees or deletes it by `auto_delete`; in deviations.md.
- The LB adds its own PTR entries to `Config.PTR` through `internal/rdns` (LB-09).
- Disabling the public interface keeps the IP and removes the public veth, route and listeners; it needs a network (422 `load_balancer_not_attached_to_network`) and no targets without `use_private_ip` (422 `targets_without_use_private_ip`); while off, a public target gives 422 `load_balancer_public_interface_disabled`.
- At most one network per LB (422 `load_balancer_already_attached`, `ip_not_available`, `no_subnet_available`); its IP given or from ipam; one veth into the network's VRF with the conntrack-zone rule of its VLAN.
- Server targets: public IPv4, or with `use_private_ip` the IP in the LB's network; label selectors resolve when the document is built and show as nested `targets`.
- add_target: 422 `resolve_cloud_private_targets_error`, `resolve_cloud_public_targets_error`, `target_already_defined`; the vswitch codes never occur; a target in another network zone gives 422 `invalid_input`.
- IP targets: any unicast IPv4 except the transit /30 and the LB's own IP, over the public path; in deviations.md.
- `source_port_already_used`: 412 on add_service, 422 on update_service (per-call status on `api.Error`); `redirect_http` takes port 80.
- change_type over the new type's limits: 422 `invalid_load_balancer_type`; add_service/add_target over a limit: 403 `resource_limit_exceeded`.
- Services: omitted health check is tcp/15/10/3; health protocol `https` accepted; GET always returns `sticky_sessions`, `redirect_http`, `health_check.http.tls` as booleans; `cookie_lifetime: 0` and an empty cookie name in update_service keep the default.
- Appliance: one netns and one HAProxy master-worker per LB; public veth routed through the root namespace so firewalls apply; reload only on config change, after `haproxy -c`, through the master CLI.
- HAProxy mapping: roundrobin/leastconn; sticky cookie `HCLBSTICKY`, 300 s; redirect_http as a port-80 frontend with 301; `proxyprotocol` as `send-proxy-v2` (Q13); health checks mapped 1:1, status_codes default `["2??","3??"]`.
- Packages: five Debian 13 .debs in `appliance-debs.json` (haproxy from the debian-security snapshot URL), hashes recomputed before commit; installed only if missing, Debian's own service disabled.
- Health and metrics: a job every 10 s reads `show stat` through the agent; health `unknown` until the first check; counters that drop count as a reset; metrics open_connections, connections_per_second, requests_per_second, bandwidth.in, bandwidth.out with the server metrics' retention and point limits.
- LB IPv6: `2001:db8:<0x80000000 | LB ID>::1`, with an IPv6 primary IP `<its /64>::1` (Q11).
- Uploaded certificates: chain and key stored, key never returned; fingerprint, domains and validity from the leaf; no expiry or chain-order check; bad PEM or mismatching key: 422 `invalid_input`; `used_by` lists the LBs.
- Managed certificates: start `issuance pending, renewal unavailable`; every domain must equal or lie below a zone of the project (wildcards allowed); success `completed`/`scheduled`; failure `failed` with `dns_zone_not_found` or `dns_zone_is_secondary_zone` in `status.error` and on the action; retry when not failed: 422 `invalid_input`; delete while used: 422 `resource_in_use`.
- Renewal: hourly job, renews 30 days before expiry, `scheduled` → `pending` → `scheduled` or `failed`; LBs using it get their document pushed again; tested with the fake clock.
- hlab CA: ECDSA P-256, 10 years, created at the first cloud start in `Paths.CA` (`ca.key` SYSTEM only); leaves ECDSA P-256, 90 days, SANs = domain names, CN = first domain, serverAuth.
- CRL (Q12): every leaf names `http://api.hetzner.internal/hlab/v1/ca.crl`; hlabd serves an empty signed CRL, renewed at every cloud start and weekly, `nextUpdate` 8 days ahead; the root CA has no CDP; the URL goes into the documentation and deviations.md.
- `GET /mgmt/v1/ca.pem` and `hlab ca export <file>` (CERT-05); `hlab ca trust on|off` with `--output json` (Q10).
- `PUT /mgmt/v1/trust {"on":bool}` calls `hostnet.TrustCA` and stores `[certificates] trust_ca`; cloud start re-applies it while on.
- Windows trust: LocalMachine\Root through the Go Cert* calls, found by thumbprint.
- WSL trust (Q9): hlabd runs `wsl.exe` with the console user's token for each WSL2 distro, only while trust is on, and removes the CA the same way when switched off; nobody logged on: pending until the next logon, cloud start or switch; distros without `update-ca-certificates` (e.g. docker-desktop) skipped and logged; fallback if it fails in the first CERT-04 run: a scheduled task in the user's context.
- E2E: success criteria, LB-02/03/04/05 and CERT-04 share one environment (a network, two servers with a small web server from cloud-init, one LB); CERT-04 switches trust off at the end.
- Documentation: deviations (IP targets, LB-held primary IPs, formal IPv6, hlab CA instead of Let's Encrypt, CRL URL, PROXY v2 unverified, metrics resolution), the `hlab ca` commands, the `trust_ca` key, and the README section on trust and its risk.
