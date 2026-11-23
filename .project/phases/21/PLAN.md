# Phase 21: Storage Boxes in the console and the clients

## Interfaces

### Go: own storage box types (`internal/catalog`, new file `storageboxtypes_own.go`)

```go
// OwnStorageBoxTypeSpec describes an own storage box type to add or update.
type OwnStorageBoxTypeSpec struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Size                   int64    `json:"size"`                     // bytes, >= 1
	SnapshotLimit          int      `json:"snapshot_limit"`           // >= 0
	AutomaticSnapshotLimit int      `json:"automatic_snapshot_limit"` // >= 0
	SubaccountsLimit       int      `json:"subaccounts_limit"`        // >= 0
	Locations              []string `json:"locations"`                // catalog location names, at least one
}

// AddOwnStorageBoxType checks s against the served types of c and the own
// types of file, builds the Hetzner storage_box_type object (next free ID
// from MinOwnID, deprecation null, one price entry per location with every
// price 0 in the shape of hetzner.spec.json) and rewrites file in place in
// one write. Field errors are *FieldError with Field "name", "size",
// "snapshot_limit", "automatic_snapshot_limit", "subaccounts_limit" or
// "locations" (empty list: "at least one location is required"; unknown
// name: "unknown location %q").
func AddOwnStorageBoxType(c *Catalog, file string, s OwnStorageBoxTypeSpec) (StorageBoxType, error)

// UpdateOwnStorageBoxType replaces the own type named s.Name, checked as
// by AddOwnStorageBoxType; ID and position stay. The error wraps
// fs.ErrNotExist when file has no own type of that name.
func UpdateOwnStorageBoxType(c *Catalog, file string, s OwnStorageBoxTypeSpec) (StorageBoxType, error)

// RemoveOwnStorageBoxType removes the own type name from file in place;
// the error wraps fs.ErrNotExist when there is none.
func RemoveOwnStorageBoxType(file, name string) error

// OwnStorageBoxTypes returns the entries of file as specs (for the edit
// form), also those not served; a missing file has none.
func OwnStorageBoxTypes(file string) ([]OwnStorageBoxTypeSpec, error)
```

### Go: bridge (`internal/bridge/service.go`)

```go
// ListedStorageBoxType is a storage box type with where it comes from; an
// own type also carries its spec for the edit form.
type ListedStorageBoxType struct {
	Type   json.RawMessage                `json:"type"`   // Hetzner storage_box_type object
	Source string                         `json:"source"` // "catalog" | "own"
	Own    *catalog.OwnStorageBoxTypeSpec `json:"own,omitempty"`
}

// StorageBoxTypeList is the catalog and own types and why own types are
// left out.
type StorageBoxTypeList struct {
	Types   []ListedStorageBoxType `json:"types"`
	Problem string                 `json:"problem,omitempty"` // like custom_storage_box_types
}

func (s *Service) StorageBoxTypes() (StorageBoxTypeList, error)
func (s *Service) AddStorageBoxType(spec catalog.OwnStorageBoxTypeSpec) (*Invalid, error)
func (s *Service) UpdateStorageBoxType(spec catalog.OwnStorageBoxTypeSpec) (*Invalid, error)
func (s *Service) RemoveStorageBoxType(name string) error
```

All three writers call `s.reload()` (`POST /mgmt/v1/settings/reload`, which
does nothing while the cloud is stopped) and read the files directly, so
the view works with the cloud stopped.

### TypeScript: Hetzner API of a project (`ui/frontend/src/lib/api.ts`)

```ts
// hetznerApi sends one request to /mgmt/v1/projects/{name}/api/hetzner/v1<path>.
export function hetznerApi<T>(project: string, method: string, path: string, body?: unknown): Promise<T>;
// hetznerListAll reads every page of a Hetzner API list endpoint (no meta: one request).
export async function hetznerListAll<T>(project: string, path: string, key: string): Promise<T[]>;
// runHetzner runs one Hetzner API request under an action toast; the action
// is polled through the Cloud API's GET /actions/{id}, which also returns
// storage box actions (E12, E15).
export function runHetzner<T extends ActionResult>(project: string, title: string, method: string, path: string, body?: unknown): Promise<T | undefined>;
export function runHetznerOn<T extends ActionResult>(project: string, name: string, action: string,
  req: { method: string; path: string; body?: unknown }): Promise<T | undefined>; // toast "Storage Box <name>: <action>"
```

