# Phase 24 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| CLI-06 | while a box action is held at its Apply (`holdAction`), `DELETE` of a subaccount of that box answers 201 with a `delete_subaccount` action in status `running`; its Apply starts only after the held action finished, then it succeeds and the subaccount is gone | unit | internal/resources/storageboxes/deletes_test.go: TestStorageBoxes/CLI-06/delete_subaccount_waits | 24-01 |
| CLI-06 | while a box action is held, `DELETE` of a snapshot of that box answers 201 with a `delete_snapshot` action in status `running`; no delete-snapshot job reaches the agent until the held action finished, then it succeeds and the snapshot is gone | unit | internal/resources/storageboxes/deletes_test.go: TestStorageBoxes/CLI-06/delete_snapshot_waits | 24-01 |
| CLI-06 | while a subaccount delete waits behind a held box action, `reset_password` of the box, `change_home_directory` of another subaccount and `DELETE` of the box each answer 423 `locked` (table) | unit | internal/resources/storageboxes/deletes_test.go: TestStorageBoxes/CLI-06/others_locked | 24-01 |
| CLI-06 | a waiting delete whose item is gone at its turn ends with success: a second `DELETE` of the same subaccount, a second `DELETE` of the same snapshot (no second agent job), and a subaccount `DELETE` sent while the box's `delete_storage_box` is held (table) | unit | internal/resources/storageboxes/deletes_test.go: TestStorageBoxes/CLI-06/gone_at_turn | 24-01 |
| CLI-06 | OpenTofu with only `HCLOUD_ENDPOINT` and `HETZNER_ENDPOINT`: one configuration with an `hcloud_storage_box`, an `hcloud_storage_box_subaccount` and an `hcloud_storage_box_snapshot` of it is applied; a single `tofu destroy` exits 0 and box, subaccount and snapshot are gone from the API | e2e | test/e2e/clients_storagebox_tofu_test.go: TestE2E/CLI-06/destroy | 24-01 |

DOC-03 and DOC-04 keep their existing tests in `test/docs/docs_test.go`
(DOC-03 picks up the two new entries of `api.Deviations()`); DOC-07 is
manual.

## Writers

1. Storage boxes (one writer, runs once): `internal/resources/storageboxes/deletes_test.go` (four rows, registered in `TestStorageBoxes` in `storageboxes_test.go`, using `holdAction`, `createBox`, `createSnapshot`, `fakeAgent.jobsOf` and the other helpers of `helpers_test.go` and `snapshots_test.go`), ~200 lines; and the row `CLI-06/destroy` in `test/e2e/clients_storagebox_tofu_test.go` via `registerRow`, using `sharedTofu`, `boxProjectEnv`, `newTofuDir` and `assertHetznerGone`, ~80 lines.
