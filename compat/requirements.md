# Requirements of the tools

What the tools of [versions.yaml](versions.yaml) need from the API, found in their
source code or measured on the real API. Each entry names its source (a file in the
tool, or the measurement), the phase that brings it and the unit test that checks
it; "open" means the phase has not come yet. What never works is in
[exceptions.yaml](exceptions.yaml). A new entry: DEVELOPING.md, Recipes.

## Every endpoint

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Errors as `{"error": {"code", "message", "details"}}` with Hetzner's codes and status codes | hcloud-go `hcloud/schema/error.go`, `hcloud/error.go` | 1a | `internal/web`: `TestErrorsUseHetznersFormat` |
| `X-Correlation-Id` in every answer once the token is checked: hcloud-go adds it to its error texts | hcloud-go `hcloud/error.go` (`Error.Error`); measured | 1a | `internal/web`: `TestCorrelationIDOnceTheTokenIsChecked` |
| 401 `unauthorized` without or with an unknown token; 403 `forbidden` for a `read` token on a changing request | hcloud-go `hcloud/error.go` (`ErrorCodeUnauthorized`, `ErrorCodeForbidden`); measured | 1a | `internal/web`: `TestTokenCheck` |
| Pagination: `page`, `per_page` (default 25, at most 50, invalid values fall back), `meta.pagination` with `null` where there is no page; clients page until `next_page` is empty | hcloud-go `hcloud/client_helper.go` (`iterPages`), `hcloud/schema/meta.go`; measured | 1a | `internal/web`: `TestPaginate` |
| `sort=field[:asc|:desc]`, several in order; contrary orders of one field are ignored | hcloud-go `hcloud/action.go` and the other list options (`Sort`); measured | 1a | `internal/web`: `TestSort` |
| Label selectors `k=v`, `k==v`, `k!=v`, `k`, `!k`, `k in (…)`, `k notin (…)`, joined by commas | hcloud-go `hcloud/client.go` (`ListOpts.LabelSelector`), provider data sources (`with_selector`) | 1a | `internal/web`: `TestSelector` |
| `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`; 429 `rate_limit_exceeded`; 3600 per hour and project, and 7200 of their own for the actions endpoints | hcloud-go `hcloud/client_handler_rate_limit.go`, `hcloud/client_handler_retry.go` (`retryPolicy`); measured | 1a | `internal/web`: `TestRateLimit`, `TestActionsHaveTheirOwnRateLimit` |
| Error codes and fields exactly as Hetzner: `locked`, `target_already_defined`, `uniqueness_error`, `invalid_input` with `details.fields`, `not_found`, `conflict` | hcloud-go `hcloud/error.go` (`ErrorCode…`, `IsError`) | 1a (format), each resource's phase | `internal/web`: `TestErrorsUseHetznersFormat`; open per resource |

## Actions

| Requirement | Source | Phase | Test |
|---|---|---|---|
| `GET /actions?id=…` returns the actions of the IDs; without IDs it is 410 `deprecated_api_endpoint` (listing all actions is gone since 2025-01-30); `GET /actions/{id}` | hcloud-go `hcloud/action.go` (`AllWithOpts`, `GetByID`), `hcloud/action_waiter.go`; measured | 1a | `internal/api/cloud`: `TestActions`, `TestActionsNeedIDs` |

