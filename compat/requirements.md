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
| `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`; 429 `rate_limit_exceeded`; 3600 per hour and project | hcloud-go `hcloud/client_handler_rate_limit.go`, `hcloud/client_handler_retry.go` (`retryPolicy`) | 1a | `internal/web`: `TestRateLimit` |
| Error codes and fields exactly as Hetzner: `locked`, `target_already_defined`, `uniqueness_error`, `invalid_input` with `details.fields`, `not_found`, `conflict` | hcloud-go `hcloud/error.go` (`ErrorCode…`, `IsError`) | 1a (format), each resource's phase | `internal/web`: `TestErrorsUseHetznersFormat`; open per resource |

## Actions

| Requirement | Source | Phase | Test |
|---|---|---|---|
| `GET /actions?id=…` returns the actions of the IDs; without IDs it is 410 `deprecated_api_endpoint` (listing all actions is gone since 2025-01-30); `GET /actions/{id}` | hcloud-go `hcloud/action.go` (`AllWithOpts`, `GetByID`), `hcloud/action_waiter.go`; measured | 1a | `internal/api/cloud`: `TestActions`, `TestActionsNeedIDs` |

## Catalog and images

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Hetzner's real IDs in the catalog: types cpx12 = 108, cpx22 = 109, location hel1 = 3, and the other IDs of Hetzner's catalog | `hetzner.hcloud` integration targets (`server_type_info`, `location_info`) | 1a-4 | open |
| `GET /datacenters` is 410 `deprecated_api_endpoint`, `"API functionality was removed"`, with the announcement `https://docs.hetzner.cloud/changelog#2026-06-02-datacenters-deprecated` | measured | 1a-4 | open |
| Default images of the tools are older (`ubuntu-24.04`, `debian-12`); the lab has only the newest ones, the smoke tests set `debian-13`, unknown images get Hetzner's error | provider and CLI defaults; `hetzner.hcloud` `tests/integration/targets/*/defaults` | 1b | open |
| Load balancer types with all fields; required fields of `update_service` | provider `internal/loadbalancer` | 4 | open |

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
| `GET /v1/actions?id=abc` | 422 `invalid_input`, `"invalid integer value"`, `details` `null` |
| `?sort=bogus` | 422 `invalid_input`, `"invalid sort value"`, `details.fields` `[{"name": "sort", "messages": ["value 'bogus' is not allowed"]}]` |
| `?sort=id:asc&sort=id:desc` | 200, the order ignored |
| `?label_selector=env in (a` | 422 `invalid_input`, `"invalid label_selector: invalid syntax: missing brackets for group"`, `details` `{}` |
| `?page=0&per_page=1000` | page 1, `per_page` 50; `Link` header with the rest of the query |
| `?page=99` (one page in all) | page 99, `previous_page` and `next_page` `null`, `last_page` 1 |

Still open:

- `GET /v1/actions/1` with a token (an action of no project of the token) answered
  the plain text `Not Found`, not JSON; its status was not recorded. HLab answers 404
  `not_found` `"action not found"` in JSON until it is.
- The `RateLimit-*` headers of answers with a token were not recorded.
- The messages of HLab's other label selector errors are its own; Hetzner's are known
  only for missing brackets.
