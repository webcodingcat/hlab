# Phase 21 context

Confirmed assumptions:
- Console sidebar entry "Storage Boxes" in "Cloud" between Volumes and Firewalls; list with name, username, type, location, usage, status, refreshed every 5 s.
- "Create Storage Box": one-page form with name, location, type (only those offered in the location), password (Hetzner rule errors under the field), SSH keys as checkboxes from the project's keys (no free-text key), access settings, labels.
- Box detail tabs: Overview (hostname and username as copy fields, type, location, usage data/snapshots, folder browser with path field and "up"), Subaccounts, Snapshots, Settings, Labels; rename via actions menu.
- Subaccounts: create, edit (name, description, home directory, access incl. read-only), new password, delete in dialogs; no detail page.
- Snapshots: create (optional description), edit description, delete, roll back with a confirm naming the newer snapshots it deletes.
- Snapshot plan at the top of the Snapshots tab ("No plan" or the plan); dialog with max snapshots, hour and minute (UTC), day of week (every day or Mon-Sun), day of month (every day or 1-31); "Disable plan" confirms first.
- Settings tab: access settings, delete protection, change type (only types offered in the box's location; too-small type shows the API error as toast), reset password (user enters it; console never generates or shows one).
- Delete: confirm dialog with the box name typed (Q23); with delete protection, Delete is disabled with a hint.
- Project list "N resources" menu counts storage boxes and opens the section.
- Administration view "Storage Box Types": catalog and own types with source column and problem banner; add, edit, remove own types (name, description, size, snapshot limit, automatic snapshot limit, subaccounts limit, locations); works while the cloud is stopped.
- Own-type size: number plus unit MiB/GiB/TiB (default GiB); limits default 10, 10, 100.
- Own-type locations: multi-select of the catalog's locations, all preselected through UI-SPEC.md's "All locations" checkbox (checked by default; then every catalog location is sent explicitly), otherwise at least one; the type is offered at box creation and change type only there (user correction).
- Own-type prices: one entry per selected location with all prices 0 in the shape of the Hetzner description, as for own server types; responses for own types pass the check against hetzner.spec.json (user correction).
- Help texts "Storage Boxes" and "Storage Box Types".
- Client tests: one subtest each for storage boxes, subaccounts, snapshots, storage box types, separately for hcloud, OpenTofu, Ansible; each client gets only hlab's Cloud and Hetzner endpoints and the token.
- OpenTofu covers its 3 storage box resources and 7 data sources; Ansible its 7 modules, password reset and rollback through `storage_box`.
- hcloud test covers `hcloud all list` (nothing to the real Hetzner API; closes the backlog item); finds subaccounts by name or ID; expects newer snapshots gone after rollback.
- hcloud test sets a snapshot plan with Sunday through hcloud and reads it back through the hlab API directly: `day_of_week` must be 7 (user correction; hcloud's own JSON prints 0).
- Docs: README keeps `HETZNER_ENDPOINT` / `hetzner-endpoint`, drops "storage boxes (with hcloud only)", adds the console and OpenTofu/Ansible storage box examples and console screenshots.

Technical choices:
- Frontend `hetznerApi`/`runHetzner` on `/mgmt/v1/projects/{name}/api/hetzner/v1`; action toasts poll `/api/v1/actions/{id}`.
- Pure modules `storageBoxes.ts`, `storageBoxSubaccounts.ts`, `storageBoxSnapshots.ts` with unit tests; views in `src/views/storage-boxes/`; routes `storage-boxes` and admin `storage-box-types`; nav icon `Archive`; `projects.ts` maps `storage_boxes`; `views.json`, `help.ts`, two help markdown files.
- Go: `catalog.AddOwnStorageBoxType`/`UpdateOwnStorageBoxType`/`RemoveOwnStorageBoxType` with `OwnStorageBoxTypeSpec` and `FieldError`; explicit location list, empty list refused; bridge `StorageBoxTypes()` and add/update/remove, then `POST /settings/reload`; bridge reads the files directly.
- Unit test checks a written own type against the `storage_box_type` schema of hetzner.spec.json via apitest.
- E2E harness: `HETZNER_ENDPOINT` removed from the inherited environment and set to hlab's in `hcloudEnv`, passed to WSL via `WSLENV`; never `endpoint_hetzner`.
- Client rows CLI-05/06/07 in `test/e2e/clients_storagebox_{hcloud,tofu,ansible}_test.go`; UI rows UI-12, UI-13, UI-14 in `ui_test.go`, UI-12/13 share the box fixture, 15 min limit.

Answers:
- Q23: deleting a storage box requires typing its name (UI-SPEC.md updated).
