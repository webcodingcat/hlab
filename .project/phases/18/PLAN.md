# Phase 18: Storage Box API

## Interfaces

```go
// internal/resource: optional interface of a module served under another
// API prefix than /v1. Registrar patterns stay relative to the prefix.
type Prefixed interface {
	Prefix() string // api.HetznerPrefix
}

// internal/api
const HetznerPrefix = "/hetzner/v1" // Prefix ("/v1") stays the default
// Router.Mount registers a Prefixed module's patterns under its prefix; a
// path under neither prefix, or unknown under one, answers not_found. The
// middleware chain (log, auth, JSON check, read-only guard) is the same.

// internal/cloud: APIRoutes reports a Prefixed module's patterns with the
// full prefix, e.g. "GET /hetzner/v1/storage_boxes"; /v1 routes stay
// relative ("GET /servers").

// internal/apitest
const HetznerBaseURL = "http://api.hetzner.internal/hetzner/v1"
// Env.Do / DoAuth: a path starting with api.HetznerPrefix is sent to
// "http://api.hetzner.internal"+path and validated against
// hetzner.spec.json; every other path keeps BaseURL+path and cloud.spec.json.
func HetznerDescriptionPath() (string, error) // <user cache>/hlab/openapi/hetzner.spec.json
// StorageBoxTypes returns the recorded types of
// internal/apitest/testdata/storage-box-types.json (bx11, bx21, bx31 in the
// real API shape, prices for fsn1, nbg1 and hel1; bx11 lacks nbg1).
func StorageBoxTypes(t testing.TB) []catalog.StorageBoxType

// internal/catalog
type StorageBoxType struct {
	ID                     int64
	Name                   string
	Size                   int64
	SnapshotLimit          *int
	AutomaticSnapshotLimit *int
	SubaccountsLimit       int
	Locations              []string        // locations with a price
	Raw                    json.RawMessage `json:"-"` // the object as served
}
// Catalog.StorageBoxTypes []StorageBoxType: from storage-box-types.json
// ({"storage_box_types":[...]}); a missing file gives none.
func ParseStorageBoxTypes(data []byte) ([]StorageBoxType, error)

// internal/nthash
func Hex(password string) string // lower-case hex of MD4 over UTF-16LE

// internal/storagepass
// Check returns nil when pw meets the Storage Box password policy of the
// frozen description; the error text is the details.fields message.
func Check(pw string) error
type Hashes struct {
	Crypt string `json:"crypt"` // sha512-crypt ($6$) by shacrypt.NewSHA512
	NT    string `json:"nt"`    // nthash.Hex
}
func Hash(pw string) (Hashes, error)

// internal/resources/storageboxes
type Deps struct {
	Catalog *catalog.Catalog
	// Apply makes the storage VM match box boxID; it runs in the work
	// function of every box and subaccount action. nil does nothing
	// (phase 18); phase 19 sets it.
	Apply func(ctx context.Context, boxID int64) error
}
func New(d Deps) *Module // Type "storage_boxes", Prefix api.HetznerPrefix
```

Stored documents (`resources` table, no migration):

- kind `storage_boxes`: the box in the API shape plus
  `_hlab: {"n": 100000, "hashes": {"crypt","nt"}, "next_sub": 1}`;
  `ssh_keys` of the create body are kept in `_hlab.ssh_keys`.
- kind `storage_box_subaccounts`: the subaccount in the API shape (with
  `storage_box`, the box ID) plus `_hlab: {"hashes": {"crypt","nt"}}`.
- kv `storage_boxes/next_user`: the next `n`, starting at 100000.
- Action commands: `create_storage_box`, `delete_storage_box`,
  `change_protection`, `change_type`, `reset_password`,
  `update_access_settings`, `enable_snapshot_plan`,
  `disable_snapshot_plan`, `create_subaccount`, `delete_subaccount`,
  `change_home_directory`, `reset_subaccount_password`,
  `update_subaccount_access_settings` (`update_access_settings` of a
  subaccount). `PUT` of a box or subaccount is synchronous, as in the
  description. Resource types `storage_box`, `storage_box_subaccount`; a
  subaccount action references both and locks the box.

## Plans

