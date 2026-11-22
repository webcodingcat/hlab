# Phase 13 research: hcloud CLI, OpenTofu provider and Ansible collection against hlab

Method note: this run had no shell, so `hcloud <type> --help`, `ansible-doc` and `tofu` were NOT executed. Everything below comes from the tagged sources and docs on GitHub (hcloud CLI v1.70.2 `docs/reference/manual/*`, terraform-provider-hcloud v1.70.2 `docs/` and `internal/`, hetzner.hcloud 7.2.0 `plugins/`). Where the text is derived from source code summaries it is marked "(source)". The phase should run the three local commands once to confirm the flag lists (open items at the end).

## Versions and maintenance

| Tool | Pinned in SPEC | Current stable | Last release |
|---|---|---|---|
| hcloud CLI | v1.70.2 | v1.70.2 | 2026-10-23 (v1.70.1 2026-10-06, v1.70.0 2026-10-05) |
| terraform-provider-hcloud | 1.70.2 | 1.70.2 | 2026-10-26 (1.70.1 2026-10-23, 1.70.0 2026-10-05) |
| hetzner.hcloud collection | 7.2.0 | 7.2.0 | 2026-10-05 (7.1.0 2026-09-17, 7.0.1 2026-09-11) |

All three are maintained; pins are the current releases. Sources: https://github.com/hetznercloud/cli/releases, https://github.com/hetznercloud/terraform-provider-hcloud/releases, https://github.com/ansible-collections/hetzner.hcloud/releases. Compatibility: no conflicts known; the CLI and the provider both use hcloud-go v2 (same action polling), the collection uses its own Python client (hcloud-python).

## 1. hcloud CLI v1.70.2 command tree

Source: https://github.com/hetznercloud/cli/tree/v1.70.2/docs/reference/manual

