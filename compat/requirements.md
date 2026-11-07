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
| Default images of the tools are older (`ubuntu-24.04`, `debian-12`); the lab has only the newest ones, the smoke tests set `debian-13`, unknown images get Hetzner's error | provider and CLI defaults; `hetzner.hcloud` `tests/integration/targets/*/defaults` | 1b | open |
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
| `RateLimit-*` with a token | `/v1/actions…`: limit 7200; `/v1/ssh_keys…` and the rest: 3600; the two count apart (remaining 7199 and 3599 side by side); `RateLimit-Reset` is when the budget is full again |
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
| `PUT` with `{}` or `"labels": null` | 200, nothing changed |

Still open:

- The messages of HLab's other label selector errors are its own; Hetzner's are known
  only for missing brackets.
- Whether the action endpoints of each resource (`/servers/{id}/actions/…`,
  `/servers/actions`) count against the budget of `/actions` too; HLab counts only
  `/actions` there. To measure when the first of them comes (1b).
- Whether a PUT with several invalid fields names them all ("fields") like a POST;
  HLab names the first, as measured with one.
- The message of a body over 1 MiB; HLab answers `json_error`.