`lib/bridge.ts` gets `service<T>(type: "Service" | "Cloud" | "Logs", ...)`
unchanged; the Storage Box Types view calls `service("Service",
"StorageBoxTypes")` and the add/update/remove methods.

### TypeScript: routes, navigation, project counts

- `ProjectView` gains `"storage-boxes"` (list, `new`, detail with tabs
  `overview`, `subaccounts`, `snapshots`, `settings`, `labels`);
  `AdminView` gains `"storage-box-types"`.
- `consoleNav.ts`: entry `{ id: "storage-boxes", title: "Storage Boxes",
  tabs: [{ view: "storage-boxes", title: "Storage Boxes" }] }` in "Cloud"
  after Volumes; icon `Archive` in ProjectConsole.
- `projects.ts` kinds: `{ key: "storage_boxes", view: "storage-boxes",
  one: "Storage Box", many: "Storage Boxes" }` after volumes.
- `views.json` and `ViewId` in `help.ts`: `storage-boxes`,
  `storage-box-types`.

### TypeScript: pure action modules (no Wails import, `ResourceAction` of phase 14)

```ts
// lib/resources/storageBoxes.ts; base "storage_boxes"
export const create: ResourceAction;   // POST /storage_boxes {name, location, storage_box_type, password, ssh_keys (public keys), access_settings, labels}
export const menu: ResourceAction[];   // Rename (PUT {name}), Delete (typed-name) last
export const tabs: Record<string, ResourceAction>;
//   change_type {storage_box_type}, reset_password {password} (needsInput),
//   update_access_settings {reachable_externally, samba_enabled, ssh_enabled, webdav_enabled, zfs_enabled},
//   change_protection {delete}, labels (PUT {labels}), delete (typed-name)
export type StorageBox = { id: number; name: string; username: string | null; server: string | null; status: string;
  storage_box_type: BoxType; location: { name: string }; access_settings: Record<string, boolean>;
  snapshot_plan: Record<string, number | null> | null; protection: { delete: boolean };
  stats: { size: number; size_data: number; size_snapshots: number }; labels: Record<string, string> };
export type BoxType = { id: number; name: string; prices: { location: string }[]; deprecation: unknown | null };
// typesIn lists the types with a price entry for location, in catalog order.
export function typesIn(types: BoxType[], location: string): BoxType[];

// lib/resources/storageBoxSubaccounts.ts; input.subaccount is the subaccount ID
export const tabs: Record<string, ResourceAction>;
//   create      POST /storage_boxes/{id}/subaccounts {home_directory, password, name?, description, access_settings}
//   update      PUT  /storage_boxes/{id}/subaccounts/{sub} {name, description}
//   change_home_directory   POST .../subaccounts/{sub}/actions/change_home_directory {home_directory}
//   reset_password          POST .../subaccounts/{sub}/actions/reset_subaccount_password {password}
//   update_access_settings  POST .../subaccounts/{sub}/actions/update_access_settings
//                           {reachable_externally, readonly, samba_enabled, ssh_enabled, webdav_enabled}
//   delete      DELETE .../subaccounts/{sub} (plain)

// lib/resources/storageBoxSnapshots.ts; input.snapshot is the snapshot ID
export const tabs: Record<string, ResourceAction>;
//   create   POST /storage_boxes/{id}/snapshots {description}
//   update   PUT  /storage_boxes/{id}/snapshots/{snap} {description}
//   delete   DELETE .../snapshots/{snap} (plain)
//   rollback POST /storage_boxes/{id}/actions/rollback_snapshot {snapshot: <id>} (plain)
//   enable_plan  POST .../actions/enable_snapshot_plan {max_snapshots, minute, hour, day_of_week, day_of_month}
//   disable_plan POST .../actions/disable_snapshot_plan (plain)
export type PlanInput = { max_snapshots: number; hour: number; minute: number;
  day_of_week: "every" | 1 | 2 | 3 | 4 | 5 | 6 | 7; day_of_month: "every" | number };
// planBody turns the dialog's values into the API body: "every" -> null, Monday 1 .. Sunday 7.
export function planBody(p: PlanInput): { max_snapshots: number; minute: number; hour: number; day_of_week: number | null; day_of_month: number | null };
export type Snap = { id: number; name: string; created: string };
// newerThan lists the snapshots created after target (the ones a rollback deletes), oldest first.
export function newerThan(snapshots: Snap[], target: Snap): Snap[];
```

