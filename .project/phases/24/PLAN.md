# Phase 24: Storage box destroy in one run

## Interfaces

None new. The phase changes only how two handlers of
`internal/resources/storageboxes` start their actions
(`actions.Manager.StartQueued` with `refuse` nil instead of `begin`); no
signature changes, and `internal/actions` stays as it is.

## Plans

- [ ] 24-01 Queued deletes of subaccounts and snapshots
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: CLI-06 | Tests: internal/resources/storageboxes/deletes_test.go, internal/resources/storageboxes/storageboxes_test.go (registration), test/e2e/clients_storagebox_tofu_test.go (CLI-06/destroy)
  - Reads: REQUIREMENTS CLI-06; ARCHITECTURE "hlabd (Windows service)" the storage box paragraph (box lock, `StartQueued`); DECISIONS "2026-11-23 Storage box deletes of subaccounts and snapshots wait instead of answering locked"; phases/24/CONTEXT.md; phases/21/RESEARCH.md the OpenTofu delete lines (provider waits for each delete action, ignores not found)
  - Size: ~120 lines in 4 files
  - Adjusts: none (no existing unit test expects 423 for these deletes)
  - [ ] `subAction` in `internal/resources/storageboxes/subaccounts.go`: for `delete_subaccount` start the action with `acts.StartQueued(tx, p.ID, command, [box, sub], nil)` instead of `begin`; when its turn comes and the box or the subaccount is gone, end with success without `Apply`; other subaccount commands keep `begin`. Update the comments of `begin` in `storageboxes.go` and of `subAction`.
  - [ ] `deleteSnapshot` in `internal/resources/storageboxes/snapshots.go`: start with `StartQueued(..., [box, snap], nil)`; when its turn comes, reload box and snapshot in a transaction, skip the agent job and succeed if either is gone, otherwise run `removeSnapshot` with the reloaded records.
  - [ ] `internal/api/deviations.go`: reword the storage box entry at `change_type` (every action locks the box except deleting a subaccount or a snapshot, which waits as running), and add entries for `DELETE /hetzner/v1/storage_boxes/{id}/subaccounts/{subaccount_id}` and `DELETE /hetzner/v1/storage_boxes/{id}/snapshots/{snapshot_id}` ("a delete while another action on the box runs waits as running instead of answering 423 locked").

- [ ] 24-02 Documentation
  - Wave: 2 | Depends on: 24-01 | Review: alone
  - Requirements: DOC-03, DOC-04, DOC-07 | Tests: test/docs/docs_test.go (existing DOC-03, DOC-04)
  - Reads: SPEC "Documentation" (D1, D4, D6); REQUIREMENTS DOC-01 to DOC-07; phases/24/CONTEXT.md; the plan 24-01 changes of `internal/api/deviations.go`
  - Size: ~40 lines in 3 files
  - Adjusts: none
  - [ ] `docs/guide.md` (OpenTofu storage box example): drop the paragraph that `tofu destroy` can stop with `locked` and must run again; say that one destroy removes the box with its subaccounts and snapshots. `docs/deviations.md`: in "Storage boxes" drop "run it again" and keep that other actions on a busy box answer `locked`; in "Actions that wait instead of answering locked" add deleting subaccounts and snapshots, which wait for the box.
  - [ ] `docs/development/api-coverage.md`: rewrite the storage box note "Every action on a box ... locks the box" so that `DELETE` of a subaccount or snapshot waits as running behind the box's actions while everything else, `DELETE` of the box included, answers 423 `locked`; add the waiting delete to the subaccount and snapshot rows.
