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
| `GET /images`, `GET /images/{id}` | System images only. |
| `GET /ssh_keys`, `POST /ssh_keys`, `GET /ssh_keys/{id}`, `PUT /ssh_keys/{id}`, `DELETE /ssh_keys/{id}` | |
| `GET /servers`, `POST /servers`, `GET /servers/{id}`, `PUT /servers/{id}`, `DELETE /servers/{id}` | See [POST /servers](#post-servers). |
| `GET /servers/{id}/metrics` | See [Metrics](#metrics). |
| `POST /servers/{id}/actions/poweron`, `POST /servers/{id}/actions/poweroff`, `POST /servers/{id}/actions/shutdown`, `POST /servers/{id}/actions/reboot`, `POST /servers/{id}/actions/reset` | See [Server actions](#server-actions). |
| `POST /servers/{id}/actions/rebuild`, `POST /servers/{id}/actions/change_type`, `POST /servers/{id}/actions/reset_password` | See [Server actions](#server-actions). |
| `POST /servers/{id}/actions/change_protection`, `POST /servers/{id}/actions/add_to_placement_group`, `POST /servers/{id}/actions/remove_from_placement_group` | See [Server actions](#server-actions). |
| `POST /servers/{id}/actions/request_console` | 422 `unsupported_error`, see [Server actions](#server-actions). |
| `GET /placement_groups`, `POST /placement_groups`, `GET /placement_groups/{id}`, `PUT /placement_groups/{id}`, `DELETE /placement_groups/{id}` | See [Placement groups](#placement-groups). |

Lists page, filter and sort with the parameters of Hetzner's API
description. Every other endpoint answers 404 `not_found`.

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
  [configuration reference](configuration.md#settingstoml)). A stopped
  server holds no RAM.
- Only `name`, `server_type`, `image`, `location`, `ssh_keys`, `user_data`,
  `start_after_create`, `labels` and `placement_group` are used. Other
  fields, such as `networks`, `volumes`, `firewalls` and `public_net`, are
  ignored.
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
- `rebuild` accepts system images only.
- `reset_password` works through a small hlab guest hook that hlab
  installs at the first boot of every server it creates, not through
  qemu-guest-agent. The hook applies the new password within a few
  seconds; without a confirmation within 120 s the action fails with
  `server_error`. A server without the hook, such as one created by an
  earlier hlab version or one whose user data turns off cloud-init's
  vendor data, therefore cannot reset its password. On a server that is
  not `running` the action answers 409 `conflict`.
- `request_console` answers 422 `unsupported_error` with the message
  `server console is not supported by hlab yet` and starts no action.

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
