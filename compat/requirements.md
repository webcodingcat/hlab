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
| A location the server type does not offer, or no longer has available, is refused; without a location the server goes where its type is available | measured | 2a | `internal/api/cloud`: `TestServerCreateErrors` |

## Primary IPs

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create in a location (no action) or for a server that is off (`assign_primary_ip`); read, update (name, labels, `auto_delete`), delete (204); `assign`, `unassign`, `change_protection` | hcloud-go `hcloud/primary_ip.go`; provider `hcloud_primary_ip` | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle`, `TestPrimaryIPAssignedOnCreate` |
| An unassigned IP has `assignee_id` `null` and `assignee_type` `"unassigned"`; an IPv4's `dns_ptr` lists Hetzner's default name | measured; Hetzner's changelog 2026-04-27 | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle` |
| A server's own IPs are named `primary_ip-<id>`, with `auto_delete` true; `unassign` clears `auto_delete` | measured | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle` |
| Lists filter by `name`, `ip`, `label_selector` and sort by `id`, `created`; `/primary_ips/actions` and `/primary_ips/{id}/actions` | hcloud-go `hcloud/primary_ip.go` (`PrimaryIPListOpts`) | 1b | `internal/api/cloud`: `TestPrimaryIPAssignedOnCreate`, `TestPrimaryIPLists` |
| Errors with Hetzner's codes and texts, details `null` (below); a new server's IP that it cannot take has codes and texts of its own, details `{}` | measured | 1b | `internal/api/cloud`: `TestPrimaryIPLifeCycle`, `TestPrimaryIPCreateErrors`, `TestPrimaryIPsAServerCannotTake` |