Global flags on every command: `--config`, `--context`, `--debug`, `--debug-file`, `--endpoint` (Cloud API, default `https://api.hetzner.cloud/v1`), `--hetzner-endpoint` (Hetzner API, default `https://api.hetzner.com/v1`), `--http-timeout` (0 = none), `--no-experimental-warnings`, `--poll-interval` (default 500ms), `--quiet`. Environment: `HCLOUD_TOKEN`, `HCLOUD_ENDPOINT`, `HCLOUD_HETZNER_ENDPOINT` (the latter is the CLI's own name; the flag exists, verify the env name locally). Most list commands: `-o/--output`, `-l/--selector`, `-s/--sort`; create/describe: `-o json|yaml`.

Top level: `all list`, `api`, `certificate`, `completion`, `config`, `context`, `datacenter`, `firewall`, `floating-ip`, `image`, `iso`, `load-balancer`, `load-balancer-type`, `location`, `network`, `placement-group`, `primary-ip`, `server`, `server-type`, `ssh-key`, `storage-box`, `storage-box-type`, `version`, `volume`, `zone`.

Subcommands by type (label commands `add-label`/`remove-label` exist on every resource type, also `zone rrset`, `storage-box`, `storage-box snapshot`):

- certificate: create, delete, describe, list, retry, update (+labels). `create` flags: `--name`, `--cert-file`, `--key-file`, `--type uploaded|managed`, `--domain` (managed), `--label`.
- datacenter: describe, list (DEPRECATED, 410 after 2026-10-01).
- firewall: add-rule, apply-to-resource (`--type server|label_selector`, `--server`, `-l/--label-selector`), create (`--name`, `--rules-file`, `--label`), delete, delete-rule, describe, list, remove-from-resource, replace-rules, update.
- floating-ip: assign, create (`--type` required, `--name`, `--home-location`, `--server`, `--description`, `--enable-protection`, `--label`), delete, describe, disable/enable-protection, list, set-rdns, unassign, update.
- image: delete, describe, disable/enable-protection, list, update.
- iso: describe, list.
- load-balancer: add-service, add-target, attach-to-network, change-algorithm, change-type, create (`--name`, `--type` required, `--algorithm-type`, `--location`, `--network-zone`, `--network`, `--primary-ipv4`, `--primary-ipv6`, `--label`, `--enable-protection`), delete, delete-service, describe, detach-from-network, disable/enable-protection, disable/enable-public-interface, list, metrics, remove-target, set-rdns, update, update-service. `add-target`: `--server` | `--label-selector` | `--ip`, plus `--use-private-ip`. `add-service`: `--protocol` (required), `--listen-port`, `--destination-port`, `--proxy-protocol`, `--http-*` (certificates, sticky sessions, cookie name/lifetime, redirect-http, timeout-idle), `--health-check-protocol/-port/-interval/-timeout/-retries`, `--health-check-http-domain/-path/-response/-status-codes/-tls`.
- load-balancer-type, location, server-type, storage-box-type: describe, list.
- network: add-route, add-subnet, change-ip-range, create, delete, describe, disable/enable-protection, expose-routes-to-vswitch, list, list-members, remove-route, remove-subnet, update.
- placement-group: create, delete, describe, list, update.
- primary-ip: assign, create, delete, describe, disable/enable-protection, list, set-rdns, unassign, update.
- server: add-to-placement-group, attach-iso, attach-to-network, change-alias-ips, change-type, create, create-image, delete, describe, detach-from-network, detach-iso, disable/enable-backup, disable/enable-protection, disable/enable-rescue, ip, list, metrics, poweroff, poweron, reboot, rebuild, remove-from-placement-group, request-console, reset, reset-password, set-rdns, shutdown, ssh, update. `create` flags: `--name`, `--type`, `--image` (required), `--location` (the `--datacenter` flag is gone since v1.67.0), `--placement-group`, `--network` (repeatable), `--firewall` (repeatable), `--primary-ipv4/-ipv6`, `--without-ipv4/-ipv6`, `--volume`, `--automount`, `--ssh-key`, `--user-data-from-file`, `--start-after-create`, `--enable-protection`, `--enable-backup`, `--label`, `--allow-deprecated-image`.
- ssh-key: create, delete, describe, list, update.
- storage-box (api.hetzner.com): add-label ... see tree: change-type, create, delete, describe, disable/enable-protection, disable/enable-snapshot-plan, folders, list, reset-password, rollback-snapshot, update, update-access-settings, `snapshot` (create, delete, describe, list, update), `subaccount` (change-home-directory, create, delete, describe, list, reset-password, update, update-access-settings).
- volume: attach, create (`--name`, `--size` required, `--location` or `--server`, `--automount`, `--format ext4|xfs`, `--label`, `--enable-protection`), delete, describe, detach, disable/enable-protection, list, resize, update.
- zone: add-records, change-primary-nameservers, change-ttl, create, delete, describe, disable/enable-protection, export-zonefile, import-zonefile, list, remove-records, set-records, `rrset` (add-records, change-ttl, create (`--name`, `--type` required, `--record` repeatable or `--records-file`, `--ttl`, `--label`), delete, describe, disable/enable-protection, list, remove-records, set-records, labels).
- config/context/completion/version/api: local only (`api` sends a raw request to the Cloud API; useful as a generic client).

### Commands that reach endpoints hlab does not serve (410 or out of scope)

- `hcloud datacenter list|describe`: `GET /datacenters[/{id}]` -> 410 Gone as of 2026-10-01 (CLI help text says so). Expect a CLI error; no fallback.
- `hcloud server request-console`: `POST /servers/{id}/actions/request_console` (IDEA.md lists the console as the exception; see decisions/server-console.md).
- `hcloud server metrics`, `hcloud load-balancer metrics`: `GET .../metrics`. Not in the example flow; decide served or 404.
- `hcloud server reset-password`, `rebuild`, `create-image`, `enable-rescue`, `attach-iso`, `change-type`, `enable-backup`: server actions; check each is served.
- Storage box, storage-box-type commands: use `--hetzner-endpoint` (api.hetzner.com), a different API. Note `hcloud all list` also lists Storage Boxes and Zones: so `all list` needs the Hetzner API endpoint reachable, or hlab must answer on it. Unclear how `all list` behaves if the Hetzner endpoint is not served; test it.
- `hcloud server ssh`, `server ip`: no API beyond GET server.
- Client side effect: after 1.67.0 the CLI does not call datacenters during `server create/describe/list` (flag and column removed), so normal server flows never hit the 410.

## 2. OpenTofu provider hetznercloud/hcloud 1.70.2

Sources: https://github.com/hetznercloud/terraform-provider-hcloud/tree/v1.70.2/docs (resources, data-sources), CHANGELOG.md.

Provider config (verify names locally): `token` (env `HCLOUD_TOKEN`), `endpoint` (env `HCLOUD_ENDPOINT`), `poll_interval`, `poll_function`; the Hetzner API endpoint for storage boxes has its own setting (name not confirmed).

Resources (29): certificate, firewall, firewall_attachment, floating_ip, floating_ip_assignment, load_balancer, load_balancer_network, load_balancer_service, load_balancer_target, managed_certificate, network, network_route, network_subnet, placement_group, primary_ip, rdns, server, server_network, snapshot, ssh_key, storage_box, storage_box_snapshot, storage_box_subaccount, uploaded_certificate, volume, volume_attachment, zone, zone_record, zone_rrset.

Data sources (44): certificate(s), datacenter(s), firewall(s), floating_ip(s), image(s), load_balancer(s), load_balancer_type(s), location(s), network, network_members, networks, placement_group(s), primary_ip(s), server, servers, server_type(s), ssh_key(s), storage_box(es), storage_box_snapshot(s), storage_box_subaccount(s), storage_box_type(s), volume(s), zone, zones, zone_rrset(s).

Required arguments of the ones asked about:

- `hcloud_zone`: `name`, `mode` (`primary`/`secondary`). Optional `ttl`, `labels`, `delete_protection`, `primary_nameservers` (must be omitted for primary, required for secondary). Read-only `id` (number), `registrar`, `authoritative_nameservers.assigned`. Flow (source): `POST /zones` (returns zone + action), `change_protection` if set, wait for actions, `GET /zones/{id}`; delete `DELETE /zones/{id}` then wait for action; update uses `change_ttl`, `change_primary_nameservers`, `change_protection`, then `PUT /zones/{id}`.
- `hcloud_zone_rrset`: `zone` (ID or name), `name`, `type`, `records` (set of `{value, comment?}`). Optional `ttl`, `labels`, `change_protection`. `id` is a string. SOA RRSets are imported into state, not created, and not deleted. TXT values must be quoted (helper `provider::hcloud::txt_record`).
- `hcloud_zone_record`: `zone`, `name`, `type`, `value`; optional `comment`. Does not manage TTL/labels/protection (belong to the RRSet); docs recommend `hcloud_zone_rrset`. Import needs Terraform 1.12 identity (maybe unsupported by OpenTofu).
- `hcloud_rdns`: `dns_ptr`, `ip_address`, and exactly one of `server_id`, `primary_ip_id`, `floating_ip_id`, `load_balancer_id`. ID format `s-<id>-<ip>` / `p-` / `f-` / `l-`.
- `hcloud_snapshot`: `server_id`; optional `description`, `labels`. Creates an image of type snapshot (`POST /servers/{id}/actions/create_image`).
- `hcloud_managed_certificate`: `name`, `domain_names`; optional `labels`. Computed `certificate`, `fingerprint`, `not_valid_before/after`. See decisions/managed-certificates.md.
- `hcloud_uploaded_certificate`: `name`, `certificate`, `private_key`; optional `labels`. Computed `domain_names`, `fingerprint`, `not_valid_*`.
- `hcloud_firewall_attachment`: `firewall_id`; optional `server_ids`, `label_selectors`. Only one per firewall. Alternative: `firewall_ids` in `hcloud_server` (applied in the create request, no extra call).
- `hcloud_floating_ip_assignment`: `floating_ip_id`, `server_id`. Delete = unassign.
- `hcloud_volume_attachment`: `volume_id`, `server_id`; optional `automount`. Delete = detach. (`hcloud_volume` itself also takes `server_id` and `location`.)
- `hcloud_server_network`: `server_id`, one of `subnet_id`/`network_id`; optional `ip`, `alias_ips`. `hcloud_server` has an inline `network {}` block (same args); the provider attaches after the server is created, waits for each action, then powers on if it had been created offline.
- `hcloud_load_balancer_network`: `load_balancer_id`, one of `subnet_id`/`network_id`; optional `ip`, `enable_public_interface`.
- `hcloud_load_balancer_target`: `type` (server|label_selector|ip), `load_balancer_id`; `server_id` | `label_selector` | `ip`; `use_private_ip` (server/label_selector only).
- `hcloud_load_balancer_service`: `load_balancer_id`, `protocol` (tcp|http|https); for tcp `listen_port` and `destination_port` are required; optional `proxyprotocol`, `http {}`, `health_check {protocol, port, interval, timeout, retries, http{}}`. Import id `<lb-id>__<listen-port>`.

### Datacenter handling against hlab (410)

- `hcloud_datacenter` / `hcloud_datacenters` data sources: deprecated since 1.66.0 ("removed after 1 Oct. 2026"). Source calls `Datacenter.GetByID/GetByName/All`; no special 410 handling, the error goes through the generic API error diagnostics. Expect a plan/apply failure with the API error (HTTP 410). The e2e harness should use `hcloud_location(s)`.
- `hcloud_server.datacenter` and `hcloud_primary_ip.datacenter`: deprecated 1.58.0, removed in 1.67.0 (CHANGELOG). Docs page for server still lists it as deprecated, but per source the validator always returns a "Removed Attribute" error, so setting it fails at validation, before any API call (source summary; not tested). Use `location`.
- Exported `datacenter` attribute on servers is no longer filled (not updated once the API stops returning it). hlab not serving `datacenter` fields is consistent.
- No other resource or data source calls `/datacenters` (server_type data sources use locations since 1.61.0 / 1.68.0).
- Provider has no 410-specific retry or fallback.

## 3. Ansible collection hetzner.hcloud 7.2.0

Sources: https://github.com/ansible-collections/hetzner.hcloud/tree/7.2.0/plugins, CHANGELOG.rst. `ansible-doc -l hetzner.hcloud` not run.

Modules (from `plugins/modules`, prefix `hetzner.hcloud.`): certificate, certificate_info, datacenter_info, firewall, firewall_info, firewall_resource, floating_ip, floating_ip_info, image_info, iso_info, load_balancer, load_balancer_info, load_balancer_network, load_balancer_service, load_balancer_target, load_balancer_type_info, location_info, network, network_info, network_member_info, placement_group, primary_ip, primary_ip_info, rdns, route, server, server_info, server_network, server_type_info, ssh_key, ssh_key_info, storage_box, storage_box_info, storage_box_snapshot(_info), storage_box_subaccount(_info), storage_box_type_info, subnetwork, volume, volume_attachment, volume_info, zone, zone_info, zone_rrset, zone_rrset_info. (The GitHub listing may be truncated; compare with `ansible-doc -l`.) There is no module for snapshots/images creation beyond `image_info` and no module for `request_console`; no zone_record module (use `zone_rrset`).

Common module options (doc fragment): `api_token` (env `HCLOUD_TOKEN`, required), `api_endpoint` (alias `endpoint`, env `HCLOUD_ENDPOINT`, default `https://api.hetzner.cloud/v1`), `api_endpoint_hetzner` (env `HETZNER_ENDPOINT`, default `https://api.hetzner.com/v1`, storage boxes). Requires python-dateutil and requests.

Inventory plugin `hetzner.hcloud.hcloud` (file name must end `hcloud.yml`/`hcloud.yaml`): options `plugin` (`hcloud` or `hetzner.hcloud.hcloud`), `api_token` (alias `token`, env `HCLOUD_TOKEN`), `api_endpoint` (env `HCLOUD_ENDPOINT`), `group` (default `hcloud`), `connect_with` (`public_ipv4` default, `public_ipv6`, `hostname`, `ipv4_dns_ptr`, `private_ipv4`), `locations`, `types`, `images`, `label_selector`, `network` (name or ID), `status`, `hostvars_prefix` (default `hcloud_`), `hostvars_suffix`, `hostname` (template), plus `constructed` (`compose`, `groups`, `keyed_groups`) and cache options.
- `connect_with: private_ipv4` requires `network`; a server not in that network gets no `ansible_host` silently (only a verbose log line).
- `keyed_groups` by label: key `hcloud_labels` (dict) e.g. `- key: hcloud_labels` or `key: hcloud_labels.role`, `prefix: role`. The doc example uses `image_os_flavor`, which only exists unprefixed if `hostvars_prefix: ""`.
- Hostvars: `hcloud_id`, `hcloud_name`, `hcloud_status`, `hcloud_type`, `hcloud_server_type`, `hcloud_architecture`, `hcloud_ipv4`, `hcloud_ipv6`, `hcloud_ipv6_network`, `hcloud_ipv6_network_mask`, `hcloud_private_networks` (id, name, ip), `hcloud_private_ipv4`, `hcloud_location`, `hcloud_image_id/_os_flavor/_name`, `hcloud_labels`, plus unprefixed `ansible_host`.
- Image filter matches `image.os_flavor` although docs say name.

Calls of the inventory plugin (source): 1. `GET /locations` (token check; failure -> "Invalid Hetzner Cloud API Token"), 2. if `network` set: `GET /networks?name=` (or by ID), 3. `GET /servers` (paged, with `label_selector` and `status` as query params). Server type and image come embedded in the server object, so `servers[].server_type`, `image`, `public_net`, `private_net`, `labels`, `datacenter`-free `location` must be present in hlab's server JSON. Everything else is filtered client side.

`datacenter_info`: deprecated (6.10.0), removal date 2026-10-01 in the docs; the module still exists in 7.2.0 (7.0.0 removed the datacenter property from servers and primary IPs, not the module). It calls `client.datacenters.get_all()`/`get_by_id`/`get_by_name` -> hlab's 410 gives a module failure (API error, 410). Replacement: `location_info`, `server_type_info`.

7.2.0 news relevant: load balancers can use existing primary IPs (`public_net.ipv4/ipv6`); 7.1.0 fixed bugs in certificate, firewall, load_balancer_service, volume (e.g. wrong health_check payload).

## 4. API call order for the example environment

Example: 2 servers in one private network, LB with private-IP targets and a TCP service, firewall on both, volume and floating IP on server 1, DNS zone with A records. Calls listed are from the provider/module/CLI sources; all actions are waited on by every client.

Action polling, all clients: hcloud-go (CLI and provider) polls `GET /actions?id=<id>&id=<id>...` (list endpoint with `id` filter, `sort=status`, `sort=id`, chunks of 25), interval 500ms default with backoff; an id missing from the response is a hard error ("actions not found"); status `error` fails the call with the action's error; no client-side timeout except the context (provider create timeout 90 min). Ansible uses hcloud-python (`action.wait_until_finished`), which polls `GET /actions/{id}` (source not read; unclear). hlab must serve both forms (phase 01 already does) and must keep an action visible by id until clients read it.

### OpenTofu (dependency order follows resource references)

1. `hcloud_network` -> `POST /networks`; `hcloud_network_subnet` -> `POST /networks/{id}/actions/add_subnet` (+ wait). Subnet create retries only on `conflict` (and vswitch_already_used); `locked` fails at once. Delete retries on conflict and locked.
2. `hcloud_ssh_key` -> `POST /ssh_keys`. `hcloud_firewall` -> `POST /firewalls` (returns `actions` of `set_firewall_rules`; waited).
3. `hcloud_server` (x2): before the create it calls `GET /server_types?name=`, `GET /images?name=...&architecture=...`, `GET /ssh_keys` (by name). Then `POST /servers` (with `firewalls`, `ssh_keys`, `location`, `labels`, no networks) -> action `create_server` + `next_actions`; waits for all; then for each inline `network {}` block `POST /servers/{id}/actions/attach_to_network` and waits; powers on if created off; `GET /servers/{id}`. Note: with the inline network block the private_net exists once `hcloud_server` is finished, so anything referencing `hcloud_server.x.id` is automatically ordered after the attach. With separate `hcloud_server_network` it is a second resource that completes later.
4. `hcloud_load_balancer` -> `POST /load_balancers` (+ wait); `hcloud_load_balancer_network` -> `POST /load_balancers/{id}/actions/attach_to_network` (+ wait).
5. `hcloud_load_balancer_target` (use_private_ip = true): `GET /load_balancers/{id}`; provider retries (`control.Retry`) while the LB has no `private_net`; then `POST /load_balancers/{id}/actions/add_target`; waits; `target_already_defined` is treated as success. Create has no retry on `locked`/`conflict`; update = remove + add. `hcloud_load_balancer_service` -> `POST .../actions/add_service` (+ wait).
6. `hcloud_firewall_attachment` (if used instead of `firewall_ids`): `POST /firewalls/{id}/actions/apply_to_resources` (+ wait).
7. `hcloud_volume` -> `POST /volumes` (with `server_id` and `automount` this returns create action plus attach action) or `hcloud_volume_attachment` -> `POST /volumes/{id}/actions/attach` (+ wait). `hcloud_floating_ip` -> `POST /floating_ips` (with `home_location`), `hcloud_floating_ip_assignment` -> `POST /floating_ips/{id}/actions/assign` (+ wait).
8. `hcloud_zone` -> `POST /zones` (+ action wait, then GET); `hcloud_zone_rrset` -> `POST /zones/{zone}/rrsets` (response includes action; wait); `hcloud_zone_record` -> RRSet `add_records` action. `hcloud_rdns` -> `POST /servers|floating_ips|primary_ips|load_balancers/{id}/actions/change_dns_ptr`.
9. Terraform parallelism (default 10) fires independent resources concurrently: two `hcloud_server`s and their network attaches hit the same network at the same time; two targets or services hit the same LB at the same time.

### hcloud CLI

Each `create` waits for its action(s) itself; the order is the scripted order. `server create --network N --firewall F --volume V` makes `POST /servers` (with networks, firewalls and volumes in the body), waits for create and next actions; `--automount` needs a volume. Look-ups by name use `GET /<type>?name=`. `load-balancer add-target --server s --use-private-ip` is a single `add_target` plus wait. Not verified: whether `server create --network` waits for the attach inside the create action or sends a separate `attach_to_network` (hcloud-go: `networks` in the create body).

### Ansible

Per task, in task order: `network` (`POST /networks`), `subnetwork` (`add_subnet`), `server` (`POST /servers` with `networks`, `firewalls`, `volumes`, then waits), `server_network` (`attach_to_network`), `load_balancer` (`POST /load_balancers`), `load_balancer_network` (`attach_to_network`), `load_balancer_target` (`add_target` + wait; recurses on `locked`/`conflict` without delay), `load_balancer_service`, `firewall`/`firewall_resource` (`apply_to_resources`), `volume`, `floating_ip`, `zone`/`zone_rrset`. Every module first resolves its objects by `GET /<type>?name=` (and by id).

### Does hlab have to finish actions in a certain order?

- Clients enforce the order themselves by waiting for each action to reach `success` before the next call that depends on it. hlab does not have to sequence actions across resources; it must make an action's effect visible on GET no later than when the action reports `success`, and must not report `success` before the effect holds.
- LB target with `use_private_ip` needs the LB attached to a network (provider retries client-side while `private_net` is empty; the API itself answers 422 `load_balancer_not_attached_to_network`) and the server in that network (422 `resolve_cloud_private_targets_error`, see phase 08 RESEARCH). The race exists only when the HCL has no dependency on the server's attach: with inline `network {}` the dependency is implicit; with `hcloud_server_network` the config needs `depends_on` (or reference) and Hetzner itself would answer the same 422, so hlab should answer that 422 whenever the server's network attachment is not complete at the time of the request. It is unclear whether real Hetzner counts a still-running `attach_to_network` as attached; safest for hlab is: the server's `private_net` entry appears together with the action's success (not at request time) and add_target checks the committed state.
- Concurrency: the API serialises per resource. Real Hetzner answers 423 `locked` (or 409 `conflict`) when another action runs on the same resource. hcloud-go retries `conflict`, `rate_limit_exceeded`, 502/504 and timeouts; the provider's subnet create retries `conflict` only and does NOT retry `locked`; the LB target create and the server create do not retry either; Ansible `load_balancer_target` retries `locked` and `conflict`. So hlab should either queue concurrent actions on the same resource (best, actions stay `running` a little longer) or return `conflict`, and avoid `locked` on create paths where tofu applies in parallel (two servers attaching to one network, two targets or services on one LB, firewall attach to two servers, volume and floating IP on the same server).
- `create_server` with `networks`/`firewalls`/`volumes` in the body returns `next_actions`; clients wait for all of them. Ids of next actions must be listable via `GET /actions?id=`.
- Zone create returns an action which must finish before the RRSet create call; RRSet create/records changes also return actions (waited by the provider).

## Open items (unclear, needs a local run)

1. Exact `hcloud ... --help` flag lists beyond the key commands above (the docs pages were summarised, not copied).
2. `ansible-doc -l hetzner.hcloud` and `ansible-doc -t inventory hetzner.hcloud.hcloud` output: not executed; lists above come from the 7.2.0 source tree (listing may be truncated).
3. Provider configuration names for the Hetzner (storage box) endpoint and env var name of the CLI's `--hetzner-endpoint`.
4. How `hcloud all list` and `hcloud_zone`/`zone` commands behave if hlab does not answer on the Hetzner API endpoint (`all list` includes Storage Boxes; zones are on the Cloud API).
5. hcloud-python polling endpoint (`/actions/{id}`), and whether real Hetzner treats a running `attach_to_network` as attached for `add_target`.
6. `hcloud_server.datacenter`: docs say deprecated, source summary says validation error "Removed Attribute"; test with `tofu validate`.
