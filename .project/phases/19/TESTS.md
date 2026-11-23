# Phase 19 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| SBOX-21 | own types of `storage-box-types.custom.json` appear in `/storage_box_types` after the catalog's and can be used for a box | unit | internal/catalog/storageboxtypes_custom_test.go: TestCatalog/SBOX-21/own | 19-02 |
| SBOX-21 | an invalid own file, or one whose ID or name collides with a catalog type, gives a warning and only the catalog types (table: bad JSON, ID collision, name collision) | unit | internal/catalog/storageboxtypes_custom_test.go: TestCatalog/SBOX-21/invalid | 19-02 |
| SBOX-21 | a changed own file takes effect on `/settings/reload`, and its warning shows as problem `custom_storage_box_types` | unit | internal/cloud/reload_test.go: TestCloud/SBOX-21_reload | 19-02 |
| SBOX-08 | the uninstall cleanup deletes the VM named exactly `<tag>-storage` and no other name with that prefix | unit | internal/uninstall/uninstall_test.go: TestUninstall/SBOX-08/storage_vm | 19-03 |
| SBOX-08 | the `storage` folder of the data folder is deleted without `--keep-data` and kept with it | unit | internal/uninstall/uninstall_test.go: TestUninstall/SBOX-08/keep_data | 19-03 |
| SBOX-03 | applying a document never formats a disk that holds a filesystem and never removes a box present in the document; only boxes absent from it are removed (table: first apply, repeat apply, one box removed) | unit | internal/storageagent/plan_test.go: TestStorageAgent/SBOX-03/keep_data | 19-04 |
| SBOX-03 | authorized keys in OpenSSH lines and RFC4716 blocks both log in; other lines are ignored | unit | internal/storageagent/keys_test.go: TestStorageAgent/SBOX-03/keys | 19-07 |
| SBOX-13 | port 23 runs only sftp, scp `-t`/`-f`, rsync through rrsync and `borg serve` restricted to the home (with `--remote-path` borg-1.1/1.2/1.4); other commands, shell metacharacters and paths outside the home are refused; read-only accounts get `rrsync -ro` and no `scp -t` | unit | internal/storageagent/port23_test.go: TestStorageAgent/SBOX-13/commands | 19-07 |
| SBOX-04 | traffic from the host's transit /30 or from outside the public range to a box IP is dropped unless the box or one of its subaccounts is reachable externally; traffic from servers always passes | unit | internal/agent/storagebox_test.go: TestAgent/SBOX-04/external | 19-05 |
| SBOX-09 | a failed `create_storage_box` deletes the box and releases its IP; the action carries the error | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-09/create_failed | 19-08 |
| SBOX-09 | delete succeeds only after Apply succeeded; when Apply fails the box, its subaccounts and its IP remain and the action fails | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-09/delete_confirmed | 19-08 |
| SBOX-02 | subaccount create and the box actions other than delete answer 423 `locked` on a box that is not `active` (one case each: create_subaccount, change_type, folders) | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-02/not_active | 19-08 |
| SBOX-11 | `folders` answers 422 `invalid_input` (field `path`) for an absolute path or one with `..` without asking the agent, and 404 `not_found` for a path the agent reports missing | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-11/path | 19-10 |
| SBOX-06 | change_type is refused with 422 `invalid_input` (field `storage_box_type`) when the live usage exceeds the new type's size, and accepted when it fits | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-06/change_type | 19-10 |
| SBOX-06 | create and change_type to a larger type answer 412 `resource_unavailable` when the host drive has less than 10 GiB free | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-06/free_space | 19-10 |
| SBOX-09 | `stats` of an active box are refreshed from the agent's usage after a 5-minute tick of the test clock | unit | internal/resources/storageboxes/storage_test.go: TestStorageBoxes/SBOX-09/stats | 19-10 |
| SBOX-03 | the first storage box creates the storage VM; on port 22 of `u<n>.storagebox.hetzner.internal`, sftp and scp from the host write and read the box's data with its password and with a key given at creation | e2e | test/e2e/storagebox_test.go: TestE2E/SBOX-03 | 19-11 |
| SBOX-04 | the box is reachable from a server; from host and WSL2 a second box without reachable_externally is not reachable, the shared box with it is | e2e | test/e2e/storagebox_test.go: TestE2E/SBOX-04 | 19-11 |
| SBOX-11 | folders created over SFTP appear in `GET /storage_boxes/{id}/folders`, also below a path | e2e | test/e2e/storagebox_test.go: TestE2E/SBOX-11 | 19-11 |
| SBOX-13 | from WSL2, port 23 serves sftp, scp, rsync and borg (init, create, list) on data written over port 22; with ssh_enabled off port 23 refuses | e2e | test/e2e/storagebox_access_test.go: TestE2E/SBOX-13 | 19-11 |
| SBOX-12 | a subaccount logs in with its own name and password on ports 22 and 23 and sees only its home directory; a read-only subaccount reads but writes nothing over either port | e2e | test/e2e/storagebox_access_test.go: TestE2E/SBOX-12 | 19-11 |
| SBOX-06 | with own types of 64 MiB and 128 MiB: writing over 64 MiB into the small box fails; on the 128 MiB box with more than 64 MiB stored, change_type to the 64 MiB type is refused | e2e | test/e2e/storagebox_access_test.go: TestE2E/SBOX-06 | 19-11 |
| DOC-02 | `docs/configuration.md` names `storage-box-types.custom.json` besides the catalog files (new `storage-debs.json` is found by the existing glob) | unit | test/docs/docs_test.go: TestDocs/DOC-02 | 19-12 |

DOC-01, DOC-03, DOC-04, DOC-05 and DOC-06 keep their existing tests in `test/docs/docs_test.go`; DOC-07 and DOC-08 are manual.

## Writers

1. hlabd side (catalog, cloud reload, uninstall, docs): `internal/catalog/storageboxtypes_custom_test.go` (registered in `catalog_test.go`), `internal/cloud/reload_test.go`, `internal/uninstall/uninstall_test.go`, `test/docs/docs_test.go`; about 250 lines.
2. Agents: `internal/storageagent/plan_test.go`, `keys_test.go`, `port23_test.go` (group `TestStorageAgent`, following the signatures in PLAN.md "Interfaces"), `internal/agent/storagebox_test.go` (registered in `TestAgent` of `metadata_test.go`); about 350 lines.
3. Storage boxes module: `internal/resources/storageboxes/storage_test.go` (registered in `storageboxes_test.go`; fakes for `Apply`, `Job`, `FreeBytes` and the clock in `helpers_test.go`); about 400 lines.
4. End to end: `test/e2e/storagebox_test.go` (shared box fixture with reachable_externally on, host sftp/scp through the askpass pattern of `sshPassword`, release after the group) and `test/e2e/storagebox_access_test.go` (WSL2 askpass for ssh, rsync and borg; own types file in `%ProgramData%\hlab-dev`, `POST /mgmt/v1/settings/reload`, restored afterwards), rows registered as `parallel` in `e2e_test.go`; about 700 lines.
