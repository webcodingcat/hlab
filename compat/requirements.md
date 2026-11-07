# Requirements of the tools

What the tools of [versions.yaml](versions.yaml) need from the API, found in their
source code. Each entry names its source (a file in the tool), the phase that brings
it and the unit test that checks it; "open" means the phase has not come yet.
What never works is in [exceptions.yaml](exceptions.yaml). A new entry:
DEVELOPING.md, Recipes.

## Every endpoint

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Errors as `{"error": {"code", "message", "details"}}` with Hetzner's codes and status codes | hcloud-go `hcloud/schema/error.go`, `hcloud/error.go` | 1a | `internal/web`: `TestErrorsUseHetznersFormat` |
| `X-Correlation-Id` in every answer: hcloud-go adds it to its error texts | hcloud-go `hcloud/error.go` (`Error.Error`) | 1a | `internal/web`: `TestEveryAnswerHasACorrelationID` |
| 401 `unauthorized` without or with an unknown token; 403 `forbidden` for a `read` token on a changing request | hcloud-go `hcloud/error.go` (`ErrorCodeUnauthorized`, `ErrorCodeForbidden`) | 1a | `internal/web`: `TestTokenCheck` |
| Pagination: `page`, `per_page` (default 25, at most 50, invalid values fall back), `meta.pagination` with `null` where there is no page; clients page until `next_page` is empty | hcloud-go `hcloud/client_helper.go` (`iterPages`), `hcloud/schema/meta.go` | 1a | `internal/web`: `TestPaginate` |
| `sort=field[:asc|:desc]`, several in order; contrary orders of one field are ignored | hcloud-go `hcloud/action.go` and the other list options (`Sort`) | 1a | `internal/web`: `TestSort` |
| Label selectors `k=v`, `k==v`, `k!=v`, `k`, `!k`, `k in (…)`, `k notin (…)`, joined by commas | hcloud-go `hcloud/client.go` (`ListOpts.LabelSelector`), provider data sources (`with_selector`) | 1a | `internal/web`: `TestSelector` |
| `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`; 429 `rate_limit_exceeded`; 3600 per hour and project | hcloud-go `hcloud/client_handler_rate_limit.go`, `hcloud/client_handler_retry.go` (`retryPolicy`) | 1a | `internal/web`: `TestRateLimit` |
| Error codes and fields exactly as Hetzner: `locked`, `target_already_defined`, `uniqueness_error`, `invalid_input` with `details.fields`, `not_found`, `conflict` | hcloud-go `hcloud/error.go` (`ErrorCode…`, `IsError`) | 1a (format), each resource's phase | `internal/web`: `TestErrorsUseHetznersFormat`; open per resource |

## Actions

| Requirement | Source | Phase | Test |
|---|---|---|---|
| `GET /actions?id=…` returns the actions of the IDs (listing all actions is gone since 2025-01-30); `GET /actions/{id}` | hcloud-go `hcloud/action.go` (`AllWithOpts`, `GetByID`), `hcloud/action_waiter.go` | 1a | `internal/api/cloud`: `TestActions` |

## Catalog and images

| Requirement | Source | Phase | Test |
|---|---|---|---|
| Hetzner's real IDs in the catalog: types cpx12 = 108, cpx22 = 109, location hel1 = 3, and the other IDs of Hetzner's catalog | `hetzner.hcloud` integration targets (`server_type_info`, `location_info`) | 1a-4 | open |
| Default images of the tools are older (`ubuntu-24.04`, `debian-12`); the lab has only the newest ones, the smoke tests set `debian-13`, unknown images get Hetzner's error | provider and CLI defaults; `hetzner.hcloud` `tests/integration/targets/*/defaults` | 1b | open |
| Load balancer types with all fields; required fields of `update_service` | provider `internal/loadbalancer` | 4 | open |

## Gaps of the old HLab

The dry run of the hcloud CLI against the Python HLab found 58 gaps in fidelity. Each
becomes an entry here, with the phase of its resource, when that phase starts.

## To check against the real API (step 1a-6)

- Whether `details` of an error without details is `null` (the spec's example, which
  HLab follows) or `{}`.
- The messages of `not_found` (HLab: "action not found") and of a missing `id` at
  `GET /actions` (HLab: 422 `invalid_input`).
- The message of 403 `forbidden` (HLab: "insufficient permissions for this request").
- Whether `405 method_not_allowed` comes before or after the token check (HLab: after).
- The `Link` header of lists, which Hetzner announced to remove; HLab leaves it out.
