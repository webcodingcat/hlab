# Known deviations from Hetzner

hlab answers in Hetzner's formats, at `http://api.hetzner.internal/v1`
with a token from `hlab token create`. This page lists where it behaves
differently from the real Hetzner Cloud API.

## Endpoints

hlab serves these endpoints:

| Endpoint | Notes |
|---|---|
| `GET /actions`, `GET /actions/{id}` | `GET /actions` needs at least one `id` parameter; `sort` accepts `id` and `status`. |
| `GET /locations`, `GET /locations/{id}` | Filter: `name`. |
| `GET /datacenters`, `GET /datacenters/{id}` | 410 `deprecated_api_endpoint`, as at Hetzner since 2026-10-01. |
| `GET /server_types`, `GET /server_types/{id}` | Filter: `name`. |
| `GET /images`, `GET /images/{id}` | System images only. Filters: `name`, `type`, `architecture`, `include_deprecated`. |
| `GET /ssh_keys`, `POST /ssh_keys`, `GET /ssh_keys/{id}`, `DELETE /ssh_keys/{id}` | Filters: `name`, `fingerprint`. No update. |
| `GET /servers`, `POST /servers`, `GET /servers/{id}`, `DELETE /servers/{id}` | Filter: `name`. No update and no server actions. |

Every other endpoint answers 404 `not_found`.

## All lists

Every list is returned as one page with a valid `meta.pagination`; `page`
and `per_page` are ignored. Filters other than those above, `label_selector`
and `sort` (except on `/actions`) are ignored.

## POST /servers

- The IPv6 /64 is assigned formally from the documentation range
  `2001:db8::/32`: the next 32 bits are the server ID (server 4 gets
  `2001:db8:0:4::/64`). The server gets no IPv6
  traffic and its guest has no IPv6 address configured.
- `root_password` is always `null`; password login is not supported yet.
  Log in with an SSH key as `root`.
- Only `name`, `server_type`, `image`, `location`, `ssh_keys`, `user_data`,
  `start_after_create` and `labels` are used. Other fields, such as
  `networks`, `volumes`, `firewalls`, `placement_group` and `public_net`,
  are ignored.
- The `create_server` action succeeds when the VM runs, before the guest
  has booted; SSH answers a few seconds later.
- Public IPv4 addresses come from the configured public range (default
  `198.19.0.0/16`), not from Hetzner's ranges. A deleted server's address
  is not handed out again at once: allocation continues after the last
  address given and starts over only at the end of the range.

## Projects

Projects and tokens are managed with the `hlab` command, not in a console.
A project can be deleted only when it has no resources left
(`hlab project delete` answers `project has resources`).