### Svelte: storage box detail tabs (`ui/frontend/src/views/storage-boxes/`)

`SubaccountsTab.svelte` and `SnapshotsTab.svelte` take props
`{ project: string; box: StorageBox; refresh: () => void }` where
`StorageBox` is exported from `lib/resources/storageBoxes.ts` (the API
object: id, name, username, server, status, storage_box_type, location,
access_settings, snapshot_plan, protection, stats, labels).

### E2E harness

- `hcloudEnv` drops any inherited `HETZNER_ENDPOINT` and sets
  `HETZNER_ENDPOINT=http://api.hetzner.internal/hetzner/v1`;
  `ansibleEnviron` adds `HETZNER_ENDPOINT/u` to `WSLENV`; OpenTofu gets it
  through the same environment, never `endpoint_hetzner` (E13).
- `TestE2E` runs `testClients` for `CLI-05`, `CLI-06`, `CLI-07` as for
  CLI-01..04; `clientOf` names them `hcloud`, `tofu`, `ansible`.
- Shared helpers of the storage box client rows live in
  `test/e2e/clients_storagebox_test.go` (owned by the first test writer
  part): `boxRow(t, e, suffix) (*storageBox)` creating a box through the
  API with cleanup, `boxGet(t, e, id) map[string]any`, and the
  subaccount/snapshot readers through `hetznerDo`.

## Plans

- [x] 21-01 Own storage box types: catalog writers and bridge
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-14 | Tests: internal/catalog/catalog_test.go, internal/resources/storageboxtypes/storageboxtypes_test.go, internal/bridge/service_test.go
  - Reads: CONTEXT.md; Interfaces "Go"; ARCHITECTURE.md "Files and formats" (storage-box-types.custom.json), "hlab-tray"; DECISIONS "Own storage box types follow own server types", "Own storage box types file"; hetzner.spec.json schema `storage_box_type` (prices entry shape); internal/catalog/custom.go (AddOwn/UpdateOwn/RemoveOwn/readOwnRaw/writeOwn), storageboxtypes_custom.go; internal/bridge/service.go
  - Size: ~300 lines in 3 files
  - [x] `internal/catalog/storageboxtypes_own.go`: `OwnStorageBoxTypeSpec`, add, update, remove and `OwnStorageBoxTypes` as in Interfaces, writing in place with the settings.toml ACL kept (reuse `writeOwn`).
  - [x] `internal/bridge/service.go`: `StorageBoxTypes`, `AddStorageBoxType`, `UpdateStorageBoxType`, `RemoveStorageBoxType`; problem from `OwnStorageBoxTypesWarning` of a freshly loaded catalog; field errors as `*Invalid`.
  - [x] Make the test map's rows of this plan pass.

- [x] 21-02 Console foundation for storage boxes
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-12, UI-14 | Tests: ui/frontend/tests/unit/routes.test.ts, ui/frontend/tests/unit/projects.test.ts, ui/frontend/tests/unit/help.test.ts
  - Reads: CONTEXT.md; UI-SPEC.md "Views" (Project console, Storage Box Types); Interfaces "TypeScript: Hetzner API", "routes, navigation, project counts"; ui/frontend/src/lib/api.ts, routes.ts, consoleNav.ts, projects.ts, help.ts, views.json, views/ProjectConsole.svelte; docs/development/components.md "views"
  - Size: ~200 lines in 9 files
  - [x] `lib/api.ts`: `hetznerApi`, `hetznerListAll`, `runHetzner`, `runHetznerOn`; `lib/format.ts` maps the path `storage_boxes` to the type "Storage Box" if `resourceType` needs it.
  - [x] `lib/routes.ts`, `lib/consoleNav.ts`, `views/ProjectConsole.svelte` (icon), `lib/projects.ts`: the new views, entry and count.
  - [x] `views.json`, `help.ts`, `help/storage-boxes.md`, `help/storage-box-types.md` (first version naming the views and buttons of UI-SPEC.md; the documentation plan checks them).