- [x] 18-01 Hetzner API prefix and its validation
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: API-10, API-14 | Tests: internal/api/router_test.go
  - Reads: ARCHITECTURE.md "api", "resource (one module per resource type)"; SPEC.md "References"; CONTEXT.md items 1, 2, 5, 6; RESEARCH.md "Q3"; EXPERIMENTS.md E8, E12
  - Size: ~260 lines in 7 files
  - Adjusts: -
  - [x] `internal/resource/resource.go`: interface `Prefixed`. `internal/api/router.go`: `HetznerPrefix`; the router keeps one mux per prefix, `underPrefix` accepts both prefixes and strips the matching one, `Mount` picks the mux by `Prefixed`; unknown paths under either prefix answer `not_found`. `internal/cloud/routes.go`: `APIRoutes` records each module's patterns and prefixes those of a `Prefixed` module with its prefix.
  - [x] `internal/apitest/openapi.go`: the loader takes the description file; a second validator for `hetzner.spec.json`, built once, with `"url": "https://api.hetzner.com/v1"` rewritten to `"url": "/hetzner/v1"` in the bytes and `upperRangeKeys` applied (`lbAssignee` only for cloud.spec.json); `ValidateResponse` picks the validator by the request path; `HetznerDescriptionPath`; `RequireDescription` names `task openapi` for either file. `internal/apitest/apitest.go`: `HetznerBaseURL` and the path rule of `Do`/`DoAuth`.
  - [x] New `api/hetzner-openapi.lock` (`https://docs.hetzner.cloud/hetzner.spec.json`, SHA-256 `3afa18f135b62f4c6703a855fe0c5d5f5769f9bac8451a09819acc6a637cb688`); `tools/openapi/main.go` processes both lock files, caching `cloud.spec.json` and `hetzner.spec.json`; the `openapi` task description in `Taskfile.yml` names both.

- [ ] 18-02 Storage box types from the catalog
  - Wave: 2 | Depends on: 18-01 | Review: alone
  - Requirements: SBOX-01 | Tests: internal/catalog/catalog_test.go, internal/resources/storageboxtypes/storageboxtypes_test.go
  - Reads: ARCHITECTURE.md "Files and formats" (storage-box-types.json); SPEC.md "References" (Hetzner API description, path `/storage_box_types`); CONTEXT.md item 3; RESEARCH.md "Q2" (type item fields); `internal/resources/servertypes/servertypes.go` as the pattern
  - Size: ~220 lines in 6 files
  - Adjusts: -
  - [ ] `internal/catalog/catalog.go` (or a new `storageboxtypes.go` in the package): `StorageBoxType`, `ParseStorageBoxTypes`, field `Catalog.StorageBoxTypes` filled by `Load` from `storage-box-types.json` when present (`Locations` from `prices[].location`); `hlab catalog update` does not touch it.
  - [ ] New `internal/resources/storageboxtypes/storageboxtypes.go`: constructor `New(cat *catalog.Catalog)` like `servertypes.New`, `Prefix` `api.HetznerPrefix`, `GET /storage_box_types` (filter `name`, paging, sorted by ID) and `GET /storage_box_types/{id}` serving `Raw`; wired in `Cloud.modules()` (`internal/cloud/cloud.go`). Rows for both routes in `docs/development/api-coverage.md` (section "Storage Boxes", full `/hetzner/v1` routes).
  - [ ] New `internal/apitest/storage.go` with `StorageBoxTypes(t)` and `internal/apitest/testdata/storage-box-types.json`: three types in the shape of the description (bx11 1 TB, bx21 5 TB, bx31 10 TB; `snapshot_limit` 10/20/30, `automatic_snapshot_limit` 10/20/30, `subaccounts_limit` 5/200/300 (bx11 small so SBOX-02/limit_numbering stays fast); prices for fsn1, nbg1, hel1, bx11 without nbg1).

- [!] 18-03 `task catalog` fetches the storage box types - stopped: the permission system denied the edit that reads the hcloud context token (`hcloud --context <name> config get token --allow-sensitive`); no workaround tried, the user must allow or make the edit; partial diff in the scratchpad
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: - (success criterion 2) | Tests: -
  - Reads: SPEC.md "Accounts and secrets" (`task catalog`); CONTEXT.md item 4; RESEARCH.md "Q2"; `tools/catalog/main.go`
  - Size: ~90 lines in 2 files
  - Adjusts: -
  - [ ] `tools/catalog/main.go`: after the Cloud lists, fetch all pages of `https://api.hetzner.com/v1/storage_box_types` (`per_page=50`, follow `meta.pagination.next_page`) with a bearer token: from a usable context through `hcloud --context <name> config get token --allow-sensitive` in the child process only (clearing `HCLOUD_TOKEN`, `HCLOUD_ENDPOINT`, `HCLOUD_CONTEXT`), else the token of the hidden prompt; the context's `hetzner-endpoint` is not read; the token is never printed or written. Write `catalog/storage-box-types.json` as `{"storage_box_types":[...]}`. Package comment and the `catalog` task description in `Taskfile.yml` name the new file. The implementer does not run `task catalog`; the main session does.