## Catalog and images

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Hetzner's real IDs in the catalog: types cpx12 = 108, cpx22 = 109, location hel1 = 3, and the other IDs of Hetzner's catalog | `hetzner.hcloud` integration targets (`server_type_info`, `location_info`) | 1a | `internal/catalog`: `TestBuiltinCatalogHasHetznersIDs`; `internal/api/cloud`: `TestCatalogResourcesListAndGet` |
| `GET /datacenters` and `/datacenters/{id}` are 410 `deprecated_api_endpoint`, `"API functionality was removed"`, with the announcement `https://docs.hetzner.cloud/changelog#2026-06-02-datacenters-deprecated`; no other answer names a datacenter (not even the Primary IP prices, which `hcloud-go/schema` still has) | measured | 1a | `internal/api/cloud`: `TestDatacentersAreRemoved`, `TestNoAnswerNamesADatacenter`, `TestPricingHasEveryType` |
| Lists of locations, server types, load balancer types, images and ISOs filter by `name` (the whole name): `GetByName` of every client | hcloud-go `hcloud/location.go`, `server_type.go`, `load_balancer_type.go`, `image.go`, `iso.go` (`…ListOpts.Values`) | 1a | `internal/api/cloud`: `TestCatalogResourcesListAndGet` |
| Images filter by `type`, `status`, `bound_to` (each repeatable), `name`, `architecture`, `label_selector`; deprecated ones only with `include_deprecated=true`; `sort` by `id`, `name`, `created` | hcloud-go `hcloud/image.go` (`ImageListOpts`, `GetByNameAndArchitecture`) | 1a | `internal/api/cloud`: `TestImageFilters` |
| ISOs filter by `architecture`; with `include_architecture_wildcard=true` also the ISOs without one | hcloud-go `hcloud/iso.go` (`ISOListOpts`) | 1a | `internal/api/cloud`: `TestISOArchitectureFilter` |
| Server types with their locations (ID, name, `available`, `recommended`, `deprecation`) and prices per location | hcloud-go `hcloud/schema/server_type.go`, `hcloud/server_type.go` | 1a | `internal/api/cloud`: `TestServerTypesNameTheirLocations` |
| Default images of the tools are older (`ubuntu-24.04`, `debian-12`); the lab has only the newest ones, the smoke tests set `debian-13`, unknown images get Hetzner's error | provider and CLI defaults; `hetzner.hcloud` `tests/integration/targets/*/defaults` | 1b | `internal/api/cloud`: `TestServerCreateErrors` |
| Load balancer types with all fields; required fields of `update_service` | provider `internal/loadbalancer` | 4 | open |

## SSH keys

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create, read, update (name, labels; a missing field stays), delete (204); keys of another project are `not_found` | hcloud-go `hcloud/ssh_key.go` (`Create`, `GetByID`, `Update`, `Delete`) | 1a | `internal/api/cloud`: `TestSSHKeyLifeCycle`, `TestSSHKeysOfOtherProjectsAreNotFound` |
| The fingerprint is the MD5 of the key, hex pairs with colons; lists filter by `name`, `fingerprint`, `label_selector` and sort by `id`, `name` | hcloud-go `hcloud/ssh_key.go` (`GetByFingerprint`, `SSHKeyListOpts`) | 1a | `internal/api/cloud`: `TestSSHKeyFilters`, `TestSSHKeyLifeCycle` |
| A name or fingerprint the project has already: 409 `uniqueness_error`, `"SSH key not unique"`, `details.fields` `[{"name": "name"}]` or `[{"name": "public_key"}]`; the name is checked first | measured; hcloud-go `ErrorCodeUniquenessError` | 1a | `internal/api/cloud`: `TestSSHKeyNamesAndFingerprintsAreUnique` |
| Invalid fields: 422 `invalid_input` with `details.fields` (every field, its messages); the texts below | measured; hcloud-go `hcloud/error.go` (`ErrorDetailsInvalidInput`) | 1a | `internal/api/cloud`: `TestSSHKeyRequestsAreChecked`; `internal/web`: `TestCheckCollectsEveryField`, `TestDecodeJSONErrors` |
| Labels follow Hetzner's rules: key with an optional DNS prefix, name and value up to 63 characters of letters, digits, `-_.`, starting and ending alphanumeric; the prefix `hetzner.cloud/` is accepted | Hetzner's documentation ("Labels"); measured | 1a | `internal/web`: `TestCheckLabels`, `TestCheckLabelMessages` |
| `public_key` is kept as given, comment and line end included | measured | 1a | `internal/api/cloud`: `TestSSHKeyLifeCycle` |