## Floating IPs

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create in a `home_location` (no action) or for a server (`assign_floating_ip`, the server's location as home); read, update (name, description, labels), delete (204); `assign`, `unassign`, `change_protection` | hcloud-go `hcloud/floating_ip.go`; provider `hcloud_floating_ip`, `hcloud_floating_ip_assignment` | 2a | `internal/api/cloud`: `TestFloatingIPLifeCycle`, `TestFloatingIPAssignMoveAndUnassign` |
| The JSON: `description` `null` when empty, `server` `null` when unassigned, `blocked` `false`, an IPv4's `dns_ptr` with Hetzner's default name, an IPv6's `[]`; a server's `public_net.floating_ips` lists its IPs | hcloud-go `hcloud/schema/floating_ip.go`; measured | 2a | `internal/api/cloud`: `TestFloatingIPLifeCycle`, `TestFloatingIPAssignMoveAndUnassign` |
| `assign` to any server of the IP's network zone, running or not; on an assigned IP it is a move; the record names the server as soon as the action runs | measured | 2a | `internal/api/cloud`: `TestFloatingIPAssignMoveAndUnassign` |
| Lists filter by `name`, `label_selector` and sort by `id`, `created`; `/floating_ips/actions` and `/floating_ips/{id}/actions` | hcloud-go `hcloud/floating_ip.go` (`FloatingIPListOpts`) | 2a | `internal/api/cloud`: `TestFloatingIPLists` |
| Errors with Hetzner's codes and texts, mostly details `null` (below) | measured | 2a | `internal/api/cloud`: `TestFloatingIPCreateErrors`, `TestFloatingIPLifeCycle`, `TestFloatingIPAssignHoldsOffAnotherAssign`, `TestFloatingIPUpdateChecksLabels` |
| `change_dns_ptr` | hcloud-go `hcloud/floating_ip.go` (`ChangeDNSPtr`) | 2b | open |

## Firewalls

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create with rules and `apply_to` (servers, label selectors), read, update (name, labels), delete (204; 422 while applied to a server directly); `set_rules`, `apply_to_resources`, `remove_from_resources`, each answering a list of actions | hcloud-go `hcloud/firewall.go`; provider `hcloud_firewall`, `hcloud_firewall_attachment` | 2a | `internal/api/cloud`: `TestFirewallLifeCycle`, `TestFirewallAppliedToAServer` |
| The JSON: rules with `port` `null` for icmp, esp and gre, `description` `null`, the other direction's addresses `[]`; `applied_to` with `applied_to_resources` (`[]` too) for a label selector; a server's `public_net.firewalls` with `status` `pending` while an action applies it, `applied` after | hcloud-go `hcloud/schema/firewall.go`, `server.go`; measured | 2a | `internal/api/cloud`: `TestFirewallRulesAreKeptAsHetznerKeepsThem`, `TestFirewallAppliedToAServer` |
| A label selector applies the firewall to matching servers, also new and relabelled ones, each with an `apply_firewall` | measured | 2a | `internal/api/cloud`: `TestFirewallLabelSelectorFollowsTheServers` |
| Limits: 500 effective rules per firewall, 5 firewalls per server (also through a selector) | Hetzner's documentation; measured | 2a | `internal/api/cloud`: `TestFirewallLimits` |
| Lists filter by `name`, `label_selector` and sort by `id`, `name`, `created`; `/firewalls/actions` and `/firewalls/{id}/actions` | hcloud-go `hcloud/firewall.go` (`FirewallListOpts`) | 2a | `internal/api/cloud`: `TestFirewallLifeCycle` |
| Errors with Hetzner's codes and texts (below) | measured | 2a | `internal/api/cloud`: `TestFirewallCreateErrors`, `TestFirewallAppliedToAServer`, `TestFirewallLabelSelectorFollowsTheServers` |
| The rules work on the servers' public addresses: `in` closed but for the rules, `out` open until a rule names it, answers pass, Floating IPs too, also between servers of a project | Hetzner's documentation | 2a | `internal/picture`: `TestFirewallRulesetsMatchGoldenFiles`; live: `TestFirewallsFilterServersFloatingIPsAndTheHost` |
| `firewalls` in a server's create request | hcloud-go `hcloud/server.go` (`ServerCreateOpts.Firewalls`) | 2a (with networks) | open |

## Networks

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Create with subnets and routes (no action), read, update (name, labels, `expose_routes_to_vswitch`), delete (also with servers in it); `add_subnet`, `delete_subnet`, `add_route`, `delete_route`, `change_ip_range`, `change_protection` | hcloud-go `hcloud/network.go`; provider `hcloud_network`, `hcloud_network_subnet`, `hcloud_network_route` | 2a | `internal/api/cloud`: `TestNetworkLifeCycle`, `TestNetworkSubnetsRoutesAndRange` |
| The JSON: subnets in the order of their ranges, each with the network's first address as `gateway` and `vswitch_id` `null`; `servers` and `load_balancers` as lists | hcloud-go `hcloud/schema/network.go`; measured | 2a | `internal/api/cloud`: `TestNetworkLifeCycle` |
| `attach_to_network` (with `ip`, `ip_range`, `alias_ips`), `detach_from_network`, `change_alias_ips`; a server's `private_net` with `network`, `ip`, `alias_ips`, `mac_address` | hcloud-go `hcloud/server.go`; provider `hcloud_server_network` | 2a | `internal/api/cloud`: `TestServerJoinsAndLeavesANetwork`, `TestAttachToNetworkErrors` |
| `GET /networks/{id}/members`: servers and load balancers with `ip`, `alias_ips`, `subnet`, `status`; filters `type`, `status`, `subnet`; sort `id`, `type`, `status`, `ip` | measured; the spec | 2a (load balancers: 4) | `internal/api/cloud`: `TestServerJoinsAndLeavesANetwork`, `TestMembersShowAServerLeavingAsHetznerDoes` |
| Limits: 50 subnets and 100 routes per network, 3 networks per server, 5 alias IPs per network | measured | 2a | `internal/api/cloud`: `TestNetworkCreateErrors`, `TestAttachToNetworkErrors` |
| Errors with Hetzner's codes and texts (below) | measured | 2a | `internal/api/cloud`: `TestNetworkCreateErrors`, `TestNetworkSubnetsRoutesAndRange`, `TestAttachToNetworkErrors`, `TestServerJoinsAndLeavesANetwork` |
| In the guest: the address as a `/32` by DHCP, the gateway on-link and the network's range through it, MTU 1450, no router and no name servers; a NIC attached to a running server works without a reboot | measured in a Hetzner guest | 2a | `internal/agent`: `TestPrivateNetworkDHCPGivesTheAddressAsHetznerDoes`; live: `TestPrivateNetworksConnectServersAndStayApart` |
| Two networks with the same range, also of two projects, stay apart; no Cloud Firewall filters a private network | Hetzner's documentation | 2a | `internal/agent`: `TestPrivateNetworksWithTheSameRangeStayApart`; live: `TestPrivateNetworksConnectServersAndStayApart` |
| `networks` in a server's create request; the metadata's `private-networks` | hcloud-go `hcloud/server.go` (`ServerCreateOpts.Networks`); measured | 2a-4 | open |

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

### Floating IPs and the server's location (2a-1)

Measured on 2026-11-08 with the read-write token of the empty test project
(servers `cx23` in `fsn1` and `nbg1`, `cpx12` in `sin`, never started;
Floating IPs in `fsn1` and `nbg1`). Everything was deleted afterwards.
HLab answers the same, except where noted.

| Request | Answer |
|---|---|
| `POST /servers` with a location the type does not offer (`cx23` in `ash`, by name or ID) or no longer has available (`cpx11` in `fsn1`, `hel1`) | 422 `invalid_input`, `"unsupported location for server type"`, `details` `{}` |
| `POST /floating_ips` without `type` (also `{}`) | 422 `"invalid input in field 'type'"`, `"'type' is required"`; nothing else is checked |
| … with `type` `ipv5` / without `server` and `home_location` / `home_location` `bogus` or `"1"` (an ID) | `"invalid"` / `"invalid input in fields 'server', 'home_location'"`, each `"either server or home_location must be provided"` / `"invalid input in field 'home_location'"`, `"invalid"` |
| … with a name of 300 characters / name `""` / a label key `bad key!` | `"value required to be smaller"` / 201, named `floating_ip-<id>` / 201, the label kept |
| … with an unknown `server` | 404 `not_found`, `"server not found"`, `details` `null` |
| … with a taken name; `PUT` with a taken name | 409 `uniqueness_error`, `"Floating IP name is already used"`, `details` `null` |
| `POST /floating_ips` | 201, `action` `null`, `description` `null` without one, `server` `null`, `blocked` `false`, `dns_ptr` as for Primary IPs (IPv6: `[]`, the IP a `/64`) |
| … with `server` | 201 with `assign_floating_ip`; `home_location` is the server's |
| `GET /floating_ips/{id}` of an IP that is not there | 404 `not_found`, `"Floating IP with ID <id> not found"`, `details` `null` |
| `GET /floating_ips?sort=bogus` / `?ip=<address>` | 200, unsorted / 200, unfiltered (no filter `ip`) |
| `PUT` with `name` `""` / `description` `null` | 200, the name / description stays |
| `PUT` with a label key `bad key!` | 422 `"invalid input in field 'labels'"`, `"invalid label value: key contains invalid characters or is malformed"` |
| `change_protection` (also `{}`) | 201, finished at once |
| `DELETE` protected / assigned (also while its assign runs) | 423 `protected` `"Floating IP deletion is protected"` / 422 `must_be_unassigned` `"IP must be unassigned"`, `details` `null`; once, during an assign to the server it had, 423 `locked` `"cannot perform operation because floating_ip is locked"` (HLab: always that one while an assign runs) |
| `unassign` of an unassigned IP | 422 `service_error`, `"Floating IP with ID <id> is not assigned to any resource"`, `details` `null` |
| `assign` without `server` / with an unknown one / with `"x"` | 422 `"invalid input in field 'server'"`, `"value required to be larger"` / 404 `not_found` `"server not found"`, `details` `null` / **400** `json_error` `"A valid JSON document is required."`, no `details` |
| `assign` to a server in another network zone | 422 `service_error`, `"assigning Floating IP to a Server in another network_zone is not supported"`, `details` `null`; another location of the zone is taken |
| `assign` to a server whose create runs | 423 `locked`, `"resource is locked"` |
| `assign` | 201, `progress` 10, resources the IP and the server; the record names the server at once; the action ends after 3 to 4 s |
| `assign` while an assign runs / `unassign` while an assign runs / `poweroff` of the server while an assign runs | 423 `locked` `"cannot assign Floating IP because it is already being assigned"` / 201, both run / 201: the server is not locked |
| `assign` to the server the IP has | 201, ends at once |
| a move (`assign` of an assigned IP) | Hetzner adds an `unassign_floating_ip` of the old server to the IP's actions (HLab does not) |
| `unassign` | 201, resources the IP and its server; ends after 1 to 3 s |
| `DELETE /servers/{id}` with Floating IPs | they stay, `server` `null` once the server is gone |

### Firewalls and addresses nobody holds (2a-2)

Measured on 2026-11-08 with the read-write token of the empty test project
(two servers `cx23` in `nbg1`, never started; firewalls; one Primary IPv4,
unassigned). Everything was deleted afterwards. HLab answers the same,
except where noted.

| Request | Answer |
|---|---|
| ping and TCP 22, 80, 443 to an unassigned Primary IPv4, from the Internet | no answer at all: ping lost, each connect times out (HLab: the lab's ranges end in a blackhole route) |
| `POST /firewalls` without `name`, with `""` / 129 characters | 422 `"invalid input in field 'name'"`, `"value required to be larger"` / `"value required to be smaller"`; 128 pass |
| … a rule with `direction` `both` | `rules[0].direction`: `"'rules[0].direction' value not allowed"` |
| … `protocol` `sctp` | `"invalid input in field 'rules[].protocol_l4'"`, `"field contains invalid value"` |
| … tcp without `port`, with `""` or `any` / icmp, esp with a port / `80-80` | 201, port `any` / 201, port `null` / 201, port `80` |
| … port `0` / `65536`, `abc`, `" 80"` / `90-80` | `"invalid input in fields 'rules[].port', 'rules[].port'"`, `"value is out of range"` twice / `"invalid input in field 'port'"`, `"invalid port"` / `rules[].port`: `"field value is too small"` |
| … `in` without `source_ips` or with `[]`; `out` without `destination_ips` | `"invalid input in field 'rules'"`, field `rules[0].source_ips`: `"source_ips needs to have at least one entry"` (the same for `destination_ips`) |
| … `in` with `destination_ips`, `out` with `source_ips` | 201, the other direction's list kept as `[]` |
| … `10.0.0.300/32`, `10.0.0.1` / `10.0.0.1/24` | `rules[].source_ips`: `"IP not in cidr notation: <ip>"` / `"IP has host bit(s) set, did you mean 10.0.0.0/24?"` |
| … `2001:0db8:0000::0/32`, `2001:DB8:1::1/128` | kept as `2001:db8::/32`, `2001:db8:1::1/128` |
| … 101 addresses in a rule / a description of 256 characters | `rules[0].source_ips` / `rules[0].description`: `"value required to be smaller"`; 100 pass |
| … 51 rules (on the same two addresses) | 201: no limit of 50 rules |
| … 500 effective rules in / 501 / 300 in and 300 out / 60 rules on the same 10 addresses | 201 / 403 `resource_limit_exceeded`, `"firewall limit exceeded"`, `details.limits` `[{"name": "firewall_effective_rules"}]` / 403 the same / 201 |
| … `rules` `null` / a rule that is a string | 201, rules `[]` / 400 `json_error`, `"A valid JSON document is required."` |
| … a label key `bad key!` | `labels`: `"invalid label value: key contains invalid characters or is malformed"` (also on `PUT`) |
| … `apply_to` a missing server | 404 `firewall_resource_not_found`, `"server with ID <id> not found"`, `details` `null` (also `apply_to_resources`) |
| … `apply_to` `{"server": {"id": 1}}` without `type` / type `volume` | `apply_to[0].server.id` `"value required to be larger"` and `apply_to[0].type` `"'apply_to[0].type' value not allowed"` / only the type's |
| … type `label_selector` without `label_selector` / with `a=b=c,,` | `apply_to[0].label_selector`: `"parameter label_selector needs to be specified if type is label_selector"` / `apply_to[].label_selector`: `"invalid label_selector: invalid syntax"` |
| … a taken name; `PUT` with a taken name | 409 `uniqueness_error`, `"name is already used"`, `details` `null` |
| `POST /firewalls` | 201 with `actions`: `set_firewall_rules` (the firewall, ended), for each `apply_to` entry an `apply_firewall` naming the firewall and the server, or the label selector by an ID of its own |
| `GET /firewalls/{id}` of a firewall that is not there; its actions | 404 `not_found`, `"firewall with ID '<id>' not found"` / `"firewall with ID <id> not found"` (no quotes), `details` `null` |
| `GET /firewalls?sort=bogus` | 200, sorted by ID |
| `PUT` with `name` `""` / with `rules` | 422 `"value required to be larger"` / 200, the rules unchanged |
| `set_rules` without servers / with a server / without `rules` | `set_firewall_rules`, ended / and an `apply_firewall` naming the firewall and the server; the server shows the firewall `pending` for 4 to 5 s / 201, the rules cleared |
| `apply_to_resources` to a server | `apply_firewall` (firewall, server), running, ends after 2.9 to 3.5 s; the server shows the firewall `pending` meanwhile (HLab: as long as the action runs) |
| … to a label selector | `apply_firewall` (firewall, label selector), ends after 0.6 to 1.2 s |
| … twice / without `apply_to` / `[]` | 422 `firewall_already_applied`, `"firewall has already been applied to resource"` / 422 `"'apply_to' is required"` / 201, `actions` `[]` |
| … a sixth firewall to a server (also on create) | 403 `resource_limit_exceeded`, `"firewall limit exceeded"`, `details.limits` `[{"name": "firewalls_per_server"}]` |
| … a label selector that matches a server with five | 201; the server keeps its five, `applied_to_resources` stays `[]` |
| … a server the firewall's label selector holds | `remove_firewall` (firewall, server, label selector) and `apply_firewall` (firewall, server), both ended at once; the server moves to a direct entry |
| a server created with labels a selector matches / relabelled to match | `apply_firewall` (firewall, server) in the firewall's actions; the server shows `[]`, then `pending`, then `applied` after 3 to 9 s (HLab: the action ended, `applied` at once) |
| `remove_from_resources` a server / a label selector | `remove_firewall` (firewall, server), the server shows `pending` for 3 s / `remove_firewall` (firewall, label selector), ends after 0.6 s |
| … a server the firewall reaches through a selector / a missing server / a selector it does not have / without `remove_from` / `[]` | 422 `firewall_managed_by_label_selector`, `"firewall has been applied via label selector."` / 404 `firewall_resource_not_found`, `"resource not found"` / 404 `firewall_resource_not_found`, `"firewall with ID <id> cannot be removed from label_selector: resource not found"` / 422 `"'remove_from' is required"` / 201, `actions` `[]` |
| `DELETE /firewalls/{id}` applied to a server / only to a label selector | 422 `resource_in_use`, `"firewall with ID <id> is still in use"`, `details` `null` / 204 |
| `DELETE /servers/{id}` with firewalls | the server leaves `applied_to` and `applied_to_resources`, without an action |
| `POST /firewalls/{id}/actions/bogus` | 405, no body (HLab: 404 `not_found`, as for any unknown route) |

Not measured: a server that is neither applied directly nor matched, in
`remove_from_resources` (HLab: 404 `firewall_resource_not_found`, with
the selector's text for a server); a server relabelled out of a selector
while nothing else applies the firewall (HLab: it leaves without an
action); the limit of 50 firewalls per account.

### Private networks (2a-3)

Measured on 2026-11-08 with the read-write token of the empty test project:
networks, two servers `cx23` in `nbg1` without public addresses (one
started for two minutes), a load balancer `lb11` in a network; then one
Debian 13 server with a Primary IPv4, read over SSH. Everything was
deleted afterwards. HLab answers the same, except where noted.

| Request | Answer |
|---|---|
| `POST /networks` without `name` or with `""` / 129 characters / `a b` | 422 `"invalid input in field 'name'"`, `"'name' is required"` / `"value required to be smaller"` / 201 |
| … without `ip_range` / `nope`, `10.0.0.0` | `"'ip_range' is required"` / `"must be a valid IP prefix"` |
| … `8.8.0.0/16`, `100.64.0.0/16`, `10.0.0.0/25`, `10.0.0.1/16`, `fd00::/48`, `10.0.0.0/7` | 422 `invalid_input`, `"invalid ip range"`, `details` `null` |
| … `name` `""` and `8.8.0.0/16` | only the name's error |
| … a subnet outside, `/31`, with host bits / a `/30` | `"invalid subnetwork"` / 201 |
| … two subnets that overlap, a route over a subnet, a route twice, two routes that overlap | `"ip ranges of subnetworks or routes overlap"` |
| … a subnet without `network_zone`, `type`, `ip_range` | `subnets[0].network_zone`: `"'subnets[0].network_zone' is required"` (the same for the others) |
| … `type` `foo` / zone `mars` / `vswitch` without `vswitch_id` / with `vswitch_id` 1000 / `cloud` with `vswitch_id` | `"field contains invalid value"` / `"network zone does not exist"` / `subnets[0].vswitch_id`: `"Field must not be empty"` / 422 `service_error`, `"vswitch not found"` / 201, `vswitch_id` `null` |
| … subnets in `us-east` and `eu-central` / of the type `server` | `"subnetwork zones are not aligned"` / 201, the type kept |
| … a route to `8.8.8.0/24`, with host bits / its gateway the network's first address, outside, `172.31.1.1` / to `172.31.1.1/32` | `"invalid route"` / `"invalid gateway"` / `"invalid gateway"` |
| … a route without `gateway` / to `0.0.0.0/0` / inside the range / through a subnet's first address | `routes[0].gateway`: `"'routes[0].gateway' is required"` / 201 / 201 / 201 |
| … labels with the key `bad key!` | `labels`: `"key contains invalid characters or is malformed"` |
| … 51 subnets / 101 routes (50 and 100 pass) | 403 `resource_limit_exceeded`, `"subnet limit reached"` / `"route limit reached"`, `details` `null` |
| … a taken name; the same range as another network of the project | 409 `uniqueness_error`, `"name is already used"`, `details` `null`; 201 |
| `GET /networks/1`; `?sort=ip_range` | 404 `not_found`, `"entity not found"`, `details` `null`; 200, the unknown value ignored |
| `PUT` with `name` `""` / a taken name / `expose_routes_to_vswitch` `true` / `{}` | `"field value is too short"` / 409 as above / 200, kept / 200 |
| `add_subnet` without `ip_range` / the same, an overlapping / outside / without `type` and zone / `vswitch` | the first free `/24` / `"ip range overlaps with existing subnetwork or route"` / `"invalid subnetwork"` / `"invalid input in fields 'network_zone', 'type'"` / `service_error` `"vswitch not found"` |
| `delete_subnet` unknown / without `ip_range` / `nope` / holding a server's address | 422 `service_error`, `"subnet not found"` / `"'ip_range' is required"` / `"cidrv4 violated"` / `service_error`, `"network has attached resources"` |
| `add_route` with a taken destination, over a subnet / a gateway in no subnet | `"ip range overlaps with existing subnetwork or route"` / 201 |
| `delete_route` unknown, with another gateway / without `gateway` | `service_error`, `"route not found"` / `"'gateway' is required"` |
| `change_ip_range` to `10.0.0.0/15` / smaller, the same, elsewhere, public, `/7` / with servers in it | 201 / `"invalid ip range"` / `service_error`, `"network has attached resources"` |
| the network's actions | `resources` the network; subnets and range end at once, routes too without servers and after 4.6 to 6.4 s with; two `add_route` and a `delete_route` sent at once: 201 each, run one after another |
| `DELETE` protected / with subnets and routes / with a server in it | 423 `protected`, `"network is delete protected"` / 204 / 204, the server's `private_net` loses it at once, no action of the server's |
| `attach_to_network` without `network` / an unknown one / without a subnet | `"'network' is required"` / 404 `"entity not found"` / 422 `no_subnet_available`, `"no subnet or IP available"` |
| … `ip` outside the subnets, the network's first address / a subnet's own or last address, taken, another's alias IP | `no_subnet_available` / 422 `ip_not_available`, `"IP not available"` |
| … `ip` `nope` / `ip_range` `nope` / an `alias_ips` entry `nope` | `"must be a valid IP address"` / `"cidrv4 violated"` / `alias_ips[0]`: `"must be a valid IP address"` |
| … `ip_range` that is no subnet / `ip` outside `ip_range` | 422 `service_error`, `"subnet not found"` / `"ip not in ip range"` |
| … an alias IP outside, in no subnet, twice, the gateway / taken, the own `ip` / in another subnet | `no_subnet_available` / `ip_not_available` / 201 |
| … six alias IPs (also when attached already) / attached already / a range that overlaps one of the server's networks / a fourth network | 403 `invalid_input`, `"alias ips limit reached"` / 422 `server_already_attached`, `"server already attached"` / 422 `networks_overlap`, `"network IP range overlaps with one of the resource networks"` / 403 `resource_limit_exceeded`, `"networks per resource limit reached"` |
| … a network whose subnet is in `us-east`, from `nbg1` | 201: the zone is not checked |
| … without `ip` | the first free address of the last subnet (`.1`), or of the `ip_range` given |
| … the action | `attach_to_network`, resources the server and the network; ends after 1.8 to 7.6 s (2.4 s on a running server); listed under `/networks/{id}/actions`, not under `/servers/{id}/actions` |
| `change_alias_ips` with six / without `alias_ips` / a network it is not in, with alias IPs / with `[]` | 422 `invalid_input`, `"alias ips limit reached"` / `"'alias_ips' is required"` / `service_error`, `"resource not found"` / 201, ends at once |
| … twice the same, outside / taken, its own address / an unknown network | `no_subnet_available` / `ip_not_available` / 404 `"entity not found"`; else it ends after 5.8 to 8.1 s |
| `detach_from_network` without `network` / unknown / not attached | `"'network' is required"` / 404 `"entity not found"` / `service_error`, `"resource not found"`; else it ends at once on a running server, after 4.1 to 5.3 s on one that is off |
| `GET /networks/{id}/members` | servers and load balancers (`type` `load_balancer`); `?type=foo`, `?status=foo` / `?subnet=nope`: `"value not allowed"` / field `filters.subnets[0]`, `"must be a valid IP prefix"`; an unknown `sort` is ignored |
| … the statuses | `attaching` while the server's attach runs; after a detach or the server's delete `detaching` for 5.3 s; the other members `updating` from 1.8 to 2.9 s after another server joins or leaves, still after 6 s (HLab: for 4 s) |
| the guest (Hetzner's Debian 13, attached at the create) | `enp7s0`, MTU 1450, `10.0.1.1/32`, routes `10.0.0.1` on-link and `10.0.0.0/16` via `10.0.0.1`; no route of the network's routes; `10.0.0.1` answers ping, an address nobody holds does not |
| … its lease (dhcpcd, started by hc-utils' udev rule) | address `/32`, options 121 and 249 `10.0.0.1/32 0.0.0.0 10.0.0.0/16 10.0.0.1`, MTU 1450, a day; no router, no name servers |
| … its public lease (`eth0`) | a day too (HLab before 2a-3: infinite) |
| … a network attached while it runs | its address in the guest 7.8 s after the attach was sent; after the detach it is gone 4.3 s later |
| … the metadata's `private-networks` | a list of `ip`, `alias_ips`, `interface_num`, `mac_address`, `network_id`, `network_name`, `network`, `subnet`, `gateway` (for 2a-4) |

Deviations, all named in docs/servers.md: the guest's private NICs are
`eth1` to `eth3` under Hyper-V; the seed's vendor-data brings a script
that does what hc-utils does; load balancers come with phase 4. Not
measured: `add_subnet` without a free `/24`, the delete of a network
while an action works on it, a limit of networks per project (2a-4).

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
