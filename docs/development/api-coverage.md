# API coverage

This page is for contributors. It lists every endpoint HLab serves, every
endpoint or behaviour that differs from the real Hetzner Cloud API with
its route, and every `unsupported_error`. Users read the short summary by
topic in [Known deviations](../deviations.md) instead.

HLab answers in Hetzner's formats, at `http://api.hetzner.internal/v1`
for the Cloud API and at `http://api.hetzner.internal/hetzner/v1` for the
[Storage Boxes](#storage-boxes) of Hetzner's API, with a token from
`hlab token create`.

The list is kept complete by a test (`TestDocs/DOC-03` in
[test/docs](../../test/docs/docs_test.go)):

- every route hlabd registers, as returned by `cloud.APIRoutes()` in
  [internal/cloud/routes.go](../../internal/cloud/routes.go), must appear
  on this page literally, in backticks: a Cloud API route relative to
  `/v1` (for example `POST /servers/{id}/actions/poweron`), a route of a
  module served under another prefix with that prefix (for example
  `GET /hetzner/v1/storage_boxes`), so a new endpoint goes into the
  [Endpoints](#endpoints) table or the table of its section;
- every entry of `api.Deviations()` in
  [internal/api/deviations.go](../../internal/api/deviations.go) must name
  an endpoint that appears on this page, and none of them may appear in
  `docs/deviations.md`.

A change of behaviour against Hetzner updates the code list, this page
and, when users notice it, the matching topic of the user page; see
[Adding a resource](adding-a-resource.md#8-document-deviations).

## Endpoints

HLab serves these endpoints:

| Endpoint | Notes |
|---|---|
| `GET /actions`, `GET /actions/{id}` | `GET /actions` needs at least one `id` parameter. |
| `GET /servers/actions`, `GET /servers/{id}/actions`, `GET /servers/{id}/actions/{action_id}` | |
| `GET /locations`, `GET /locations/{id}` | |
| `GET /datacenters`, `GET /datacenters/{id}` | 410 `deprecated_api_endpoint`, as at Hetzner since 2026-10-01. |
| `GET /server_types`, `GET /server_types/{id}` | Includes your own server types, see [Server types and prices](#server-types-and-prices). |
| `GET /pricing` | See [Server types and prices](#server-types-and-prices). |
| `GET /images`, `GET /images/{id}`, `PUT /images/{id}`, `DELETE /images/{id}`, `POST /images/{id}/actions/change_protection` | System images of the catalog plus the project's snapshots and backups, see [Snapshots and backups](#snapshots-and-backups). |
| `GET /images/actions`, `GET /images/{id}/actions`, `GET /images/{id}/actions/{action_id}` | |
| `GET /isos`, `GET /isos/{id}` | See [ISOs](#isos). |
| `GET /ssh_keys`, `POST /ssh_keys`, `GET /ssh_keys/{id}`, `PUT /ssh_keys/{id}`, `DELETE /ssh_keys/{id}` | |
| `GET /servers`, `POST /servers`, `GET /servers/{id}`, `PUT /servers/{id}`, `DELETE /servers/{id}` | See [POST /servers](#post-servers). |
| `GET /servers/{id}/metrics` | See [Metrics](#metrics). |
| `POST /servers/{id}/actions/poweron`, `POST /servers/{id}/actions/poweroff`, `POST /servers/{id}/actions/shutdown`, `POST /servers/{id}/actions/reboot`, `POST /servers/{id}/actions/reset` | See [Server actions](#server-actions). |
| `POST /servers/{id}/actions/rebuild`, `POST /servers/{id}/actions/change_type`, `POST /servers/{id}/actions/reset_password` | See [Server actions](#server-actions). |
| `POST /servers/{id}/actions/change_protection`, `POST /servers/{id}/actions/add_to_placement_group`, `POST /servers/{id}/actions/remove_from_placement_group` | See [Server actions](#server-actions). |
| `POST /servers/{id}/actions/create_image`, `POST /servers/{id}/actions/enable_backup`, `POST /servers/{id}/actions/disable_backup` | See [Snapshots and backups](#snapshots-and-backups). |
| `POST /servers/{id}/actions/enable_rescue`, `POST /servers/{id}/actions/disable_rescue` | See [Rescue system](#rescue-system). |
| `POST /servers/{id}/actions/attach_iso`, `POST /servers/{id}/actions/detach_iso` | See [ISOs](#isos). |
| `POST /servers/{id}/actions/request_console` | 422 `unsupported_error`, see [Server actions](#server-actions). |
| `GET /placement_groups`, `POST /placement_groups`, `GET /placement_groups/{id}`, `PUT /placement_groups/{id}`, `DELETE /placement_groups/{id}` | See [Placement groups](#placement-groups). |
| `GET /volumes`, `POST /volumes`, `GET /volumes/{id}`, `PUT /volumes/{id}`, `DELETE /volumes/{id}` | See [Volumes](#volumes). |
| `POST /volumes/{id}/actions/attach`, `POST /volumes/{id}/actions/detach`, `POST /volumes/{id}/actions/resize`, `POST /volumes/{id}/actions/change_protection` | See [Volumes](#volumes). |
| `GET /volumes/actions`, `GET /volumes/{id}/actions`, `GET /volumes/{id}/actions/{action_id}` | |
| `GET /networks`, `POST /networks`, `GET /networks/{id}`, `PUT /networks/{id}`, `DELETE /networks/{id}`, `GET /networks/{id}/members` | See [Networks](#networks). |
| `POST /networks/{id}/actions/add_subnet`, `POST /networks/{id}/actions/delete_subnet`, `POST /networks/{id}/actions/add_route`, `POST /networks/{id}/actions/delete_route`, `POST /networks/{id}/actions/change_ip_range`, `POST /networks/{id}/actions/change_protection` | See [Networks](#networks). |
| `POST /servers/{id}/actions/attach_to_network`, `POST /servers/{id}/actions/detach_from_network`, `POST /servers/{id}/actions/change_alias_ips` | See [Networks](#networks). |
| `GET /networks/actions`, `GET /networks/{id}/actions`, `GET /networks/{id}/actions/{action_id}` | |
| `GET /primary_ips`, `POST /primary_ips`, `GET /primary_ips/{id}`, `PUT /primary_ips/{id}`, `DELETE /primary_ips/{id}` | See [Primary IPs](#primary-ips). |
| `POST /primary_ips/{id}/actions/assign`, `POST /primary_ips/{id}/actions/unassign`, `POST /primary_ips/{id}/actions/change_protection` | See [Primary IPs](#primary-ips). |
| `GET /primary_ips/actions`, `GET /primary_ips/{id}/actions`, `GET /primary_ips/{id}/actions/{action_id}` | |
| `GET /floating_ips`, `POST /floating_ips`, `GET /floating_ips/{id}`, `PUT /floating_ips/{id}`, `DELETE /floating_ips/{id}` | See [Floating IPs](#floating-ips). |
| `POST /floating_ips/{id}/actions/assign`, `POST /floating_ips/{id}/actions/unassign`, `POST /floating_ips/{id}/actions/change_protection` | See [Floating IPs](#floating-ips). |
| `GET /floating_ips/actions`, `GET /floating_ips/{id}/actions`, `GET /floating_ips/{id}/actions/{action_id}` | |
| `GET /firewalls`, `POST /firewalls`, `GET /firewalls/{id}`, `PUT /firewalls/{id}`, `DELETE /firewalls/{id}` | See [Firewalls](#firewalls). |
| `POST /firewalls/{id}/actions/apply_to_resources`, `POST /firewalls/{id}/actions/remove_from_resources`, `POST /firewalls/{id}/actions/set_rules` | See [Firewalls](#firewalls). |
| `GET /firewalls/actions`, `GET /firewalls/{id}/actions`, `GET /firewalls/{id}/actions/{action_id}` | |
| `GET /zones`, `POST /zones`, `GET /zones/{id}`, `PUT /zones/{id}`, `DELETE /zones/{id}` | See [Zones](#zones). |
| `POST /zones/{id}/actions/change_protection`, `POST /zones/{id}/actions/change_ttl`, `POST /zones/{id}/actions/change_primary_nameservers` | See [Zones](#zones). |
| `GET /zones/{id}/zonefile`, `POST /zones/{id}/actions/import_zonefile` | See [Zone files](#zone-files). |
| `GET /zones/{id}/rrsets`, `POST /zones/{id}/rrsets`, `GET /zones/{id}/rrsets/{rr_name}/{rr_type}`, `PUT /zones/{id}/rrsets/{rr_name}/{rr_type}`, `DELETE /zones/{id}/rrsets/{rr_name}/{rr_type}` | See [Records](#records). |
| `POST /zones/{id}/rrsets/{rr_name}/{rr_type}/actions/change_protection`, `POST /zones/{id}/rrsets/{rr_name}/{rr_type}/actions/change_ttl`, `POST /zones/{id}/rrsets/{rr_name}/{rr_type}/actions/set_records`, `POST /zones/{id}/rrsets/{rr_name}/{rr_type}/actions/add_records`, `POST /zones/{id}/rrsets/{rr_name}/{rr_type}/actions/remove_records`, `POST /zones/{id}/rrsets/{rr_name}/{rr_type}/actions/update_records` | See [Records](#records). |
| `GET /zones/actions`, `GET /zones/actions/{id}`, `GET /zones/{id}/actions`, `GET /zones/{id}/actions/{action_id}` | Zone and RRSet actions are readable at all of these and at `GET /actions/{id}`. |
| `POST /servers/{id}/actions/change_dns_ptr`, `POST /primary_ips/{id}/actions/change_dns_ptr`, `POST /floating_ips/{id}/actions/change_dns_ptr`, `POST /load_balancers/{id}/actions/change_dns_ptr` | See [Reverse DNS](#reverse-dns). |
| `GET /load_balancer_types`, `GET /load_balancer_types/{id}` | The types of HLab's catalog. |
| `GET /load_balancers`, `POST /load_balancers`, `GET /load_balancers/{id}`, `PUT /load_balancers/{id}`, `DELETE /load_balancers/{id}` | See [Load balancers](#load-balancers). |
| `POST /load_balancers/{id}/actions/add_service`, `POST /load_balancers/{id}/actions/update_service`, `POST /load_balancers/{id}/actions/delete_service`, `POST /load_balancers/{id}/actions/add_target`, `POST /load_balancers/{id}/actions/remove_target`, `POST /load_balancers/{id}/actions/change_algorithm`, `POST /load_balancers/{id}/actions/change_type`, `POST /load_balancers/{id}/actions/change_protection` | See [Load balancers](#load-balancers). |
| `POST /load_balancers/{id}/actions/attach_to_network`, `POST /load_balancers/{id}/actions/detach_from_network`, `POST /load_balancers/{id}/actions/enable_public_interface`, `POST /load_balancers/{id}/actions/disable_public_interface` | See [Load balancers](#load-balancers). |
| `GET /load_balancers/{id}/metrics` | See [Metrics](#metrics). |
| `GET /load_balancers/actions`, `GET /load_balancers/{id}/actions`, `GET /load_balancers/{id}/actions/{action_id}` | |
| `GET /certificates`, `POST /certificates`, `GET /certificates/{id}`, `PUT /certificates/{id}`, `DELETE /certificates/{id}`, `POST /certificates/{id}/actions/retry` | See [Certificates](#certificates). |
| `GET /certificates/actions`, `GET /certificates/{id}/actions`, `GET /certificates/{id}/actions/{action_id}` | |

`{id}` of a zone is its ID or its name. Lists page, filter and sort with
the parameters of Hetzner's API description. Every other endpoint answers
404 `not_found`.

## Action queue

Hetzner serialises actions per resource and answers 423 `locked` while
another action runs on a resource of the request. HLab keeps 423 `locked`
for actions that change the state of their own resource (power, rebuild,
change_type, change_protection, delete, `change_ip_range`, `set_rules`
and similar), but the actions below wait instead: they are created
`running` and their work starts after every earlier running action that
shares a resource with them has finished, in order of creation. A
resource listed as "refuses" still answers 423 `locked` while another
action holds it.

| Endpoint | Waits for actions on | Refuses |
|---|---|---|
| `POST /servers/{id}/actions/attach_to_network`, `POST /servers/{id}/actions/detach_from_network`, `POST /servers/{id}/actions/change_alias_ips` | the network | the server |
| `POST /load_balancers/{id}/actions/attach_to_network`, `POST /load_balancers/{id}/actions/detach_from_network` | the network | the load balancer |
| `POST /networks/{id}/actions/add_subnet`, `POST /networks/{id}/actions/delete_subnet`, `POST /networks/{id}/actions/add_route`, `POST /networks/{id}/actions/delete_route` | the network | - |
| `POST /load_balancers/{id}/actions/add_target`, `POST /load_balancers/{id}/actions/remove_target`, `POST /load_balancers/{id}/actions/add_service`, `POST /load_balancers/{id}/actions/update_service`, `POST /load_balancers/{id}/actions/delete_service` | the load balancer and its target servers | - |
| `POST /volumes/{id}/actions/attach`, `POST /volumes/{id}/actions/detach`, `POST /volumes` with `server` | the server | the volume |
| `POST /floating_ips/{id}/actions/assign`, `POST /floating_ips/{id}/actions/unassign` | the server | the floating IP |
| `POST /primary_ips/{id}/actions/assign`, `POST /primary_ips/{id}/actions/unassign` | the server | the primary IP |
| `POST /firewalls/{id}/actions/apply_to_resources`, `POST /firewalls/{id}/actions/remove_from_resources` | the servers | the firewall |
| `POST /servers` with `firewalls` (its firewall actions) | the firewall | - |
| `POST /zones/{id}/rrsets`, `DELETE /zones/{id}/rrsets/{rr_name}/{rr_type}` and the RRSet actions | the zone | - |

## POST /servers

- The IPv6 /64 is assigned formally from the documentation range
  `2001:db8::/32`: the next 32 bits are the server ID (server 4 gets
  `2001:db8:0:4::/64`). The server gets no IPv6
  traffic and its guest has no IPv6 address configured.
- A server created without SSH keys returns a `root_password`. It does
  not expire: you are not asked to change it at the first login, as you
  are at Hetzner. Root may log in over SSH with it.
- Servers run on your machine, so `POST /servers`, `poweron` and
  `change_type` answer 412 `resource_unavailable` when the server type has
  more vCPUs than the host has logical CPUs, when its RAM exceeds the
  host's free RAM minus `reserve_ram_mb`, or when its disk does not fit
  into the free space of the data folder minus `reserve_disk_gb` (see the
  [configuration reference](../configuration.md#settingstoml)); `rebuild`
  answers the same when the disk does not fit. A stopped server holds no
  RAM.
- Only `name`, `server_type`, `image`, `location`, `ssh_keys`, `user_data`,
  `start_after_create`, `labels`, `placement_group`, `volumes`,
  `automount`, `public_net`, `networks` and `firewalls` are used. Other
  fields are ignored.
- A server without public IPv4, public IPv6 and network answers 422
  `invalid_input` on the field `public_net`. A primary IP given in
  `public_net.ipv4` or `public_net.ipv6` is used even when
  `enable_ipv4` or `enable_ipv6` is `false`.
- `image` takes a system image by name or ID, or a snapshot or backup of
  the project by ID. A server type whose disk is smaller than the image's
  `disk_size` answers 422 `invalid_input` on the field `server_type`.
- Server types with architecture `arm` answer 412 `resource_unavailable`:
  HLab runs only x86 servers.
- The `create_server` action succeeds when the VM runs, before the guest
  has booted; SSH answers a few seconds later.
- Public IPv4 addresses come from the configured public range (default
  `198.19.0.0/16`), not from Hetzner's ranges. A deleted server's address
  is not handed out again at once: allocation continues after the last
  address given and starts over only at the end of the range.

## Server actions

- `shutdown` asks the guest to shut down and succeeds once the guest has
  accepted the request. A guest that is still booting refuses it; HLab
  asks again every 2 seconds for up to 2 minutes before the action fails.
  The server shows `stopping` until the guest has turned the VM off, then
  `off`; a guest that ignores the request stays `stopping` while it runs.
  A reboot started inside the guest shows `stopping` only while the VM
  restarts and `running` again once it runs.
- `rebuild` takes a system image, or a snapshot or backup of the project
  by ID. An image whose `disk_size` exceeds the server type's disk answers
  422 `invalid_input` on the field `image`.
- After a `rebuild`, cloud-init in the guest sees the instance-id
  `<server id>-<action id>` (the ID of the rebuild action), so it runs its
  first-boot steps again, also on a disk restored from a snapshot or
  backup of the same server. The metadata service keeps answering
  `/hetzner/v1/metadata/instance-id` with the server ID.
- `reset_password` works through a small HLab guest hook that HLab
  installs at the first boot of every server it creates, not through
  qemu-guest-agent. The hook applies the new password within a few
  seconds; without a confirmation within 120 s the action fails with
  `server_error`. A server without the hook, such as one created by an
  earlier HLab version or one whose user data turns off cloud-init's
  vendor data, therefore cannot reset its password. On a server that is
  not `running` the action answers 409 `conflict`. When the cloud stops
  during the action, the action ends with `server_error` at the next
  start, and the server no longer receives the new password.
- `request_console` answers 422 `unsupported_error` with the message
  `server console is not supported by HLab yet` and starts no action.

## Snapshots and backups

- `create_image` copies the server's disk while the server keeps running,
  through a Hyper-V checkpoint. The copy is crash-consistent: it holds what
  a power cut at that moment would leave on the disk. Without
  `description`, the image gets `<server name> <creation time>`.
- `image_size` is the size of the copied disk file, not a compressed size.
- `backup_window` shows `22-02` while backups are enabled; HLab does not
  use it. HLab checks every hour while the cloud runs and makes one daily
  backup per server and UTC day at the first check of that day. Days on
  which the cloud did not run get no backup later; a server with a
  running action gets its backup at the next check.
- Daily and manual backups share the server's 7 slots; the oldest backup
  beyond 7 is deleted. `create_image` with `type` `backup` on a server
  without backups enabled answers 422 `invalid_input` on the field `type`.
- `DELETE /images/{id}` of an image that is still being created answers
  423 `locked`.
- When the cloud stops while an image is being created, the next cloud
  start ends the `create_image` action with `server_error` and removes the
  image and its partial file.

## Rescue system

- The rescue system is a Debian 13 system that boots from its own disk,
  not from memory. The server's disk stays `/dev/sda`; the rescue disk is
  another SCSI disk (`/dev/sdb` on a server without volumes). Everything
  written to the rescue disk is lost when rescue ends.
- The first `enable_rescue` prepares the rescue disk from the debian-13
  image, downloading that image if no server used it before, so it takes
  longer.
- Rescue is disabled when HLab boots the server into it, through
  `poweron`, `reboot` or `reset`, or 60 minutes after `enable_rescue`
  without such a boot. A reboot started inside the guest also boots the
  rescue system, but HLab does not notice it: rescue then stays enabled,
  and every later boot runs the rescue system, until `disable_rescue` or
  the 60 minutes have passed.
- `reboot` of a server running the rescue system shuts the guest down and
  starts the VM again; it fails when the guest is not off within two
  minutes.

## ISOs

- `/isos` lists the five ISOs of HLab's catalog (Ubuntu live server
  22.04.5, 24.04.5 and 26.04.1, Debian netinst 12.15.0 and 13.7.0) with
  HLab's own IDs from 130001, not Hetzner's ISOs, followed by the user's
  own ISOs from `isos.custom.json` with IDs from 100000. These are the same
  in every project, of `type` `private` with `name` `null`; the `name`
  filter matches only catalog ISOs. See the configuration reference,
  [ISOs](../configuration.md#isos) and [Own ISOs](../configuration.md#own-isos).
- `attach_iso` takes a catalog ISO by name or ID and an own ISO by ID
  only.
- The first `attach_iso` of an ISO downloads it inside the action, which
  takes minutes; an own ISO from a local file is copied when hlabd reads
  `isos.custom.json`.
- An unknown or deprecated ISO answers 422 `invalid_input` on the field
  `iso`. `detach_iso` on a server without an ISO succeeds and changes
  nothing.
- The server has a second DVD drive holding its cloud-init seed; the
  system booted from the ISO sees both.

## Volumes

- `linux_device` is `/dev/disk/by-id/scsi-0HC_Volume_<id>`. The link, the
  automatic mount and the pick-up of a grown volume come from HLab guest
  hooks that HLab installs at the first boot of every server it creates.
  In a server without them,
  such as one whose user data turns off cloud-init's vendor data, a
  volume is a plain SCSI disk `/dev/sdX`.
- `automount` mounts a volume with a filesystem at `/mnt/HC_Volume_<id>`
  and adds an `/etc/fstab` line with `discard,nofail,defaults`.
- `resize` grows the disk; the guest hook makes the server see the new
  size within a few seconds. A server without the hook sees it at the
  disk's next access. As at Hetzner, the filesystem is not grown; run
  `resize2fs` or `xfs_growfs` in the server.
- A server holds at most 16 volumes; one more answers 422
  `invalid_input`.
- Attaching a volume that is attached answers 422
  `volume_already_attached`. Attaching it to a server in another location
  answers 422 `invalid_input` on the field `server` or `assignee`;
  `POST /volumes` with a `location` other than the server's answers the
  same on the field `location`.
- `DELETE /volumes/{id}` of an attached volume answers 422
  `invalid_input`; detach it first.
- `POST /volumes` and `resize` answer 412 `resource_unavailable` when the
  volume does not fit into the free space of the data folder minus
  `reserve_disk_gb`.
- When the cloud stops while a volume is being created, the next cloud
  start ends its action with `server_error` and removes the volume and its
  disk file.

## Networks

- A subnet of type `vswitch` answers 422 `invalid_input` with the message
  `vswitch subnets are not supported by HLab`. `expose_routes_to_vswitch`
  is stored and shown but has no effect.
- The gateway of a network is the first host address of its `ip_range`
  (`10.0.0.1` for `10.0.0.0/16`); every subnet shows it as `gateway`, also
  a subnet of type `server`. A server gets the lowest free host address of
  a subnet in its location's network zone; the network and broadcast
  address of each subnet, the gateway and `172.31.1.1` are never handed
  out.
- Private adapters have MAC addresses `02:48:4c:2x:xx:xx`; Hetzner uses
  `86:00:00:xx:xx:xx`. Tools that recognise Hetzner's private adapters by
  their MAC, such as hc-utils, do not see HLab's.
- HLab has no DHCP server. A network a server gets at creation is set up
  by cloud-init from HLab's seed at boot: the private IP as `/32` and a
  route to the network range via the gateway, on-link, as Hetzner's DHCP
  gives it. A network attached or detached while the server runs is set
  up or removed within a few seconds by the HLab guest hook that HLab
  installs at the first boot of every server it creates; every later boot
  sets up the current networks again. A server without the hook, such as
  one created by an earlier HLab version or one whose user data turns off
  cloud-init's vendor data, picks up later attachments only if you
  configure the adapter yourself.
- The MTU on private networks is 1500; Hetzner uses 1450.
- As at Hetzner, alias IPs and routes to destinations outside the
  network range are not configured in the guest; see
  [Networks](../guide.md#networks).
- `DELETE /networks/{id}` detaches all servers of the network and removes
  their adapters before it answers.
- `GET /networks/{id}/members` lists a member once its attach action has
  succeeded, always with status `ok`.

Hetzner documents no error for these cases; HLab answers:

| Case | Answer |
|---|---|
| `delete_subnet` of a subnet in which an attached server holds an IP or alias IP | 422 `service_error` |
| `delete_subnet` or `delete_route` of a subnet or route the network does not have | 422 `invalid_input` |
| `change_ip_range` with another start address or a smaller range | 422 `invalid_input` on the field `ip_range` |
| `change_ip_range` while servers are attached | 422 `service_error` |
| `POST /networks` when all 4091 VLAN IDs of the cloud are in use | 403 `resource_limit_exceeded` |
| `attach_to_network` or `POST /servers` with a fourth network for the server | 403 `resource_limit_exceeded` |
| more than 5 alias IPs of a server in one network | 422 `invalid_input` on the field `alias_ips` |
| no free IP, or no subnet in the server's network zone | 422 `no_subnet_available` |
| an `ip` or alias IP that is taken or reserved | 422 `ip_not_available` |
| an `ip` outside the subnets of the server's network zone | 422 `invalid_input` on the field `ip` |

## Primary IPs

- A primary IPv4 comes from the configured public range. A primary IPv6
  is formal, like a server's: it carries no traffic. One made with a
  server is the server's `/64` (server 4: `2001:db8:0:4::/64`); one
  created on its own is `2001:db8:8000:<id>::/64` (ID 12:
  `2001:db8:8000:c::/64`).
- Primary IPs show their `location`; they have no `datacenter` field.
- `POST /primary_ips` needs a `name`; an empty one answers 422
  `invalid_input` on the field `name`. The primary IPs HLab makes for a new
  server are named `primary_ip-<id>`.
- Servers created by an earlier HLab version get primary IP records for
  their addresses at the next cloud start, assigned and with
  `auto_delete: true`.
- `change_protection` with `delete: true` on a primary IP with
  `auto_delete` answers 422 `invalid_input` on the field `delete`.
- `DELETE /primary_ips/{id}` of an assigned primary IP answers 422
  `must_be_unassigned`; unassign it first.
- A primary IP held by a load balancer shows `assignee_type`
  `load_balancer` and the load balancer's ID in `assignee_id`; Hetzner's
  API description knows only `server` there. `assign` on such an IP
  answers 422 `primary_ip_already_assigned`, `unassign` 422
  `invalid_input`. Deleting the load balancer frees the IP, or deletes it
  when it has `auto_delete`.
- For `dns_ptr` see [Reverse DNS](#reverse-dns).

## Floating IPs

- An IPv4 floating IP comes from the configured public range. HLab routes
  it to the public IPv4 of its server, so a server without public IPv4
  receives no traffic for it. The guest has to configure the address,
  as at Hetzner; see [Primary and floating IPs](../guide.md#primary-and-floating-ips).
- An IPv6 floating IP is formal and carries no traffic:
  `2001:db8:c000:<id>::/64` (ID 12: `2001:db8:c000:c::/64`).
- A floating IP is assigned only to a server in the network zone of its
  home location; another server answers 422 `invalid_input` on the field
  `server`. Assigning it to the server that holds it answers 422
  `floating_ip_assigned`; assigning it to another server moves it.
- A floating IP created without a name is named `floating_ip-<id>`.
- `DELETE /floating_ips/{id}` of an assigned floating IP answers 422
  `must_be_unassigned`; unassign it first.
- For `dns_ptr` see [Reverse DNS](#reverse-dns).

## Firewalls

- A firewall filters the traffic of a server's public IPv4 and of the
  floating IPs assigned to the server. Hetzner does not document whether
  its firewalls filter floating IP traffic.
- IPv6 rules are accepted and stored but have no effect; servers carry no
  IPv6 traffic in HLab.
- The `status` of a firewall in a server's `public_net.firewalls` is
  always `applied`, never `pending`.
- A firewall takes at most 50 rules and 100 CIDRs per rule list, and a
  server at most 5 firewalls, as at Hetzner. Hetzner's limits of 500
  effective rules per firewall and 50 firewalls per project are not
  enforced.
- Every IP in a rule must be a CIDR with a mask and without host bits set
  (`10.0.0.0/24`, not `10.0.0.5/24` or `10.0.0.5`); otherwise the answer is
  422 `invalid_input` on the field `rules[i].source_ips` or
  `rules[i].destination_ips`. `port` is `any`, a port `N` or a range `A-B`
  (1 to 65535), required for `tcp` and `udp` and not allowed for `icmp`,
  `esp` and `gre`; it is returned exactly as sent. IPv6 CIDRs are returned
  in their canonical form.
- `apply_firewall` and `remove_firewall` for a label selector name only the
  firewall in their `resources`. `set_rules` returns `set_firewall_rules`
  plus one `apply_firewall` per server the firewall is applied to
  directly.
- `incompatible_network_type` is never returned.

Hetzner documents no error for these cases; HLab answers:

| Case | Answer |
|---|---|
| `remove_from_resources` of a server or label selector the firewall is not applied to | 404 `firewall_resource_not_found` |
| `apply_to_resources` of a label selector the firewall already has | 422 `firewall_already_applied` |
| `apply_to_resources`, `POST /firewalls` or `POST /servers` that would give a server a sixth firewall | 403 `resource_limit_exceeded`; a label selector that matches such a server skips it |
| `DELETE /firewalls/{id}` of a firewall applied only through a label selector | 422 `resource_in_use` |
| `POST /servers` with a firewall that does not exist | 404 `not_found`; no server is created |
| `POST /servers` with `firewalls` and without public IPv4 | 422 `private_net_only_server` |
| a firewall listed twice in the `firewalls` of `POST /servers` | applied once |

## Zones

HLab's DNS server in the appliance answers for your zones; the host, WSL2
and the servers ask it (see [DNS zones](../guide.md#dns-zones)). No
registrar or public nameserver is involved.

- Besides a registrable domain (`example.com`, exactly one label below a
  public suffix of the ICANN section of the Public Suffix List), a zone
  name may be exactly one label below the private-use suffixes
  `internal`, `test` or `home.arpa`, such as `lab.internal`.
  `hetzner.internal`, which HLab uses itself, is refused, and so are names
  under `local`, unknown TLDs, subdomains such as `a.example.com` or
  `a.lab.internal`, and every name under `arpa` except `home.arpa`, which
  also refuses reverse zones. Each refusal is 422 `invalid_input` on the
  field `name`.
- A zone name is unique in the whole cloud: a name used in another project
  answers 409 `uniqueness_error` with the message `zone name is already
  used`.
- `authoritative_nameservers.assigned` lists Hetzner's three nameservers,
  but nothing is delegated: `delegated` is `[]`, `delegation_status` and
  `registrar` are `unknown`, and `status` is always `ok`.
- A zone with `mode` `secondary` is stored with its
  `primary_nameservers`, but HLab never transfers it (no AXFR) and holds no
  records for it: the appliance forwards its queries to the host's DNS
  servers. Its RRSet calls, `change_ttl` and `import_zonefile` answer 422
  `incorrect_zone_mode`, as does `change_primary_nameservers` on a primary
  zone.
- In `primary_nameservers`, `address` must be an IP address (private
  addresses are allowed) and unique in the zone, `port` lies in 1 to 65535
  (default 53), `tsig_algorithm` is one of Hetzner's values and `tsig_key`
  is valid base64. Each refusal is 422 `invalid_input` on the field
  `primary_nameservers[i].<field>`. HLab never checks whether the
  nameservers answer.
- There is no DNSSEC: answers are not signed, and DS records are stored
  and served as plain data.
- `PUT /zones/{id}` and `PUT /zones/{id}/rrsets/{rr_name}/{rr_type}` answer 200
  without an action; all other writes run as actions.
- `record_count` counts every record, SOA and NS included (4 for a new
  zone).

### Records

- The apex SOA RRSet is managed by HLab; its serial grows with every
  change. Creating, deleting or changing its records answers 422
  `invalid_input` on the field `type`; its TTL may be changed. The apex NS
  RRSet may be edited freely.
- A CNAME beside another type at the same name, a CNAME RRSet with more
  than one record, or a CNAME at the apex answers 422 `invalid_input` on
  the field `type` with the message
  `(<name>, CNAME) conflicts with (<name>, <type>)`.
- Creating an RRSet that exists answers 409 `uniqueness_error`.
- Every write to a change-protected RRSet answers `protected`: records,
  TTL, delete, and a zone file import that would replace it. Labels and
  the protection itself stay changeable.
- `RP` values are taken in DNS mailbox form
  (`admin.example.com. txt.example.com.`); `admin@example.com` is
  refused.
- Record values are stored and returned exactly as sent.

### Zone files

- A bad zone file answers 422 `invalid_input` on the field `zonefile` at
  once, with the line number in the message (`line N`); no action is
  started. `$INCLUDE` and `$GENERATE` are refused.
- The export writes `$ORIGIN` and `$TTL`, then the SOA, the NS records and
  the other records sorted by name and type. A record line carries a TTL
  only when its RRSet has its own. Comments are exported, and dropped on
  import, as at Hetzner.
- An import into a zone with any change-protected RRSet answers
  `protected`. The imported SOA keeps its values except the serial, which
  HLab continues.

### Answers of the DNS server

- Wildcards answer only names without records of their own (RFC 4592).
- A CNAME is followed inside HLab's zones and, for outside names, through
  the host's DNS servers, at most 8 steps.
- HLab warns when a zone you create hides a domain that exists publicly;
  see [hlab cloud status](../commands.md#hlab-cloud-status).

## Reverse DNS

- A primary IPv4 or floating IPv4 without an own entry shows Hetzner's
  default `static.<d>.<c>.<b>.<a>.clients.your-server.de`, as a server's
  IPv4 does. IPv6 addresses start without entries.
- PTRs of IPv4 addresses resolve through HLab's DNS server. IPv6 entries
  are stored and shown but do not resolve, since servers carry no IPv6
  traffic in HLab.
- `dns_ptr` must be a host name of at least two labels without a trailing
  dot; `null` resets an IPv4 to its default and removes an IPv6 entry. A
  /64 holds at most 100 entries. The `dns_ptr` key is required.
- A server's `change_dns_ptr` writes to the primary IP that holds the
  address. An IP the server does not hold answers 422 `invalid_input` on
  the field `ip`; the action names the server and the primary IP.
- A load balancer's IPv4 starts with the same default name and resolves
  like a server's, also while its public interface is off.

## Load balancers

A load balancer is an HAProxy instance in the appliance VM, in a network
namespace of its own. It forwards traffic as Hetzner's does, with these
differences:

- `ip` targets take any unicast IPv4 address except the first four
  addresses of the public range (they connect host and appliance) and the
  load balancer's own address; addresses of servers in the cloud are
  allowed. Hetzner allows only the IPs of your dedicated servers. They are
  reached over the public interface.
- The IPv6 address is formal and carries no traffic:
  `2001:db8:<0xA0000000 + load balancer ID>::1` (load balancer 3:
  `2001:db8:a000:3::1`), or `<its /64>::1` with an IPv6 primary IP.
  Reverse DNS entries an older HLab stored under the earlier prefix
  `2001:db8:8000:<ID>::/64` move to the same address in the new /64 at
  the next cloud start.
- Without `public_net.ipv4`, the load balancer gets an IPv4 from the
  public range that is not a primary IP and is deleted with it. With
  `public_net.ipv4` or `public_net.ipv6` it holds those primary IPs; see
  [Primary IPs](#primary-ips).
- `proxyprotocol` sends PROXY protocol version 2. Which version Hetzner
  sends is not documented; most receivers accept both.
- A health check with protocol `https` is stored and shown as `http` with
  `http.tls: true`. TLS health checks do not verify the target's
  certificate.
- `health_status` of a target is `unknown` until HAProxy has checked it,
  and shows only `listen_port` and `status`.
- `max_connections` of the load balancer type becomes HAProxy's
  `maxconn`; nothing else limits the traffic.
- `redirect_http` of an https service takes port 80 of the load
  balancer.
- An https service whose managed certificate is not issued yet, or has
  failed, is not served until the certificate is issued.
- A load balancer is attached to at most one network.

Hetzner documents no error for these cases, or does not confirm one;
HLab answers:

| Case | Answer |
|---|---|
| `add_service` or `add_target` beyond the limits of the load balancer type | 403 `resource_limit_exceeded` |
| `change_type` to a type whose limits the load balancer exceeds | 422 `invalid_load_balancer_type` |
| a server target in another network zone than the load balancer | 422 `invalid_input` on the field `server` |
| `add_service` on a listen port in use | 412 `source_port_already_used` |
| `update_service` that moves a service onto a listen port in use | 422 `source_port_already_used` |
| `update_service` or `delete_service` of a listen port without a service | 422 `invalid_input` on the field `listen_port` |
| `remove_target` of a target the load balancer does not have | 404 `not_found` |
| `detach_from_network` while targets use private IPs of the network | 422 `invalid_input` on the field `network` |
| a target without `use_private_ip` while the public interface is off | 422 `load_balancer_public_interface_disabled` |
| `add_service` without `health_check` | a `tcp` check on the destination port, interval 15 s, timeout 10 s, 3 retries |

## Certificates

- Managed certificates are issued by the HLab CA inside HLab, not by
  Let's Encrypt. They are valid for 90 days, use an ECDSA P-256 key and
  name their domains; HLab renews them 30 days before they expire.
  Clients trust them only after `hlab ca trust on`, or with the CA
  certificate from `hlab ca export`; see
  [Load balancers and certificates](../guide.md#load-balancers-and-certificates).
- Every certificate the HLab CA issues names the revocation list
  `http://api.hetzner.internal/hlab/v1/ca.crl`. HLab serves an empty,
  signed list there, renewed at every cloud start and every week, so
  clients that check revocation, such as `curl.exe` on Windows, accept
  the certificate.
- A managed certificate is issued when every domain equals or lies below
  a zone of the project, wildcards included. Otherwise it fails with
  `dns_zone_not_found` in `status.error` and on the action, or with
  `dns_zone_is_secondary_zone` when that zone is a secondary zone.
- `retry` of a certificate that has not failed, or of an uploaded one,
  answers 422 `invalid_input`.
- `DELETE /certificates/{id}` of a certificate a load balancer service
  uses answers 422 `resource_in_use`.
- Uploaded certificates are not checked for expiry or chain order. A PEM
  that cannot be read, or a key that does not match the first
  certificate, answers 422 `invalid_input`.

## Metrics

- hlabd samples every running server every 10 s and keeps the samples
  24 hours in memory. They are lost when the cloud stops, and a stopped
  server has no samples for its stopped time. The smallest `step` is 10 s.
- `disk.0` is the sum of all disks of the server's VM.
- Load balancer metrics (`open_connections`, `connections_per_second`,
  `requests_per_second`, `bandwidth`) come from HAProxy's counters, read
  every 10 s and kept 24 hours in memory like the server samples; the
  smallest `step` is 10 s. Counters that drop, as after a reload of
  HAProxy, count as a reset, not as negative traffic.

## Metadata service

Servers read their metadata at `http://169.254.169.254/hetzner/v1/metadata`
(the whole document, or one key such as
`/hetzner/v1/metadata/hostname`) and their user data at
`/hetzner/v1/userdata`.

- Only these Hetzner paths are served; the EC2-style routes are not.
- `public-keys` and `userdata` are in the shape cloud-init's Hetzner
  data source reads.
- The service answers by the source address of the request, and a
  server cannot send from another server's address, so each server reads
  only its own data.
- The service also answers at the same address through each private
  network, so a server without public IPv4 reads its metadata there.
  `/hetzner/v1/metadata/private-networks` lists the server's networks.

## Placement groups

HLab runs every server on your one machine, so a placement group places
nothing physically: a `spread` group only records its member servers.

- `POST /placement_groups` answers with `"action": null`.
- A spread group holds at most 10 servers. The eleventh, whether added at
  `POST /servers` or with `add_to_placement_group`, answers 422
  `placement_error`.
- `DELETE /placement_groups/{id}` also deletes a group that still has
  members; its servers then show `"placement_group": null`.

## Server types and prices

- `/server_types` lists the server types of HLab's catalog, followed by
  your own types from `server-types.custom.json` (see the
  [configuration reference](../configuration.md#custom-server-types)).
  `hlab catalog update` replaces the catalog types with the current ones
  from Hetzner.
- `/pricing` shows the prices of HLab's catalog. Its `server_types` list
  every served type; an own type without `prices` gets zero prices in each
  of its locations.

## Storage Boxes

The Storage Box endpoints of Hetzner's API are served under
`http://api.hetzner.internal/hetzner/v1`, the address of
`api.hetzner.com/v1`; the routes below carry that prefix.

| Endpoint | Notes |
|---|---|
| `GET /hetzner/v1/storage_box_types`, `GET /hetzner/v1/storage_box_types/{id}` | The types of HLab's catalog file `storage-box-types.json`, as Hetzner lists them, followed by the own types of `storage-box-types.custom.json`; none without either file. `hlab catalog update` does not change them. |
| `GET /hetzner/v1/storage_boxes`, `POST /hetzner/v1/storage_boxes`, `GET /hetzner/v1/storage_boxes/{id}`, `PUT /hetzner/v1/storage_boxes/{id}`, `DELETE /hetzner/v1/storage_boxes/{id}` | `PUT` answers at once; create and delete run as actions. Create and `change_type` to a larger type answer 412 `resource_unavailable` below 10 GiB free on the drive of the data folder. |
| `POST /hetzner/v1/storage_boxes/{id}/actions/change_protection`, `POST /hetzner/v1/storage_boxes/{id}/actions/change_type`, `POST /hetzner/v1/storage_boxes/{id}/actions/reset_password`, `POST /hetzner/v1/storage_boxes/{id}/actions/update_access_settings`, `POST /hetzner/v1/storage_boxes/{id}/actions/enable_snapshot_plan`, `POST /hetzner/v1/storage_boxes/{id}/actions/disable_snapshot_plan` | `change_type` also takes a smaller type, unless the box holds more data than it: 422 `invalid_input` on `storage_box_type`. A snapshot plan is stored only. |
| `GET /hetzner/v1/storage_boxes/{id}/folders` | The direct subdirectories of `path` (default `.`), hidden ones included, sorted. |
| `GET /hetzner/v1/storage_boxes/{id}/subaccounts`, `POST /hetzner/v1/storage_boxes/{id}/subaccounts`, `GET /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}`, `PUT /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}`, `DELETE /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}` | The subaccount list has no paging, as in the description. |
| `POST /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}/actions/change_home_directory`, `POST /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}/actions/reset_subaccount_password`, `POST /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}/actions/update_access_settings` | `update_access_settings` refuses `home_directory`; use `change_home_directory`. |
| `GET /hetzner/v1/storage_boxes/actions`, `GET /hetzner/v1/storage_boxes/actions/{id}`, `GET /hetzner/v1/storage_boxes/{id}/actions`, `GET /hetzner/v1/storage_boxes/{id}/actions/{action_id}` | Box and subaccount actions are readable at all of these and at `GET /v1/actions/{id}` and `GET /v1/actions?id=`. |

Not served yet; they answer 404 `not_found`: the snapshots (`GET /hetzner/v1/storage_boxes/{id}/snapshots`,
`POST /hetzner/v1/storage_boxes/{id}/snapshots`,
`GET /hetzner/v1/storage_boxes/{id}/snapshots/{snapshot_id}`,
`PUT /hetzner/v1/storage_boxes/{id}/snapshots/{snapshot_id}`,
`DELETE /hetzner/v1/storage_boxes/{id}/snapshots/{snapshot_id}`) and
`POST /hetzner/v1/storage_boxes/{id}/actions/rollback_snapshot`.

- The boxes live in the storage VM `<tag>-storage`, which the first
  `create_storage_box` creates and the delete of the last box removes;
  each box is a btrfs subvolume with a quota of its type's size. They are
  reached over SSH only: SFTP on port 22, and with `ssh_enabled` SFTP,
  scp, rsync and `borg serve` on port 23. Samba and WebDAV are not
  offered; `samba_enabled`, `webdav_enabled` and `zfs_enabled` have
  no effect. The server answers on every box address, so the
  credentials of one box also log in at another box's address.
- `reachable_externally` of the box or of one of its subaccounts opens
  the box address to the host and WSL2; servers always reach it.
- `ssh_keys` are written once at create into `.ssh/authorized_keys` of
  the box; OpenSSH lines and RFC 4716 blocks work on both ports.
- `POST /hetzner/v1/storage_boxes` answers with the box in status
  `initializing`: `username`, `server`, `system` and `snapshot_plan` are
  null and `stats` are 0. Once `create_storage_box` succeeds the box is
  `active` with `username` `u<n>` (`n` from 100000, never reused),
  `server` `u<n>.storagebox.hetzner.internal` and `system`
  `<LOCATION>-BX<id>`, such as `FSN1-BX1`. Subaccounts are
  `u<n>-sub<m>` at `u<n>-sub<m>.storagebox.hetzner.internal`.
- `location` must be one the type has a price for; `ssh_keys` must be in
  OpenSSH format. Passwords follow Hetzner's password policy for boxes and
  subaccounts, on create and on reset, but are not checked against known
  data breaches. HLab keeps only a sha512-crypt and an NT hash of a
  password.
- Invalid input answers 422 `invalid_input` at once, also where Hetzner
  fails the action instead, such as a `home_directory` with a leading
  slash at `POST /hetzner/v1/storage_boxes/{id}/subaccounts`.
- A box name is unique in the project, a subaccount name in its box;
  both answer 409 `uniqueness_error`.
- A failed or interrupted `create_storage_box` deletes the box and
  releases its address; the action carries the error.
  `DELETE /hetzner/v1/storage_boxes/{id}` succeeds only after the storage
  VM removed the box; otherwise the box stays and the action fails.
- A box that is not `active` answers 423 `locked` to every action except
  delete, to `POST /hetzner/v1/storage_boxes/{id}/subaccounts` and to
  `GET /hetzner/v1/storage_boxes/{id}/folders`.
- `GET /hetzner/v1/storage_boxes/{id}/folders` answers 422
  `invalid_input` on `path` for an absolute path or one with `..`, and
  404 `not_found` for a path that does not exist.
- `stats` are refreshed every 5 minutes and after each action of the box.
- Every action on a box or one of its subaccounts locks the box: while it
  runs, a second one, `DELETE` included, answers 423 `locked`. Hetzner
  documents this only for creating subaccounts and snapshots, see
  `POST /hetzner/v1/storage_boxes/{id}/actions/change_type` and the other
  actions. A box with delete protection answers 423 `protected` to
  `DELETE`.

## Projects

Projects and tokens are managed with the `hlab` command and in the tray
program, not through the API.
`hlab project delete` deletes a project with all its resources, servers
with delete protection included.

## WSL2

Servers and `api.hetzner.internal` are reachable from WSL2 only in NAT
networking mode with `dnsTunneling` on; see [WSL2](../../README.md#wsl2).