## Servers

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create, read, update (name, labels), delete; `POST /servers` answers 201 with `server` (`initializing`, `included_traffic` 0), `action` (`create_server`, resources server and image), `next_actions` (`start_server`, none with `start_after_create` false) and `root_password` (null with SSH keys) | hcloud-go `hcloud/server.go` (`Create`, `ServerCreateResult`); measured | 1b | `internal/api/cloud`: `TestServerLifeCycle`, `TestServerWithoutKeysGetsARootPassword` |
| The server's JSON: the spec's fields in the real API's order, no `datacenter`, `locked` `false`, `public_net.ipv4`/`ipv6` `null` when missing | hcloud-go `hcloud/schema/server.go`; measured | 1b | `internal/api/cloud`: `TestServerJSONHasHetznersFields`, `TestPrimaryIPAssignedOnCreate` |
| `poweron`, `poweroff`, `shutdown`, `reboot`, `reset`, `change_protection` with Hetzner's action names; actions sent while another runs wait their turn instead of 423 | hcloud-go `hcloud/server.go`; measured | 1b | `internal/api/cloud`: `TestServerLifeCycle`, `TestServerActionsWaitForEachOther` |
| `DELETE /servers/{id}` answers 200 with `delete_server`; the server is gone at once; Primary IPs with `auto_delete` go with it | hcloud-go `hcloud/server.go` (`DeleteWithResult`); measured | 1b | `internal/api/cloud`: `TestServerLifeCycle`, `TestPrimaryIPLifeCycle` |
| Lists filter by `name`, `label_selector`, `status` and sort by `id`, `name`, `created`; `/servers/actions`, `/servers/{id}/actions` and their single actions | hcloud-go `hcloud/server.go` (`ServerListOpts`), `hcloud/action.go` | 1b | `internal/api/cloud`: `TestServerUpdateAndList`, `TestServerActionLists` |
| Create errors with Hetzner's codes and texts (below) | measured | 1b | `internal/api/cloud`: `TestServerCreateErrors`, `TestServerDeleteProtection` |

