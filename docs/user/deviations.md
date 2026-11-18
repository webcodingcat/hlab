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
- `root_password` is always `null`; password login is not supported yet.
  Log in with an SSH key as `root`.
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

- `request_console` answers 422 `unsupported_error` with the message
  `server console is not supported by hlab yet` and starts no action.
- Power actions (`poweron`, `shutdown`, `reboot` and the others) are not
  served yet. A server is `off` only when it was created with
  `start_after_create: false`, so only such a server can be added to a
  placement group with `add_to_placement_group`.
- `change_protection` stores the rebuild protection, but there is no
  rebuild action yet that it could refuse.

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