- [x] 18-04 Storage Box passwords and their hashes
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-09, SBOX-02 (password policy) | Tests: internal/nthash/nthash_test.go, internal/storagepass/storagepass_test.go
  - Reads: CONTEXT.md items 11, 12; RESEARCH.md "Q1" (password policy); EXPERIMENTS.md E19; `internal/shacrypt/shacrypt.go`
  - Size: ~200 lines in 2 files
  - Adjusts: -
  - [x] New `internal/nthash/nthash.go`: MD4 (RFC 1320) and `Hex` over the UTF-16LE encoding of the password.
  - [x] New `internal/storagepass/storagepass.go`: `Check` (12 to 128 runes; only `a-z A-Z Ä Ö Ü ä ö ü ß 0-9 ^ ° ! § $ % / ( ) = ? + # - . , ; : ~ * @ { } _ &`; at least one upper case letter (A-Z, Ä Ö Ü), one lower case letter (a-z, ä ö ü ß), one digit and one of the other allowed characters; no breach check), `Hashes`, `Hash`.

- [ ] 18-05 Storage boxes: create, list, show, update, delete
  - Wave: 3 | Depends on: 18-02, 18-04 | Review: alone
  - Requirements: SBOX-09 | Tests: internal/resources/storageboxes/storageboxes_test.go, internal/resources/conventions_test.go (API-03, API-04, API-05, API-11)
  - Reads: REQUIREMENTS.md "API core", "Storage Boxes"; ARCHITECTURE.md "resource", "actions", "Data schema"; SPEC.md "References" (paths `/storage_boxes`, `/storage_boxes/{id}`, `/storage_boxes/actions*`); `decisions/storage-box.md`; CONTEXT.md; RESEARCH.md "Q1"; EXPERIMENTS.md E12, E14, E15; `internal/resources/actions/actions.go` (`RoutesFor`), `internal/resources/zones` (the `{id}/{sub}` route)
  - Size: ~420 lines in 6 files
  - Adjusts: test/docs/docs_test.go (alias `GET /hetzner/v1/storage_boxes/{id}/{sub}` serving `GET /hetzner/v1/storage_boxes/actions/{id}`)
  - [ ] New `internal/resources/storageboxes/storageboxes.go` (module, `Deps`, `New`, `Prefix`, `Routes` calling `boxRoutes`, `actionRoutes`, `subaccountRoutes`; a helper that starts a box action locking the box and runs `Apply` in its work function) and `boxes.go`: `POST /storage_boxes` (validates name, `location` among the type's `Locations`, `storage_box_type`, password through `storagepass.Check`, `ssh_keys` in OpenSSH format, `access_settings`, labels; uniqueness per project; status `initializing` with `username`, `server`, `system`, `snapshot_plan` null and `stats` 0; action `create_storage_box`, on success `username` `u<n>` from kv `storage_boxes/next_user` (from 100000, never reused), `server` `u<n>.storagebox.hetzner.internal`, `system` `<LOCATION>-BX<id>`, status `active`), `GET` list (name, label_selector, sort keys of the description, paging) and one, `PUT` (name, labels), `DELETE` (protected while `protection.delete`; action `delete_storage_box`, removes the box and its subaccounts on success); the plain password is never stored, logged or returned. Wire the module in `Cloud.modules()` (`internal/cloud/cloud.go`).
  - [ ] Action endpoints with `actionsres.RoutesFor(r, "storage_boxes", "storage_box")` plus `GET /storage_boxes/{id}/{sub}` serving `/storage_boxes/actions/{id}` (as zones do); empty `actionRoutes` in `actions.go` and `subaccountRoutes` in `subaccounts.go` for 18-06 and 18-07.
  - [ ] `internal/api/deviations.go`: the Storage Box deviations (no data or protocols yet before phase 19, `server` under `storagebox.hetzner.internal`, no breach check of passwords, validation errors as 422 instead of a failed action, every box action locks the box); `docs/development/api-coverage.md`: rows for every Storage Box route of the phase (including those of 18-06 and 18-07), the deviations and the routes not served yet (`folders`, snapshots, `rollback_snapshot`); the docs test alias.

- [ ] 18-06 Storage box actions
  - Wave: 4 | Depends on: 18-05 | Review: alone
  - Requirements: SBOX-10, SBOX-20 | Tests: internal/resources/storageboxes/actions_test.go
  - Reads: REQUIREMENTS.md "Storage Boxes"; SPEC.md "References" (the `/storage_boxes/{id}/actions/*` operations); CONTEXT.md; RESEARCH.md "Q1" (snapshot plan, change_type); EXPERIMENTS.md E14
  - Size: ~300 lines in 1 file
  - Adjusts: -
  - [ ] `internal/resources/storageboxes/actions.go`: `change_protection` (`delete`), `change_type` (type exists and has a price in the box's location; smaller types allowed), `reset_password` (policy, new hashes), `update_access_settings` (`reachable_externally`, `samba_enabled`, `ssh_enabled`, `webdav_enabled`, `zfs_enabled`, absent fields unchanged). Each validates first (422 `invalid_input` with `details.fields`), then runs as a box action through the 18-05 helper and changes the stored box when it succeeds; a box with a running action answers 423 `locked`.
  - [ ] `enable_snapshot_plan` (`max_snapshots` 1 up to the type's `automatic_snapshot_limit`, `minute` 0-59 and `hour` 0-23 required, `day_of_week` 1-7 or null, `day_of_month` 1-31 or null; replaces an existing plan) and `disable_snapshot_plan` (plan null); phase 18 only stores the plan.

- [ ] 18-07 Storage box subaccounts
  - Wave: 4 | Depends on: 18-05 | Review: alone
  - Requirements: SBOX-02 | Tests: internal/resources/storageboxes/subaccounts_test.go, internal/resources/conventions_test.go (API-06, API-07), internal/resources/openapi_test.go (API-10)
  - Reads: REQUIREMENTS.md "Storage Boxes"; SPEC.md "References" (paths `/storage_boxes/{id}/subaccounts*`); CONTEXT.md; RESEARCH.md "Q1" (subaccount name, home directory); EXPERIMENTS.md E14
  - Size: ~380 lines in 1 file (split into `subaccounts.go` and `subaccount_actions.go` if larger)
  - Adjusts: -
  - [ ] `internal/resources/storageboxes/subaccounts.go`: `POST /storage_boxes/{id}/subaccounts` (`home_directory` by the description's pattern and length, without leading slash; password policy; `access_settings` with `readonly`; `name` optional, defaults to the username, unique per box; `description`, labels; 403 `resource_limit_exceeded` with `details.limits` at the type's `subaccounts_limit`; action `create_subaccount` referencing box and subaccount; username `u<n>-sub<m>` with `m` from the box's `next_sub`, never reused; `server` `u<n>-sub<m>.storagebox.hetzner.internal`), list (filters `name`, `username`, `label_selector`, `sort`, no paging as the description has none), show, `PUT` (`name`, `description`, labels), `DELETE` (action `delete_subaccount`).
  - [ ] Subaccount actions `change_home_directory`, `reset_subaccount_password` and `update_access_settings` (the five flags with `readonly`; `home_directory` not accepted there), validated first, run as actions referencing box and subaccount and locking the box.

- [ ] 18-08 Documentation
  - Wave: 5 | Depends on: 18-03, 18-06, 18-07 | Review: alone
  - Requirements: DOC-02, DOC-03, DOC-04, DOC-06, DOC-07 | Tests: test/docs/docs_test.go
  - Reads: SPEC.md "Documentation" (D1, D3, D4, D6), "Accounts and secrets" (`hlab-docs`); `decisions/storage-box.md` "Details"; CONTEXT.md (documentation item); EXPERIMENTS.md E12, E13, E15; README.md, docs/deviations.md, docs/configuration.md, docs/development/api-coverage.md, docs/development/build-and-test.md
  - Size: ~160 lines in 5 files
  - Adjusts: -
  - [ ] `docs/deviations.md`: a "Storage boxes" topic (API only so far: no data and no protocols yet; host names under `storagebox.hetzner.internal`; no breach check of passwords; invalid input refused at once instead of a failed action; every action on a box locks it), without routes. `docs/development/api-coverage.md`: prose of the Storage Box section checked against what is built. `docs/configuration.md`: catalog file `storage-box-types.json`.
  - [ ] `README.md`: the Hetzner endpoint setting of hcloud (`hetzner_endpoint`/`HETZNER_ENDPOINT`), Ansible (`api_endpoint_hetzner`) and OpenTofu (`HETZNER_ENDPOINT`, the attribute is ignored by provider 1.70.2) in the first steps. `docs/development/build-and-test.md`: `task openapi` fetches both descriptions, `task catalog` also writes `storage-box-types.json`. hcloud examples run through `hcloud --context hlab-docs` (its `hetzner-endpoint` is set).
