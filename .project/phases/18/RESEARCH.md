# Phase 18 research: Storage Box API

Checked on 2026-11-22. Frozen description: `hetzner.spec.json` (OpenAPI 3.1.2, info version 1.0.0, server `https://api.hetzner.com/v1`).
No API was called. Go was not runnable in this session, so Q3 is from source reading, not from a run.

## Q1. Behaviour the frozen description leaves open

Summary: the frozen description and the public docs state almost none of this. Error codes for the individual cases are NOT documented. Treat every "unclear" below as something the fake server must make configurable and the real-API tests (hcloud) must observe.

### Documented (confirmed)

- General error table (info.description of the spec, <https://docs.hetzner.cloud/reference/hetzner>): `409 uniqueness_error` ("One or more of the objects fields must be unique"), `409 conflict` ("resource has changed during the request, please retry"), `403 resource_limit_exceeded` ("exceeding the maximum quantity of a resource for an account"), `422 invalid_input`, `422 unsupported_error`, `423 locked` ("there is already an Action running"), `423 protected`, `403 maintenance`, `412 resource_unavailable`, `404 not_found`, `429 rate_limit_exceeded`. Error body: `{"error":{"code","message","details"}}`; `details.fields[].name` / `.messages[]` for `invalid_input`; `details.fields[].name` for `uniqueness_error`; `details.limits[].name` for `resource_limit_exceeded`.
- Box `status` enum: `active`, `initializing`, `locked` (spec; hcloud-go `StorageBoxStatusActive|Initializing|Locked`). While `initializing`: `username`, `server`, `system`, `snapshot_plan` are not available (null) and `stats` is zeros, not null (changelog 2025-10-21). Response fields are always present but may be null.
- Action `status` enum: `running`, `success`, `error`. Action `error` object: `code`, `message` (example `action_failed`).
- Password policy (tag description "Password policy"): 12 to 128 chars; allowed set `a-z A-Z Ä Ö Ü ä ö ü ß 0-9 ^ ° ! § $ % / ( ) = ? + # - . , ; : ~ * @ { } _ &`; at least one upper, one lower, one digit and one special; must not appear in a known breach. Applies to box and subaccount (create and reset). Enforced since 2025-09-17 (changelog 2025-08-04).
- `change_type` description (spec): "Upgrades or downgrades"; downgrade is impossible to a type with less disk space than currently used. So a smaller type IS allowed when usage fits. Hetzner docs: "Rescale ... only move to a smaller plan if your current usage fits" (<https://docs.hetzner.com/storage/storage-box/getting-started/creating-a-storage-box/>). Error code for a too-small target: not documented.
- `server`: nullable string, FQDN, spec example `u1337.your-storagebox.de`. `system`: nullable string, "Host system", spec example `FSN1-BX355` (format: upper-case location code + number, `-BX` + number). `username` example `u12345`. Subaccount domain pattern: `uXXXXX-subX.your-storagebox.de` (tag text). Only spec examples exist; no stricter format is documented.
- Name uniqueness of boxes: "it is not possible to use names more than once per project" (<https://docs.hetzner.com/storage/storage-box/getting-started/creating-a-storage-box/>). Code is not documented; `uniqueness_error` (409) is the general code for this and is the best guess.
- Subaccount `name`: optional on create, "must be unique", defaults to the username, changeable by update; existing subaccounts were migrated to name = username (changelog 2026-01-15). Uniqueness scope (per box or per project) and the error code are not documented; `uniqueness_error` (409) is the general code.
- Snapshot plan: `hour` and `minute` are required (changelog 2025-10-21); `day_of_week` 1..7, `day_of_month` 1..31, null = every day; only one plan per box, enabling replaces the old one.
- Rollback snapshot takes `snapshot` (name or ID); `snapshot_id` deprecated, removal announced for 2026-04-21. Subaccount `home_directory` in "Update Access Settings" deprecated, removal 2026-04-21; use the "Change Home Directory" action (changelog 2025-10-21). `GET .../actions/{id}` per box is deprecated (changelog 2026-04-30).
- Create endpoints for box, subaccount and snapshot return the new object's ID; lists accept `sort`; subaccounts filter by `username`, snapshots by `is_automatic`; partial updates are allowed.
- Pagination: `page` (default 1) and `per_page` (default 25, max 50); `meta.pagination` with `page, per_page, previous_page, next_page, last_page, total_entries` (the last four nullable). The `Link` header is deprecated (announced 2026-10-12).
- Subaccount home directory: only `a-z A-Z 0-9 . / _ -`, no leading slash, nested allowed, created automatically.
- hcloud-go resource types for actions: `storage_box`, `storage_box_snapshot`, `storage_box_subaccount` (<https://raw.githubusercontent.com/hetznercloud/hcloud-go/main/hcloud/action.go>).

### Locking (partly confirmed from provider source)

- Per box, only one snapshot-create or subaccount-create runs at a time. A second concurrent call fails with HTTP 423, code `locked`; the terraform provider retries only that code (PR #1307, <https://github.com/hetznercloud/terraform-provider-hcloud/pull/1307>, code comment: "For a single storage box, only a single snapshot can be created simultaneously, all others fail with `locked` error"; <https://github.com/hetznercloud/terraform-provider-hcloud/pull/1284>).
- Which other actions lock a box (change_type, reset_password, rollback, delete, access settings): not documented. The `locked` box `status` meaning is not documented either.
- A reported real case (issue #1297, <https://github.com/hetznercloud/terraform-provider-hcloud/issues/1297>): parallel subaccount creates gave `locked` (423) and some gave an action with error code `action_failed`, command `create_subaccount`. A leading slash in `home_directory` produced a vague action error `action_failed`, not an `invalid_input`. So some validation failures arrive as a failed action, not as a 422.
- `conflict` (409) is documented only generically ("resource has changed ... retry"); no Storage Box case found.

### Unclear (not found in any primary source)

- How long a box stays `initializing`. The create call returns an action (hcloud CLI v1.70.2 `storage-box create` waits for that action, then reads the box; the terraform provider also only waits for the create action and never polls `status`). Hetzner FAQ says boxes added in the Console are "available immediately" (<https://docs.hetzner.com/storage/storage-box/faq/faq/>, not about API timing). No duration is documented. Recommended: wait for the create action, then re-read the box until `status` is `active`, with a deadline.
- Exact error codes for: weak password (best guess `invalid_input` 422, with `details.fields[].name = "password"`), exceeding `subaccounts_limit` (best guess `resource_limit_exceeded` 403, per the general table), snapshot plan with `max_snapshots` above `automatic_snapshot_limit` (best guess `invalid_input` 422), downgrade below used space (best guess `invalid_input` or `unsupported_error`, or a failed action). All are guesses from the general table, not observed.
- Whether a box is `locked` (status) during actions, and for how long.
- Whether subaccount actions appear in a box's action list: the endpoint says "all Actions related to a specific Storage Box" and the issue #1297 text says a subaccount action carries both the box and the subaccount resource IDs, so they very likely appear (resources `storage_box` plus `storage_box_subaccount`). Not stated in any doc; treat as likely, not confirmed.
- Action `command` names: only `create_subaccount` seen (issue #1297). No list in the spec (`command` is a free string).

## Q2. `GET https://api.hetzner.com/v1/storage_box_types`

- Authentication: the spec has one global `security: APIToken` (http bearer) and no per-operation override; every operation, this one included, needs a bearer token. Tokens are "bound to a Project", created in the Hetzner Console under Security > API Tokens (spec info.description). The same Console/Project token is the Cloud API token. The docs do not state in one sentence that a Cloud project token is accepted at `api.hetzner.com`; evidence is indirect: the Hetzner API reference tells you to create the token in the same Console project; hcloud-go copies the one client token into the Hetzner client and sends it as `Authorization: Bearer` to both endpoints (<https://raw.githubusercontent.com/hetznercloud/hcloud-go/main/hcloud/client.go>); the Ansible module documents `api_token` as the token for the Hetzner Cloud API and uses it for `api.hetzner.com`. The token page (<https://docs.hetzner.com/cloud/api/getting-started/generating-api-token>) mentions only the Cloud API. Status: very likely yes, not confirmed by an explicit statement and not tested against the real API (by instruction).
- Read-only tokens may call GET (token page: Read = GET only), so a read-only token suffices for the types list.
- Paging: query `page` (int, default 1, min 1), `per_page` (default 25, min 1, max 50), `name` (exact filter). Response: `{"storage_box_types":[...], "meta":{"pagination":{page, per_page, previous_page, next_page, last_page, total_entries}}}`; the last four are nullable. hcloud-go uses `per_page=50` and follows pages.
- Type item fields (all required): `id`, `name` (e.g. `bx11`), `description`, `snapshot_limit` (nullable int), `automatic_snapshot_limit` (nullable int), `subaccounts_limit` (int), `size` (bytes, int64), `prices[]` (per `location` name, `price_hourly`, `price_monthly` with `net`/`gross`, and setup fee), `deprecation` (nullable object with `announced`, `unavailable_after`). Prices are in the currency of the project owner's account (tag text). Costs differ between locations.
- Source: spec path `/storage_box_types`, line 11143 of the frozen file; <https://docs.hetzner.cloud/reference/hetzner>.

## Q3. libopenapi-validator v0.16.3 and `/hetzner/v1` on another host

Result: NO, not by itself. Source reading (v0.16.3 `paths/paths.go`, `internal/serverurl/serverurl.go`; not run):

- Only the path part of each server URL is used (`serverurl.BasePath(s.URL)`; the host is never compared). For `https://api.hetzner.com/v1` the base path is `/v1`.
- `StripRequestPath` removes that base path only when the request path starts with `/v1` on a segment boundary (`TrimBasePath`). For `/hetzner/v1/storage_boxes` it does not match, returns the path unchanged, and no template matches, so `FindPath` returns a `ValidationMissing` error "Path ... not found". This is consistent with E8: a different host with the `/v1` prefix works because the host is ignored.
- Fix options (the second is deterministic and needs no model API; the first is the smaller change but unrun):
  1. Override the server URL on the built model: set `model.Model.Servers[0].URL = "http://127.0.0.1:8799/hetzner/v1"` (any host, the path `/hetzner/v1` is what counts) before `validator.NewValidatorFromV3Model(&model.Model)`. `getBasePaths` reads `document.Servers` at call time.
  2. Rewrite the bytes before `libopenapi.NewDocument`: replace `"url": "https://api.hetzner.com/v1"` with `"url": "/hetzner/v1"` (a relative server URL parses to base path `/hetzner/v1`).
  3. Or pass a copy of the request with `URL.Path` trimmed of the `/hetzner` prefix to `ValidateHttpResponse`.
  Because the Cloud description keeps `/v1` and the Hetzner description needs `/hetzner/v1`, use two separate validators (one per description), never one shared document.
- The existing helper `C:\dev\WebCodingCat\hlab\internal\apitest\openapi.go` cannot load `hetzner.spec.json` as is: `DescriptionPath` is fixed to `cloud.spec.json`, and `lbAssignee` returns an error when the primary IP enum is absent (it is absent in the Hetzner file). It would need a second loader without that patch.
- Pitfall that carries over: `hetzner.spec.json` also uses lower-case range keys (`"5xx"` seen at line 1593; the `4xx` key is the same form), which the validator does not match; the existing `upperRangeKeys` rewrite to `4XX`/`5XX` applies to this file too.
- Pitfall: error responses in the description are generic (`code`/`message`/`details`) and the 4XX schema has no enum for `code`, so the validator will not check specific error codes.

## Versions and maintenance

- libopenapi v0.42.5 and libopenapi-validator v0.16.3 are the versions in go.mod; I did not check for newer releases (not asked, and the project pins them).
- hcloud CLI / hcloud-go / terraform-provider-hcloud 1.70.2 are as in the project's E12/E13/E15; the Storage Box API in hcloud-go is marked experimental (`WithHetznerEndpoint`: "breaking changes may occur within minor releases").
- Known provider bug (E13): `endpoint_hetzner` attribute ignored in 1.70.2; use `HETZNER_ENDPOINT`.

## Sources

- <https://docs.hetzner.cloud/reference/hetzner> and the frozen `hetzner.spec.json`
- <https://docs.hetzner.cloud/changelog> (2025-06-25, 2025-08-04, 2025-10-21, 2026-01-15, 2026-04-30, 2026-10-12)
- <https://docs.hetzner.com/storage/storage-box/getting-started/creating-a-storage-box/>, <https://docs.hetzner.com/storage/storage-box/faq/faq/>, <https://docs.hetzner.com/cloud/api/getting-started/generating-api-token>
- hcloud-go `main`: `hcloud/client.go`, `action.go`, `error.go`, `storage_box*.go`, `schema/storage_box.go`
- hcloud CLI v1.70.2 `internal/cmd/storagebox/create.go`
- terraform-provider-hcloud: PR #1307, PR #1284, issue #1297, `internal/storagebox/resource.go`
- libopenapi-validator v0.16.3: `paths/paths.go`, `internal/serverurl/serverurl.go`