- [x] 21-03 Storage box action modules
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: UI-12, UI-13 | Tests: ui/frontend/tests/unit/storageBoxes.test.ts
  - Reads: Interfaces "pure action modules"; hetzner.spec.json request bodies of `POST /storage_boxes`, the box and subaccount actions, snapshots and `enable_snapshot_plan`; research/EXPERIMENTS.md E14; ui/frontend/src/lib/resourceActions.ts, lib/resources/volumes.ts, tests/unit/resourceTable.ts
  - Size: ~220 lines in 3 files
  - [x] `lib/resources/storageBoxes.ts` with the `StorageBox` and `BoxType` types and `typesIn`.
  - [x] `lib/resources/storageBoxSubaccounts.ts` and `lib/resources/storageBoxSnapshots.ts` with `planBody` and `newerThan`; nested IDs from `input.subaccount` / `input.snapshot`.

- [x] 21-04 E2E harness: Hetzner endpoint for the clients
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CLI-05, CLI-06, CLI-07 | Tests: -
  - Reads: Interfaces "E2E harness"; SPEC.md "Accounts and secrets"; research/EXPERIMENTS.md E12, E13, E15; test/e2e/main_test.go (env setup), clients_test.go (`ansibleEnviron`, tofu `environ`), clients_matrix_test.go (`newProjectEnv`, `clientOf`), e2e_test.go
  - Size: ~40 lines in 4 files
  - [x] `main_test.go` and `clients_matrix_test.go`: drop inherited `HETZNER_ENDPOINT`, set hlab's in `hcloudEnv` (also in `newProjectEnv`); `clientOf` for CLI-05..07.
  - [x] `clients_test.go`: `WSLENV` gets `HETZNER_ENDPOINT/u`; comments of tofu `newTofuDir` name both variables. `e2e_test.go`: the client loop runs CLI-01..CLI-07.

- [x] 21-05 Storage Box Types view
  - Wave: 2 | Depends on: 21-01, 21-02 | Review: alone | e2e
  - Requirements: UI-14 | Tests: test/e2e/ui_test.go, ui/frontend/tests/e2e/storageboxtypes.spec.ts
  - Reads: CONTEXT.md (own-type size, locations, limits); UI-SPEC.md "Storage Box Types", "Server Types", "Building blocks"; Interfaces "Go: bridge"; ui/frontend/src/views/ServerTypes.svelte, App.svelte; ui/frontend/tests/e2e/servertypes.spec.ts
  - Size: ~350 lines in 2 files
  - [x] `views/StorageBoxTypes.svelte`: table (name, description, size, snapshot limit, automatic snapshot limit, subaccounts limit, locations, source), problem banner, "Add storage box type" dialog (size number plus unit MiB/GiB/TiB, default GiB; limits default 10, 10, 100; locations as a checkbox list of the catalog locations "<name> (<city>)", all checked at the start, at least one), own rows with Edit (name read-only) and Remove (confirm).
  - [x] `App.svelte`: admin sidebar item "Storage Box Types" after Server Types and the view switch.

- [x] 21-06 Storage box list and create form
  - Wave: 2 | Depends on: 21-02, 21-03 | Review: alone
  - Requirements: UI-12 | Tests: - (proved by UI-12 and UI-13 end to end)
  - Reads: CONTEXT.md; UI-SPEC.md "Building blocks", "Views" (Storage Boxes, Create), "Behaviour"; Interfaces; ui/frontend/src/views/volumes/Section.svelte, VolumeCreate.svelte, lib/Table.svelte, FormPage.svelte, LabelsField.svelte
  - Size: ~330 lines in 3 files
  - [x] `views/storage-boxes/Section.svelte`: list (name, username, type, location, usage, status), row menu Delete (typed name; disabled with a hint under delete protection), empty state, 5 s refresh; `new` opens the form, detail opens `Detail.svelte` (a stub showing the name until 21-07).
  - [x] `StorageBoxCreate.svelte`: location, type (`typesIn`), name, password (field errors under it), SSH keys as checkboxes of the project's keys (sends their public keys), access settings, labels; Create runs `create` under a toast.