## Primary IPs

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create in a location (no action) or for a server that is off (`assign_primary_ip`); read, update (name, labels, `auto_delete`), delete (204); `assign`, `unassign`, `change_protection` | hcloud-go `hcloud/primary_ip.go`; provider `hcloud_primary_ip` | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle`, `TestPrimaryIPAssignedOnCreate` |
| An unassigned IP has `assignee_id` `null` and `assignee_type` `"unassigned"`; an IPv4's `dns_ptr` lists Hetzner's default name | measured; Hetzner's changelog 2026-04-27 | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle` |
| A server's own IPs are named `primary_ip-<id>`, with `auto_delete` true; `unassign` clears `auto_delete` | measured | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle` |
| Lists filter by `name`, `ip`, `label_selector` and sort by `id`, `created`; `/primary_ips/actions` and `/primary_ips/{id}/actions` | hcloud-go `hcloud/primary_ip.go` (`PrimaryIPListOpts`) | 1b | `internal/api/cloud`: `TestPrimaryIPAssignedOnCreate`, `TestPrimaryIPLists` |
| Errors with Hetzner's codes and texts, details `null` (below); a new server's IP that it cannot take has codes and texts of its own, details `{}` | measured | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle`, `TestPrimaryIPCreateErrors`, `TestPrimaryIPsAServerCannotTake` |

## Gaps of the old HLab

The dry run of the hcloud CLI against the Python HLab found 58 gaps in fidelity. Each
becomes an entry here, with the phase of its resource, when that phase starts.

## Measured on the real API

Answers of `api.hetzner.cloud` on 2026-11-07, with and without a read token; HLab
answers the same (`internal/web`, `internal/api/cloud`).

| Request | Answer |
|---|---|
| a path no route takes (`/v1/nothing`, `/nothing`, `/v1/actions/abc`), with or without token | 404 `not_found`, `"api route not found"`, `details` `null`; checked before the token; no `X-Correlation-Id` |
| a known route without token | 401 `unauthorized`, `"token is required"`, no `details`; no `X-Correlation-Id`, no `RateLimit-*` |
| a known route with an unknown token | 401 `unauthorized`, `"the token you have provided is invalid"`, no `details` |
| a known route with another method (`PUT /v1/actions/1`) | without token 401; with token 405 `method_not_allowed`, `"method not allowed"`, `details` `null`, `Allow: GET, HEAD` |
| a read token on `POST /v1/ssh_keys` | 403 `forbidden`, `"permission denied"`, `details` `{}` |
| `GET /v1/actions` | 410 `deprecated_api_endpoint`, `"API functionality was removed"`, `details.announcement` the changelog entry |
| `GET /v1/actions?id=abc` | **406** `invalid_input`, `"invalid integer value"`, `details` `null` |
| `GET /v1/actions/1` (an action of no project of the token) | 404, `Content-Type: text/plain; charset=utf-8`, body `Not Found`; with `X-Correlation-Id` and `RateLimit-*` (a spec deviation) |
| `RateLimit-*` with a token | `/v1/actions…` and every resource's action endpoints (below): limit 7200; `/v1/ssh_keys…` and the rest: 3600; the two count apart (remaining 7199 and 3599 side by side); `RateLimit-Reset` is when the budget is full again |
| `?sort=bogus` | 422 `invalid_input`, `"invalid sort value"`, `details.fields` `[{"name": "sort", "messages": ["value 'bogus' is not allowed"]}]` |
| `?sort=id:asc&sort=id:desc` | 200, the order ignored |
| `?label_selector=env in (a` | 422 `invalid_input`, `"invalid label_selector: invalid syntax: missing brackets for group"`, `details` `{}` |
| `?page=0&per_page=1000` | page 1, `per_page` 50; `Link` header with the rest of the query |
| `?page=99` (one page in all) | page 99, `previous_page` and `next_page` `null`, `last_page` 1 |

### Catalog and SSH keys

Measured on 2026-11-07 with a read token and, for the bodies, a read-write token of
an empty test project. The catalog (`internal/catalog/catalog.yaml`) is taken from
these answers.

| Request | Answer |
|---|---|
| `GET /ssh_keys/1`, `PUT` or `DELETE` of a missing key | 404 `not_found`, `"SSH key not found"`, `details` `{}` |
| `GET /server_types/999`, `/images/1`, `/isos/1` | 404 `not_found`, `"server type not found"`, `"image not found"`, `"iso not found"`, `details` `{}` |
| `GET /locations/999`, `/load_balancer_types/999` | 404 `not_found`, `"location with ID 999 not found"`, `"load balancer type not found"`, `details` `null` |
| `GET /server_types/1` (cx11, gone from the list) | 200: removed types stay readable by ID; the lab knows only the listed ones |
| server types, load balancer types, images | no `deprecated` and no top-level `included_traffic` any more; `deprecation` only |
| `GET /load_balancer_types` | prices with four decimals (`"0.0112"`); in `GET /pricing` ten (`"0.0112000000"`), gross sixteen |
| `GET /pricing` | `vat_rate` `"19.000000"`; no `traffic`, no `datacenter` |
| `GET /server_types?sort=name:desc` / `?sort=bogus` | sorted / 422 `service_error`, `"internal error"`, `details` `{}` |
| `GET /load_balancer_types?sort=…` | any sort value ignored, the list by ID |
| `GET /locations?sort=bogus` | 200, the list by ID |
| `GET /isos?sort=name:desc` / `?sort=created` | sorted / 422 `"invalid sort value"` (`id` and `name` only) |
| `GET /images?type=bogus` (also `status`, `architecture`) | 422 `invalid_input`, `"invalid type value"`, `details` `{}` |
| `GET /images?name=debian-11` (deprecated) | empty without `include_deprecated=true` |
| `POST /ssh_keys` `{}` or with `null` fields | 422 `invalid_input`, `"invalid input in fields 'name', 'public_key'"`, each `"Missing data for required field."` |
| `POST /ssh_keys` with one invalid field | `"invalid input in fields 'public_key'"` (plural even for one) |
| `PUT /ssh_keys/{id}` with an invalid field | `"invalid input in field 'name'"` (singular) |
| name `""` or longer than 128 | `"Length must be between 1 and 128."` |
| `public_key` with a leading space / unreadable / unknown type | `"Must not begin with a whitespace character."` / `"SSH key type is invalid (supported types: ssh-ed25519, …"` (the bracket is not closed) |
| labels on POST / on PUT | `"invalid label key"`, `"invalid label value"` / `"key contains invalid characters or is malformed"`, `"value contains invalid characters or is malformed"` |
| a field of the wrong type (`"name": 5`, a label value `5`), or `[1]` | 422 `invalid_input`, `"failed to parse json"`, `details` `{}` |
| a body cut off (`{"name": `) | 422 `json_error`, `"unexpected EOF"`, `details` `{}` |
| data after the JSON (`{…} garbage`, `{…} {…}`; read-write token, 2026-11-07, 1a-R) | 201, the key of the first object; the rest ignored (`TestDecodeJSONIgnoresWhatFollowsTheFirstValue`) |
| `PUT` with `{}` or `"labels": null` | 200, nothing changed |

### Label selectors, bodies and servers (1a-6)

Measured on 2026-11-07 with a read token on `GET /ssh_keys?label_selector=…` and, for
the bodies, a read-write token of an empty test project. Every selector error is 422 `invalid_input`, `details` `{}`, with the
message `"invalid label_selector: "` and the reason below; HLab gives the same
(`internal/web`: `TestSelectorRefusesWhatItCannotParse`,
`TestSelectorTakesWhatHetznerTakes`; `internal/api/cloud`:
`TestSSHKeyRequestsAreChecked`; `internal/web`: `TestDecodeJSONErrors`).

| Request | Answer |
|---|---|
| `=`, `=x` | `invalid syntax: '=' operator requires value before` |
| `k=`, `k==`, `k!=`, `k=v=` | `invalid syntax: '=' (or '==', '!=') operator requires value afterwards`: an empty value is no value |
| `!` / `k!`, `k=v!` | `invalid syntax: '!' operator requires value afterwards` / `… must be in front of value` |
| `a b`, `kin (a)` | `invalid syntax: only one key allowed` |
| `,`, `a,,b`, `k=v,,` | `invalid syntax: no key given`; one comma at the end (`k=v,`) is taken |
| `k in a`, `k notin a`, `k in (a,b`, `k in (a,(b))` | `invalid syntax: missing brackets for group` |
| a key against the label rules (`k$=v`, `-k=v`, `a/b/c=d`, 70 characters, `(k)`, `env)`), and `k in ()` | `key contains invalid characters or is malformed` |
| a value against the label rules (`k=-v`, `k=(v)`, 70 characters), an empty value in a group (`k in (a,)`), `k in (a b)` | `value contains invalid characters or is malformed` |
| `k = v`, `! k`, `k in ( a , b )`, `k in (a),j`, `k=a,b`, `a.b/c=d`, `hetzner.cloud/x=y` | 200 |
| `PUT /ssh_keys/{id}` with `name` and `labels` both invalid, in either order in the JSON | only the first field in the check's order (`name`, then `labels`): `"invalid input in field 'name'"`, `details.fields` with `name` alone |
| `PUT /ssh_keys/999999999` (missing) with an invalid body / a valid one | 422 as above: the body is checked before the key is looked up / 404 |
| `POST /ssh_keys` with a body of 1 MiB + 1, 2, 11, 32 and 64 MiB (`{` spaces `}`) | 422 for the missing fields: no limit up to 64 MiB; with a read token 403 first |
| `GET /actions`, `/servers/actions`, `/servers/{id}/actions`, `POST /servers/{id}/actions/poweroff`, `/floating_ips/actions` | one budget of 7200 for all of them (remaining counts down across them); `/servers`, `POST /servers` and the rest take from the 3600 |
| `GET /servers/actions` without `id` | 200, a paginated list (unlike `GET /actions`, not removed); `?status=bogus` 422 `"invalid status value"` with `details.fields` |
| `GET /servers/999999999/actions` (missing server) | 200, empty list; `…/actions/1` 404 `"action not found"`, `details` `{}` |
| `POST /servers/999999999/actions/poweroff` | read token 403; read-write 404 `"server not found"`; an unknown command (`…/actions/bogus`) is 404 `"api route not found"` before the token |
| `GET /servers/999999999` | 404 `"server not found"`, `details` `{}` |

**What the selectors select.** Four SSH keys of the test project with the labels
`k=v`, `k=w`, none and `k=a`, deleted afterwards (`internal/web`: `TestSelector`):

| Selector | Selects |
|---|---|
| `k!=v` | `k=w`, `k=a`: **`!=` needs the label**; `k notin (v)` selects the key without it too |
| `!k=v`, `!k!=v`, `!!k` | as `k=v`, `k!=v`, `!k`: a `!` in front is ignored before a comparison |
| `k=!v`, `k!=!v`, `k=!w` | every key, also the one without `k` |
| `k=a=b`, `k=a=v` | none |
| `!k,k` | none |

HLab selects the same. Where it differs, knowingly: `k in(a)` is a malformed key on
Hetzner, `only one key allowed` in HLab. HLab reads bodies up to 64 MiB, the largest
measured, and answers `json_error` `"http: request body too large"` above it.

**A real server.** With the operator's consent, one `cx23` in `hel1` (the cheapest
x86 type), `debian-13`, IPv6 only, created, read, powered off and deleted within a
minute; nothing is left in the project. The checks belong to phase 1b:

| Request | Answer |
|---|---|
| `POST /servers` | 201 with `server`, `action` (`create_server`, resources server and image), `next_actions` (`start_server`) and `root_password` (no SSH key given); the server `initializing`, `included_traffic` 0 |
| `GET /servers/{id}`, running | exactly the fields of the spec, in this order: `id`, `name`, `status`, `server_type`, `location`, `image`, `iso`, `primary_disk_size`, `labels`, `protection`, `backup_window`, `rescue_enabled`, `locked`, `placement_group`, `public_net`, `private_net`, `load_balancers`, `volumes`, `included_traffic`, `ingoing_traffic`, `outgoing_traffic`, `created`; **no `datacenter`**; `locked` `false`; `included_traffic` 21990232555520 (20 TiB); `ingoing_traffic`/`outgoing_traffic` 0; `backup_window`, `iso`, `placement_group` `null`; `volumes`, `load_balancers`, `private_net` `[]` |
| `public_net` without IPv4 | `ipv4` `null`; `ipv6` with `id`, `ip`, `blocked` `false`, `dns_ptr` `[]`; `floating_ips`, `firewalls` `[]` |
| `server_type` inside the server | the full type with `locations` and `prices` (both locations), `deprecation` `null` |
| `POST …/actions/poweroff`, then at once `…/actions/reboot` | both 201: **the second action starts while the first runs**, no 423; the server stays `running`, `locked` `false`; both `running` in `GET /servers/{id}/actions` |
| action IDs | far above 2^32 (15 digits) |

Settled in 1b-4 (below): Hetzner runs a server's actions one after another
and answers none of the power actions with 423; HLab's server table is
queued.

### Servers and Primary IPs (1b-4)

Measured on 2026-11-08 with the read-write token of the empty test project
(`cx23` in `fsn1`, `debian-13`; `cx23` in `hel1` had no room: 412
`resource_unavailable`, `"error during placement"`). Everything was deleted
afterwards. HLab answers the same.

| Request | Answer |
|---|---|
| `POST /servers` with an unknown image / type / location | 422 `invalid_input` `"image not found"` / 404 `"server type not found"` / 404 `"datacenter not found"`, each `details` `{}` |
| `POST /servers` with name `bad_name` or `""` | 422 `"invalid input in field 'name'"`, `"name must be a valid hostname"` (singular "field", unlike SSH keys) |
| `POST /servers` without `image`; `user_data` of 32769 bytes | `"Missing data for required field."`; `"Length must be between 0 and 32768."` |
| `POST /servers` with an unknown SSH key / Primary IP | 404 `not_found` `"SSH key not found"` / 404 `primary_ip_not_found` `"primary ip not found"` |
| `POST /servers` with a taken name | 409 `uniqueness_error`, `"server name is already used"`, `details` `{}` |
| `POST /servers` with `enable_ipv4` and `enable_ipv6` false, no network | 201: a server without public addresses |
| status after the create | `initializing` ~4 s, `starting`, `running`; `create_server` ends after 4 s |
| `poweroff`, `change_protection` during the create; `poweron`, `reset`, `shutdown`, `reboot` during a `poweroff` | all 201; they run in order (each starts when the one before ended) and the server ends as the last one leaves it |
| `reboot` or `reset` of a server that is off | 201, the server starts |
| `shutdown` of a running Debian | `shutdown_server` ends after 1 s; the server is `off` 12 s later |
| `DELETE /servers/{id}` (protected) | 423 `protected`, `"server deletion is protected"`, `details` `{}` |
| `DELETE /servers/{id}` | 200 with `delete_server`; `GET` answers 404 at once |
| `POST /primary_ips` | 201, `action` `null`; `assignee_type` `"unassigned"`, `dns_ptr` `[{"ip", "dns_ptr": "static.<reversed>.clients.your-server.de"}]` (IPv6: `[]`) |
| `POST /primary_ips` without or with both `assignee_id` and `location` | 422 `"invalid input in fields 'assignee_id', 'location'"`, each `"either assignee_id or location must be provided"` |
| `POST /primary_ips` with `type` `bogus` / a taken name | 422 `"invalid input in field 'type'"`, `"value required to be smaller"` / 409 `uniqueness_error` `"name is already used"`, `details` `null` |
| `assign` / `unassign` on a running server | 422 `server_not_stopped`, `"Server must be offline for this action"`, `details` `null` |
| `assign` to a server that has an IP of the type | 403 `resource_limit_exceeded`, `"assignment limit reached"`, `details` `null` |
| `unassign` | 201, `unassign_primary_ip` (resources IP and server), running for some seconds; then `auto_delete` is `false` |
| `DELETE` while the IP's unassign runs / assigned / protected | 423 `locked` `"cannot perform operation because primary_ip is locked"` / 422 `must_be_unassigned` `"Primary IP must be unassigned"` / 423 `protected` `"Primary IP deletion is protected"`, all `details` `null` |
| `PUT /primary_ips/{id}` with `auto_delete` true on a protected IP | 200 |
| an IP that is gone | 404 `not_found`, `"Primary IP with ID <id> not found"`, `details` `null` |
| `POST /servers` with a Primary IP of the other type (held by another server too) / held by another server / of another location | 412 `primary_ip_version_mismatch` `"primary ip has a wrong ip version"` / 422 `primary_ip_assigned` `"primary ip already assigned to another server"` / 422 `primary_ip_location_mismatch` `"primary ip and server located in different locations"`, all `details` `{}`; the type is checked first |
| `assign` of an IP held by another server / of another location / to a missing server | 422 `primary_ip_already_assigned` `"Primary IP with ID <id> is already assigned to a Resource"` / 422 `service_error` `"Resource and Primary IP are in different locations"` / 404 `not_found` `"resource not found"`, all `details` `null` |

### Metadata service (1b-2)

Read with `curl` on one fresh Debian 13 server per location, on 2026-11-08,
after the datacenters' removal (`GET /datacenters` answers 410).

| What | Answer |
|---|---|
| `availability-zone` | `fsn1-dc14`, `nbg1-dc3`, `hel1-dc2`, `ash-dc1`, `hil-dc1`, `sin-dc1`: the old datacenter names stay |
| `region` | the location's network zone: `eu-central`, `us-east`, `us-west`, `ap-southeast` |
| `/hetzner/v1/metadata`, keys | `instance-id`, `hostname`, `region`, `availability-zone`, `local-ipv4` (`""`), `network-config`, `vendor_data`, `public-ipv4`, `public-keys`; **no `private-networks`** on a server without a network |
| `network-config` | version 1, `eth0` with its MAC: `dhcp` for IPv4, the `::1` of the server's `/64` with gateway `fe80::1` and the IPv6 name servers |

Still open, for 2a: HLab's summary carries `private-networks: []` on a server
without a network, Hetzner's leaves the key out.
