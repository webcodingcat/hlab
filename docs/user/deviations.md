# Known deviations from Hetzner

hlab answers in Hetzner's formats, at `http://api.hetzner.internal/v1`
with a token from `hlab token create`. This page lists where it behaves
differently from the real Hetzner Cloud API.

## Endpoints

hlab serves these endpoints:

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
| `GET /networks`, `POST /networks`, `GET /networks/{id}`, `PUT /networks/{id}`, `DELETE /networks/{id}` | See [Networks](#networks). |
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

Lists page, filter and sort with the parameters of Hetzner's API
description. Every other endpoint answers 404 `not_found`, among them
`change_dns_ptr` of servers, primary IPs and floating IPs.

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
  [configuration reference](configuration.md#settingstoml)); `rebuild`
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
  hlab runs only x86 servers.
- The `create_server` action succeeds when the VM runs, before the guest
  has booted; SSH answers a few seconds later.
- Public IPv4 addresses come from the configured public range (default
  `198.19.0.0/16`), not from Hetzner's ranges. A deleted server's address
  is not handed out again at once: allocation continues after the last
  address given and starts over only at the end of the range.

## Server actions

- `shutdown` asks the guest to shut down and succeeds once the request is
  sent. The server shows `stopping` until the guest has turned the VM
  off, then `off`; a guest that ignores the request stays `stopping`
  while it runs.
- `rebuild` takes a system image, or a snapshot or backup of the project
  by ID. An image whose `disk_size` exceeds the server type's disk answers
  422 `invalid_input` on the field `image`.
- After a `rebuild`, cloud-init in the guest sees the instance-id
  `<server id>-<action id>` (the ID of the rebuild action), so it runs its
  first-boot steps again, also on a disk restored from a snapshot or
  backup of the same server. The metadata service keeps answering
  `/hetzner/v1/metadata/instance-id` with the server ID.
- `reset_password` works through a small hlab guest hook that hlab
  installs at the first boot of every server it creates, not through
  qemu-guest-agent. The hook applies the new password within a few
  seconds; without a confirmation within 120 s the action fails with
  `server_error`. A server without the hook, such as one created by an
  earlier hlab version or one whose user data turns off cloud-init's
  vendor data, therefore cannot reset its password. On a server that is
  not `running` the action answers 409 `conflict`. When the cloud stops
  during the action, the action ends with `server_error` at the next
  start, and the server no longer receives the new password.
- `request_console` answers 422 `unsupported_error` with the message
  `server console is not supported by hlab yet` and starts no action.

## Snapshots and backups

- `create_image` copies the server's disk while the server keeps running,
  through a Hyper-V checkpoint. The copy is crash-consistent: it holds what
  a power cut at that moment would leave on the disk. Without
  `description`, the image gets `<server name> <creation time>`.
- `image_size` is the size of the copied disk file, not a compressed size.
- `backup_window` shows `22-02` while backups are enabled; hlab does not
  use it. hlab checks every hour while the cloud runs and makes one daily
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
- Rescue is disabled when hlab boots the server into it, through
  `poweron`, `reboot` or `reset`, or 60 minutes after `enable_rescue`
  without such a boot. A reboot started inside the guest also boots the
  rescue system, but hlab does not notice it: rescue then stays enabled,
  and every later boot runs the rescue system, until `disable_rescue` or
  the 60 minutes have passed.
- `reboot` of a server running the rescue system shuts the guest down and
  starts the VM again; it fails when the guest is not off within two
  minutes.

## ISOs

- `/isos` lists the five ISOs of hlab's catalog (Ubuntu live server
  22.04.5, 24.04.5 and 26.04.1, Debian netinst 12.15.0 and 13.7.0) with
  hlab's own IDs from 130001, not Hetzner's ISOs; there are no private
  ISOs. See the [configuration reference](configuration.md#isos).
- The first `attach_iso` of an ISO downloads it inside the action, which
  takes minutes.
- An unknown or deprecated ISO answers 422 `invalid_input` on the field
  `iso`. `detach_iso` on a server without an ISO succeeds and changes
  nothing.
- The server has a second DVD drive holding its cloud-init seed; the
  system booted from the ISO sees both.

## Volumes

- `linux_device` is `/dev/disk/by-id/scsi-0HC_Volume_<id>`. The link, the
  automatic mount and the pick-up of a grown volume come from hlab guest
  hooks that hlab installs at the first boot of every server it creates.
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
  `vswitch subnets are not supported by hlab`. `expose_routes_to_vswitch`
  is stored and shown but has no effect.
- The gateway of a network is the first host address of its `ip_range`
  (`10.0.0.1` for `10.0.0.0/16`); every subnet shows it as `gateway`, also
  a subnet of type `server`. A server gets the lowest free host address of
  a subnet in its location's network zone; the network and broadcast
  address of each subnet, the gateway and `172.31.1.1` are never handed
  out.
- Private adapters have MAC addresses `02:48:4c:2x:xx:xx`; Hetzner uses
  `86:00:00:xx:xx:xx`. Tools that recognise Hetzner's private adapters by
  their MAC, such as hc-utils, do not see hlab's.
- hlab has no DHCP server. A network a server gets at creation is set up
  by cloud-init from hlab's seed at boot: the private IP as `/32` and a
  route to the network range via the gateway, on-link, as Hetzner's DHCP
  gives it. A network attached or detached while the server runs is set
  up or removed within a few seconds by the hlab guest hook that hlab
  installs at the first boot of every server it creates; every later boot
  sets up the current networks again. A server without the hook, such as
  one created by an earlier hlab version or one whose user data turns off
  cloud-init's vendor data, picks up later attachments only if you
  configure the adapter yourself.
- The MTU on private networks is 1500; Hetzner uses 1450.
- As at Hetzner, alias IPs and routes to destinations outside the
  network range are not configured in the guest; see
  [Networks](../../README.md#networks).
- `DELETE /networks/{id}` detaches all servers of the network and removes
  their adapters before it answers.

Hetzner documents no error for these cases; hlab answers:

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
- A primary IP created without a name is named `primary_ip-<id>`, as are
  the ones hlab makes for a new server.
- Servers created by an earlier hlab version get primary IP records for
  their addresses at the next cloud start, assigned and with
  `auto_delete: true`.
- `change_protection` with `delete: true` on a primary IP with
  `auto_delete` answers 422 `invalid_input` on the field `delete`.
- `DELETE /primary_ips/{id}` of an assigned primary IP answers 422
  `must_be_unassigned`; unassign it first.
- `dns_ptr` is always an empty list.

## Floating IPs

- An IPv4 floating IP comes from the configured public range. hlab routes
  it to the public IPv4 of its server, so a server without public IPv4
  receives no traffic for it. The guest has to configure the address,
  as at Hetzner; see [Primary and floating IPs](../../README.md#primary-and-floating-ips).
- An IPv6 floating IP is formal and carries no traffic:
  `2001:db8:c000:<id>::/64` (ID 12: `2001:db8:c000:c::/64`).
- A floating IP is assigned only to a server in the network zone of its
  home location; another server answers 422 `invalid_input` on the field
  `server`. Assigning it to the server that holds it answers 422
  `floating_ip_assigned`; assigning it to another server moves it.
- A floating IP created without a name is named `floating_ip-<id>`.
- `DELETE /floating_ips/{id}` of an assigned floating IP answers 422
  `must_be_unassigned`; unassign it first.
- `dns_ptr` is always an empty list.

## Firewalls

- A firewall filters the traffic of a server's public IPv4 and of the
  floating IPs assigned to the server. Hetzner does not document whether
  its firewalls filter floating IP traffic.
- IPv6 rules are accepted and stored but have no effect; servers carry no
  IPv6 traffic in hlab.
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

Hetzner documents no error for these cases; hlab answers:

| Case | Answer |
|---|---|
| `remove_from_resources` of a server or label selector the firewall is not applied to | 404 `firewall_resource_not_found` |
| `apply_to_resources` of a label selector the firewall already has | 422 `firewall_already_applied` |
| `apply_to_resources`, `POST /firewalls` or `POST /servers` that would give a server a sixth firewall | 403 `resource_limit_exceeded`; a label selector that matches such a server skips it |
| `DELETE /firewalls/{id}` of a firewall applied only through a label selector | 422 `resource_in_use` |
| `POST /servers` with a firewall that does not exist | 404 `not_found`; no server is created |
| `POST /servers` with `firewalls` and without public IPv4 | 422 `private_net_only_server` |
| a firewall listed twice in the `firewalls` of `POST /servers` | applied once |

## Metrics

- hlabd samples every running server every 10 s and keeps the samples
  24 hours in memory. They are lost when the cloud stops, and a stopped
  server has no samples for its stopped time. The smallest `step` is 10 s.
- `disk.0` is the sum of all disks of the server's VM.

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

hlab runs every server on your one machine, so a placement group places
nothing physically: a `spread` group only records its member servers.

- `POST /placement_groups` answers with `"action": null`.
- A spread group holds at most 10 servers. The eleventh, whether added at
  `POST /servers` or with `add_to_placement_group`, answers 422
  `placement_error`.
- `DELETE /placement_groups/{id}` also deletes a group that still has
  members; its servers then show `"placement_group": null`.

## Server types and prices

- `/server_types` lists the server types of hlab's catalog, followed by
  your own types from `server-types.custom.json` (see the
  [configuration reference](configuration.md#custom-server-types)).
  `hlab catalog update` replaces the catalog types with the current ones
  from Hetzner.
- `/pricing` shows the prices of hlab's catalog. Its `server_types` list
  every served type; an own type without `prices` gets zero prices in each
  of its locations.

## Projects

Projects and tokens are managed with the `hlab` command, not in a console.
`hlab project delete` deletes a project with all its resources, servers
with delete protection included.

## WSL2

Servers and `api.hetzner.internal` are reachable from WSL2 only in NAT
networking mode with `dnsTunneling` on; see [WSL2](../../README.md#wsl2).