- [ ] 21-07 Storage box detail: Overview, Settings, Labels
  - Wave: 3 | Depends on: 21-06 | Review: alone | e2e
  - Requirements: UI-12 | Tests: test/e2e/ui_test.go, ui/frontend/tests/e2e/storageboxes.spec.ts (UI-12)
  - Reads: CONTEXT.md; UI-SPEC.md "Building blocks" (Detail page, Copy field, Confirm dialog), "Views" (box detail tabs); Interfaces; ui/frontend/src/views/volumes/VolumeDetail.svelte, lib/DetailPage.svelte, LabelsTab.svelte, ProtectionTab.svelte, CopyField.svelte
  - Size: ~380 lines in 4 files
  - [ ] `Detail.svelte`: title with status pill, actions menu (Rename, Delete typed name), tabs Overview, Subaccounts, Snapshots, Settings, Labels (Subaccounts and Snapshots show nothing until 21-10).
  - [ ] `OverviewTab.svelte`: hostname and username as copy fields, type, location, usage (data and snapshots of size), folder browser (path field, folder click descends, "Up").
  - [ ] `SettingsTab.svelte`: access settings, delete protection, change type (`typesIn` of the box's location), reset password dialog (field errors under the field); Labels via `LabelsTab`.

- [x] 21-08 Storage box Subaccounts tab
  - Wave: 2 | Depends on: 21-02, 21-03 | Review: alone
  - Requirements: UI-13 | Tests: - (proved by UI-12 and UI-13 end to end)
  - Reads: CONTEXT.md; UI-SPEC.md "Views" (Subaccounts), "Building blocks"; Interfaces "Svelte: storage box detail tabs"; ui/frontend/src/lib/Table.svelte, Dialog.svelte, ConfirmDialog.svelte
  - Size: ~280 lines in 2 files
  - [x] `SubaccountsTab.svelte`: table (username, name, home directory, description, access), "Create subaccount" dialog (name, description, home directory, password, SSH/Samba/WebDAV/read-only/reachable externally), row menu Edit (name, description, home directory, access; sends only the changed requests), Reset password (dialog), Delete (plain confirm).
  - [x] `SubaccountDialog.svelte` shared by create and edit.

- [x] 21-09 Storage box Snapshots tab and plan
  - Wave: 2 | Depends on: 21-02, 21-03 | Review: alone
  - Requirements: UI-13 | Tests: - (proved by UI-12 and UI-13 end to end)
  - Reads: CONTEXT.md; UI-SPEC.md "Views" (Snapshots); Interfaces; research/EXPERIMENTS.md E14; ui/frontend/src/lib/Table.svelte, Dialog.svelte, ConfirmDialog.svelte
  - Size: ~300 lines in 2 files
  - [x] `SnapshotsTab.svelte`: plan summary ("No plan" or the plan in UTC) with "Set plan" and "Disable plan" (confirm); table (name, description, size, automatic, created), "Create snapshot" (optional description), row menu Edit description, Roll back (confirm naming `newerThan`), Delete (plain).
  - [x] `SnapshotPlanDialog.svelte`: max snapshots, hour, minute (UTC), day of week (Every day, Monday..Sunday), day of month (Every day, 1..31), body through `planBody`.

- [ ] 21-10 Subaccounts and snapshots in the box detail
  - Wave: 4 | Depends on: 21-07, 21-08, 21-09 | Review: alone | e2e
  - Requirements: UI-13 | Tests: test/e2e/ui_test.go, ui/frontend/tests/e2e/storageboxes.spec.ts (UI-13)
  - Reads: Interfaces "Svelte: storage box detail tabs"; ui/frontend/src/views/storage-boxes/Detail.svelte
  - Size: ~30 lines in 1 file
  - [ ] `Detail.svelte` renders `SubaccountsTab` and `SnapshotsTab` with the box and a refresh.
  - [ ] Make TestE2E/UI-13 pass.

- [x] 21-11 Storage boxes with hcloud
  - Wave: 2 | Depends on: 21-04 | Review: alone | e2e
  - Requirements: CLI-05 | Tests: test/e2e/clients_storagebox_hcloud_test.go, test/e2e/clients_storagebox_test.go
  - Reads: CONTEXT.md; research/EXPERIMENTS.md E12, E14, E20; `hcloud storage-box --help` and the subcommands' help; internal/resources/storageboxes (only for a fix)
  - Size: ~40 lines in up to 3 files (no product change expected by E20)
  - [x] Run the CLI-05 rows; fix in hlab what they find (a fix of a public API shape stops the plan).
  - Found by the first run of the new tests (2026-11-23): a box created with an SSH public key that ends in a newline (as OpenTofu's `file()` or Ansible's lookup give it) is accepted by the API, but the storage agent refuses the whole document (`invalid document: box u100100: SSH key spans lines`), so every later storage push fails and every create of any box errors. Fix in hlab: handle such a key at the API the way Hetzner does, and make sure one bad box cannot block the pushes of all others.

- [x] 21-12 Storage boxes with OpenTofu
  - Wave: 3 | Depends on: 21-11 | Review: alone | e2e
  - Requirements: CLI-06 | Tests: test/e2e/clients_storagebox_tofu_test.go
  - Reads: RESEARCH.md "OpenTofu provider storage boxes"; research/EXPERIMENTS.md E13; internal/resources/storageboxes (boxes.go update, subaccounts.go, snapshots.go)
  - Size: ~100 lines in up to 4 files
  - [x] Run the CLI-06 rows; fix in hlab what they find, for example an empty `PUT /storage_boxes/{id}` or `locked` on subaccount and snapshot create while a box action runs.

- [x] 21-13 Storage boxes with Ansible
  - Wave: 4 | Depends on: 21-12 | Review: alone | e2e
  - Requirements: CLI-07 | Tests: test/e2e/clients_storagebox_ansible_test.go
  - Reads: RESEARCH.md "Ansible collection storage boxes"; research/EXPERIMENTS.md E15; internal/resources/storageboxes
  - Size: ~100 lines in up to 4 files
  - [x] Run the CLI-07 rows; fix in hlab what they find (`?name=` and `?username=` filters on subaccounts and snapshots, `GET /storage_boxes/actions/{id}` polling).

- [ ] 21-14 Documentation
  - Wave: 5 | Depends on: 21-05, 21-10, 21-13 | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06, DOC-07 | Tests: test/docs/docs_test.go
  - Reads: SPEC.md "Documentation", "Accounts and secrets" (hlab-docs); REQUIREMENTS.md "Documentation"; CONTEXT.md; README.md "First steps", "The tray program"; docs/guide.md "Storage boxes", "OpenTofu and Ansible"; docs/configuration.md own storage box types; docs/development/components.md; ui/frontend/tests/screenshots/console.spec.ts; BACKLOG.md item on `hcloud all list`
  - Size: ~300 lines in 8 files
  - [ ] README: keep `HETZNER_ENDPOINT` and hcloud `hetzner-endpoint`, name `api_endpoint_hetzner` for Ansible and the env variable for OpenTofu, drop "storage boxes (with hcloud only)", mention the console's Storage Boxes and Storage Box Types; console screenshots of the Storage Boxes section in light and dark (screenshot spec).
  - [ ] docs/guide.md: storage boxes in the window (list, create, detail tabs, subaccounts, snapshots and plan, rollback), OpenTofu and Ansible storage box examples (run against `hlab-docs`), own types through the Administration; docs/configuration.md: the tray writes `storage-box-types.custom.json`; components.md: the new views and bridge methods.
  - [ ] Help texts `storage-boxes.md` and `storage-box-types.md` match the built views and buttons.
