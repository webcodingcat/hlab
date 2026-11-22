# Phase 18 context

Confirmed by the user at the start of the phase (2026-11-22).

- The Hetzner API is served by the same api.Router and middleware under `/hetzner/v1`; a module picks its prefix through an optional interface in `internal/resource`, `/v1` stays the default; unknown paths there return `not_found`.
- New packages `internal/resources/storageboxtypes` and `internal/resources/storageboxes`, wired in `Cloud.modules()`; `cloud.APIRoutes()` and `docs/development/api-coverage.md` list their routes with the full `/hetzner/v1` prefix.
- Catalog file `catalog/storage-box-types.json` in the real API's shape; missing file means no types; `hlab catalog update` leaves it alone.
- The implementer extends `tools/catalog` to fetch all pages of `https://api.hetzner.com/v1/storage_box_types` (token from `hcloud config get token` in the child process only); the main session runs `task catalog` once with context `HLab` and commits the result; until then tests use a recorded response.
- `api/hetzner-openapi.lock` pins `hetzner.spec.json` (SHA-256 `3afa18f135b62f4c6703a855fe0c5d5f5769f9bac8451a09819acc6a637cb688`); `tools/openapi` fetches and verifies both lock files.
- `internal/apitest` gets a second validator for `hetzner.spec.json`, its server URL rewritten to `/hetzner/v1` in the bytes before loading; the `4xx`/`5xx` rewrite is reused, the `lbAssignee` patch applies to the Cloud description only.
- Boxes and subaccounts are documents in the `resources` table, no schema migration; labels, filters, sort and paging through `api.ListRequest`.
- Create returns status `initializing` with a `create_storage_box` action that does no real work yet; while `initializing`, `username`, `server`, `system` and `snapshot_plan` are null and `stats` is 0; on success the box is `active`. Statuses: `initializing`, `active`, `locked`.
- Username `u<n>` from a kv counter starting at 100000, never reused; subaccounts `u<n>-sub<m>`, `m` from 1 per box, never reused.
- `system` is `<LOC>-BX<n>` (like `FSN1-BX355`); `server` is `u<n>.storagebox.hetzner.internal` (subaccount `u<n>-sub<m>.storagebox.hetzner.internal`), listed in `docs/deviations.md`.
- Password rule: 12 to 128 characters (runes), exactly the allowed set of the description including Ä Ö Ü ä ö ü ß § °, at least one upper, one lower, one digit and one special character; for box and subaccount, on create and both resets; the breach check is not done (deviations.md).
- Only a sha512-crypt hash and an NT hash (new `internal/nthash`, MD4 over UTF-16LE) are kept; the plain password is never stored, logged or returned.
- Every action on one box locks it: a second concurrent action returns 423 `locked`, including subaccount actions; a decision without evidence beyond the subaccount/snapshot case, recorded in DECISIONS.md.
- Error codes from the general table: 409 `uniqueness_error` (box name per project, subaccount name per box), 403 `resource_limit_exceeded` (subaccounts), 423 `protected` (delete), 422 `invalid_input` for every validation error; where Hetzner fails the action instead (e.g. home_directory), hlab answers 422 at once, noted in deviations.md.
- change_type allows a smaller type; phase 19 adds the refusal when data does not fit (SBOX-06).
- enable_snapshot_plan requires hour and minute, checks the ranges (day_of_week 1-7 or null, max_snapshots up to automatic_snapshot_limit) and replaces an existing plan; phase 18 only stores it.
- Subaccount `name` optional, defaults to the username, unique per box; home directory changes only through change_home_directory.
- Subaccount actions reference box and subaccount and appear in the box's action list; all storage box actions are also readable through `/v1/actions`; command names follow Hetzner's operations.
- `folders`, snapshots and `rollback_snapshot` are not registered in phase 18 and answer `not_found`; the management pipe forward waits for phase 21; no IPs, DNS, agent document or storage VM before phase 19.
- DECISIONS.md: "Storage Box API on the same api.Router" replaces the "second router under `/hetzner/v1`" part of "2026-11-21 Storage Boxes in milestone 1".
- Documentation: api-coverage.md, deviations.md, configuration.md, README (Hetzner endpoint setting), build-and-test.md.
