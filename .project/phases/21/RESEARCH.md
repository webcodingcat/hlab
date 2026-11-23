## OpenTofu provider storage boxes

Provider hetznercloud/hcloud: latest stable is v1.70.2 (released 2026-10-26), the same as the pinned version. It is maintained. Sources: https://github.com/hetznercloud/terraform-provider-hcloud/tree/v1.70.2 (docs/ and internal/storagebox, storageboxsubaccount, storageboxsnapshot, storageboxtype).

### Resources (3)

**hcloud_storage_box**
- Required: `name`, `location`, `storage_box_type` (type name), `password` (sensitive).
- Optional: `access_settings` {reachable_externally, samba_enabled, ssh_enabled, webdav_enabled, zfs_enabled; all default false}, `delete_protection` (default false), `labels`, `snapshot_plan` {hour, minute (UTC), max_snapshots required; day_of_month, day_of_week optional, null means every day; day_of_week is 0 (Sunday) to 6, not the API's 1-7}, `ssh_keys` (set, default empty).
- Read-only: `id` (number), `server` (FQDN), `system`, `username`.
- Replacement: `location` and `ssh_keys` force replacement (data loss). Both are RequiresReplace in the schema. `ssh_keys` cannot be changed through the API.
- Import: by numeric ID, or by name. It makes no API call and sets `ssh_keys` to empty. `password` is never read back, so it is unknown after import.
- Create: `StorageBox.Create` (name, type, location, password, labels, plus ssh_keys and access_settings in the same request), then it waits for the action. Then, if set, `ChangeProtection(delete)` and `EnableSnapshotPlan`, each waited. Finally it re-reads the box with GetByID.
- Update, in this order (each runs only if the planned value differs, each waits for its action):
  1. `ChangeProtection(delete)`
  2. snapshot_plan: `DisableSnapshotPlan` if null, else `EnableSnapshotPlan`
  3. `ChangeType` when `storage_box_type` changes (in place)
  4. `ResetPassword` when `password` changes
  5. `UpdateAccessSettings` with all five booleans
  6. `StorageBox.Update` always last (name and labels only if changed; no action wait)
- Delete: if delete_protection is true, `ChangeProtection(false)` first, then `StorageBox.Delete` and wait. Not-found is ignored.
- Read: GetByID only. A nil result removes the resource from state.

**hcloud_storage_box_subaccount**
- Required: `storage_box_id` (number), `home_directory` (no leading slash, created if missing), `password` (sensitive).
- Optional: `name`, `description` (default ""), `labels`, `access_settings` {reachable_externally, readonly, samba_enabled, ssh_enabled, webdav_enabled; all default false}.
- Read-only: `id`, `server`, `username`.
- Replacement: only `storage_box_id`.
- Import: `<box_id>/<subaccount_id>`, no API call.
- Create: `CreateSubaccount` with name, home_directory, password, description, labels, access_settings (retried only on API error `locked`), then it waits for the action and calls GetSubaccountByID.
- Update, in this order, each waited: `ChangeSubaccountHomeDirectory`, `ResetSubaccountPassword`, `UpdateSubaccountAccessSettings`. Then `UpdateSubaccount` always last (name, description, labels only if changed).
- Delete: `DeleteSubaccount`, then it waits. Not-found is ignored.

**hcloud_storage_box_snapshot**
- Required: `storage_box_id`.
- Optional: `description` (default ""), `labels`.
- Read-only: `id`, `name` (assigned by the API; the provider sends no name), `is_automatic`.
- Replacement: only `storage_box_id`.
- Import: `<box_id>/<snapshot_id>`, no API call.
- Create: `CreateSnapshot` with description and labels (retried on `locked`), then it waits and calls GetSnapshotByID.
- Update: `UpdateSnapshot` for description and labels only. It does not wait.
- Delete: `DeleteSnapshot`, then it waits. Not-found is ignored.

### Data sources (7)

All use the `hcloud_` prefix.
- `storage_box`: all optional lookup keys `id`, `name`, `with_selector`. Attributes are those of the resource without password and ssh_keys.
- `storage_boxes`: optional `with_selector`. Returns the list `storage_boxes`, with id, name, username, server, system, location, storage_box_type, delete_protection, labels, access_settings, snapshot_plan.
- `storage_box_type`: optional `id` or `name`. Attributes: description, size (bytes), snapshot_limit, automatic_snapshot_limit, subaccounts_limit, is_deprecated, deprecation_announced, unavailable_after.
- `storage_box_types`: no arguments. Returns the list `storage_box_types` with the same fields plus id and name.
- `storage_box_subaccount`: required `storage_box_id`; optional `id`, `name`, `username`, `with_selector`. Attributes: access_settings, description, home_directory, labels, server.
- `storage_box_subaccounts`: required `storage_box_id`; optional `with_selector`. Returns the list `subaccounts`.
- `storage_box_snapshot`: required `storage_box_id`; optional `id`, `name`, `with_selector`. Attributes: description, is_automatic, labels, stats {size, size_filesystem}.
- `storage_box_snapshots`: required `storage_box_id`; optional `with_selector`. Returns the list `snapshots`.

### Points hlab may lack or must emulate
- The provider uses GetByName for box import and GetByID after every create, so hlab needs get by ID and get by name (name filter) for boxes. Subaccounts and snapshots need only get by ID.
- Locking: the provider retries `locked` on subaccount and snapshot creates, so hlab can return `locked` while a box is busy.
- Boxes need these actions: change_protection, enable_snapshot_plan, disable_snapshot_plan, change_type, reset_password, update_access_settings. Subaccounts need change_home_directory, reset_password, update_access_settings. Create and delete return actions that the provider waits on.
- Only the `delete` protection is used.
- A box update (PUT name and labels) is always sent, even with empty options, so hlab must accept an empty update.
- The data sources expose the type fields listed above, so hlab's type list must carry them.
- Subaccount and snapshot labels are sent on create and on update. Snapshot description is sent on create and on update.

### Unclear
- I did not read the hcloud-go client code that maps these calls to HTTP paths, so exact paths and bodies are unconfirmed. The OpenAPI description referenced in SPEC.md is the authority for them.
- Which endpoint the provider uses for storage boxes (api.hetzner.com/v1 versus api.hetzner.cloud/v1) is not confirmed from the files read.
- The behaviour of `SettleActions` (poll interval, timeout) was not read.
## Ansible collection storage boxes

Sources: hetzner.hcloud 7.2.0 tag (https://github.com/ansible-collections/hetzner.hcloud/tree/7.2.0/plugins/modules), vendored hcloud-python 2.27.0 (`plugins/module_utils/_vendor/hcloud/_version.py` says `2.27.0`; the collection bundles the library, nothing to pip-install), hcloud-python v2.27.0 (https://github.com/hetznercloud/hcloud-python/tree/v2.27.0/hcloud/storage_boxes). Installed version not checked (no manifest lookup done); SPEC.md pins 7.2.0 / 2.27.0.

### Modules (7 files, all exist in 7.2.0)
`storage_box`, `storage_box_info`, `storage_box_type_info`, `storage_box_snapshot`, `storage_box_snapshot_info`, `storage_box_subaccount`, `storage_box_subaccount_info`. All support check mode and take the shared connection options.

### Connection options (doc fragment `hetzner.hcloud.hcloud`)
- `api_token` (env `HCLOUD_TOKEN`, required, no_log).
- `api_endpoint` (alias `endpoint`, env `HCLOUD_ENDPOINT`, default `https://api.hetzner.cloud/v1`): Cloud API only.
- `api_endpoint_hetzner` (env `HETZNER_ENDPOINT`, default `https://api.hetzner.com/v1`): used by ALL storage box calls (the library's `StorageBoxesClient` uses `client._client_hetzner`). For a mock set `HETZNER_ENDPOINT` (or the module option) separately from `HCLOUD_ENDPOINT`.
- The same token is sent to both endpoints (single `api_token`).
- Polling (collection `_base.py`): exponential backoff base 1.0, multiplier 2, cap 5.0 s, `poll_max_retries=25` (about 117 s total); library defaults would be 1 s / 120 retries. user agent application name `ansible-module`.
- Library: failed action raises ActionFailedException, still running after the retries raises ActionTimeoutException; HTTP retries up to 5 times on 502, 504, `rate_limit_exceeded`, `conflict`, `bad_gateway`, `timeout`.

### `storage_box`
States: `present` (default), `absent`, `reset_password`, `rollback_snapshot` (last two never idempotent, always "changed").
Options: `id` or `name` (one required); `storage_box_type` (alias `type`, name or id), `location`, `password` (all required on create; `password` also for reset_password); `labels`; `ssh_keys` (list of OpenSSH public keys, create only); `access_settings` {`reachable_externally`, `samba_enabled`, `ssh_enabled`, `webdav_enabled`, `zfs_enabled`}; `snapshot_plan` {`max_snapshots`, `hour`, `minute` required; `day_of_week` 1-7 or null, `day_of_month` or null}, set null to disable; `delete_protection` (bool); `snapshot` (id or name, for rollback_snapshot).
Behaviour of `present` on an existing box (in order, each only if different): `change_type` (type change), `update_access_settings`, `change_protection`, `enable_snapshot_plan` / `disable_snapshot_plan`, then a final `PUT` for name/labels (rename needs `id`). Password and ssh_keys are never compared on update. On create: create, then protection, then snapshot plan, then reload.
Return: `hcloud_storage_box`.

HTTP (base `https://api.hetzner.com/v1`, path `/storage_boxes`):
- GET `/{id}`; GET `/?name=` (lookup by name)
- POST `/` body `{name, password, location, storage_box_type, ssh_keys?, access_settings?, labels?}` returns storage_box + action
- PUT `/{id}` body `{name?, labels?}`
- DELETE `/{id}` returns action
- POST `/{id}/actions/change_type` `{storage_box_type}`
- POST `/{id}/actions/reset_password` `{password}`
- POST `/{id}/actions/update_access_settings` `{reachable_externally, samba_enabled, ssh_enabled, webdav_enabled, zfs_enabled}` (only keys not None)
- POST `/{id}/actions/change_protection` `{delete}`
- POST `/{id}/actions/enable_snapshot_plan` `{max_snapshots, hour, minute, day_of_week, day_of_month}`
- POST `/{id}/actions/disable_snapshot_plan` (no body)
- POST `/{id}/actions/rollback_snapshot` `{snapshot}` (id or name)
- Also in library (not used by modules): GET `/{id}/folders?path=`, GET `/{id}` actions list.
Every action-returning call is followed by `wait_until_finished()`; `update` (PUT) returns the object, no action. Action poll URL is built from the storage-box base URL (most likely GET `/storage_boxes/actions/{id}`); I could not confirm the exact reload path in the source, check against the OpenAPI description in SPEC.md.

### `storage_box_snapshot`
States `present`/`absent`. Options: `storage_box` (required, id or name; id saves a request), `id`, `name` (API-assigned and immutable), `description`, `labels`.
Pitfall: `present` without `id` or `name` creates a new snapshot on every run. Create then wait then reload; update changes only description/labels (no action); delete waits. No rollback here; rollback is `storage_box` with `state: rollback_snapshot`.
HTTP: POST `/storage_boxes/{id}/snapshots` `{description?, labels?}` (returns snapshot + action); GET `/{id}/snapshots/{sid}`; GET `/{id}/snapshots?name=`; PUT `/{id}/snapshots/{sid}` `{description?, labels?}`; DELETE `/{id}/snapshots/{sid}` (action). Return: `hcloud_storage_box_snapshot`.

### `storage_box_subaccount`
States `present`, `absent`, `reset_password` (not idempotent). Options: `storage_box` (required), `id`, `name`, `password` (create / reset), `home_directory` (create), `access_settings` {`reachable_externally`, `samba_enabled`, `ssh_enabled`, `webdav_enabled`, `readonly`}, `description`, `labels`.
`present` update: `change_home_directory`, `update_access_settings`, then PUT for name/description/labels. Password never changed on update. Name lookup falls back to a legacy label-based name.
HTTP: POST `/storage_boxes/{id}/subaccounts` `{home_directory, password, name?, access_settings?, description?, labels?}`; GET `/{id}/subaccounts/{sid}`; GET `/{id}/subaccounts?name=|username=`; PUT `/{id}/subaccounts/{sid}` `{name?, description?, labels?}`; DELETE `/{id}/subaccounts/{sid}`; POST `.../{sid}/actions/change_home_directory` `{home_directory}`; POST `.../{sid}/actions/reset_subaccount_password` `{password}`; POST `.../{sid}/actions/update_access_settings` (keys as above with `readonly`). Return: `hcloud_storage_box_subaccount`.

### Info modules (read only)
- `storage_box_info`: `id` > `name` > all (`label_selector` ignored if id/name set). Return `hcloud_storage_box_info`: id, name, storage_box_type, location, labels, delete_protection, access_settings, username, server, system, status, stats {size, size_data, size_snapshots}. (Doc example wrongly uses `name: 12345`.)
- `storage_box_snapshot_info`: `storage_box` required; `id` > `name` > all (+`label_selector`). Return `hcloud_storage_box_snapshot_info`: storage_box, id, name, description, labels, stats {size, size_filesystem}, is_automatic, created.
- `storage_box_subaccount_info`: `storage_box` required; `id` > `name` > all (+`label_selector`). Return `hcloud_storage_box_subaccount_info`: storage box id, id, name, description, home_directory, username, server, access settings, labels, created.
- `storage_box_type_info`: `id` / `name` / all. Return `hcloud_storage_box_type_info`: id, name, description, snapshot_limit, automatic_snapshot_limit, subaccounts_limit, size, deprecation {announced, unavailable_after} or None. Endpoint GET `/storage_box_types` on `api.hetzner.com`.

### Unclear
- Exact action poll path for storage box actions (inferred `/storage_boxes/actions/{id}`).
- Maintenance dates of the releases (not checked).
- Installed version in this repo not verified.
