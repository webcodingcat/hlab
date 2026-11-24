# Phase B4 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| SRV-01 | with the test clock and `SeedHold` 90 s: a running server's network attach (`SyncAdapters`) 10 s after its create returns with the private adapter attached, makes no `SetDVD` call on slot 0 and leaves `seed_due` set; once the clock passes create + 90 s, slot 0 is ejected and the new seed (with the network's interface in its network-config) is inserted, and `seed_due` is cleared; a second attach after the hold swaps at once. Fails before the fix (the swap ran at once) | unit | internal/resources/servers/adapters_test.go: TestServers/SRV-01/seed_hold | B4-01 |
| SRV-01 | a server left with `seed_due` set (as an hlabd that ended during the hold or between eject and insert leaves it) gets its current seed inserted into slot 0 at the end of `Restore`, and `seed_due` is cleared (table: VM Saved and resumed: at once; VM Off and powered on by `Restore`: only after the hold; VM Off and staying off: at once) | unit | internal/resources/servers/adapters_test.go: TestServers/SRV-01/seed_due_at_start | B4-01 |
| SBOX-05 | delete_snapshot of a snapshot whose delete-snapshot job the fake agent answers with `not_found` (as after a rollback that failed after the swap) ends with success and the snapshot is no longer listed | unit | internal/resources/storageboxes/snapshots_test.go: TestStorageBoxes/SBOX-05/delete_gone | B4-03 |
| SBOX-17 | a plan run with max_snapshots 1 and three automatic snapshots, the oldest answered `not_found` by the fake agent: both old records are deleted, the next old one through its delete-snapshot job, and the newest stays | unit | internal/resources/storageboxes/snapshots_test.go: TestStorageBoxes/SBOX-17/prune_gone | B4-03 |
| LIFE-04 | in the LIFE fixture, A gets an alias IP on its private network through the API and the user's netplan file puts that alias on A's private link (configured by the seed at boot); after the `save` cycle, B pings the alias IP and A has no `05-hlab-*.network` file for that link | e2e | test/e2e/life_test.go: TestE2E/LIFE-04/netplan_private | B4-02 |

Not mapped:

- CLI-04: the seed fix is proved by the existing `TestE2E/CLI-04/tofu`,
  run five times in a row in B4-06 (before: about every second run failed).
- The private network drops after resume get no row: no hlab change
  (Q27), only a note in docs/deviations.md; the existing DOC tests stay
  green.
- The guest hook's memory of links it took over itself needs no row of
  its own: those links were already in `networks.json` and are skipped as
  before; LIFE-04/netplan_private covers the boot-configured links.

## Restructured tests

Items about the tests (feedback.md "Findings about the tests"). The
proof is that the tests stay green with the same coverage of what the
requirements promise, plus the measurements under "Close" in PLAN.md.

| Requirement | Before | After |
|-------------|--------|-------|
| API-05 | internal/api/list_test.go: TestAPI/API-05 (all paging rules on one list), unchanged | the same |
| API-05 | internal/resources/conventions_test.go: TestResources/API-05: per list endpoint, pages 1 and 2 with `per_page=1` and `meta.pagination` | TestResources/API-05: per list endpoint, page 2 with `per_page=1` (second item, `meta.pagination` page 2, per_page 1, total_entries) |
| API-07 | internal/labels/labels_test.go: TestLabels/API-07 (key and value rules), unchanged | the same |
| API-07 | internal/resources/conventions_test.go: TestResources/API-07: two tables (ssh_keys, servers, placement_groups; zones, rrsets, load_balancers, certificates, storage_boxes, subaccounts); create with labels, PUT replaces, PUT without labels keeps (first table), invalid on create and PUT, refused create stores nothing, refused PUT keeps the labels | TestResources/API-07: one table over the same nine resources; create with labels, PUT replaces, PUT without labels keeps, invalid on create (where checked today) and on PUT answer 422 `invalid_input` on `labels` |
| FAULT-07 | test/e2e/fault_net_test.go: TestE2E/FAULT-07/restart, waiting for three clean bursts of 100 pings before the measurement | the same row and measurement; the wait is 8 s of continuous pings at 10 ms without loss, deadline 2 minutes, failure naming server, peer and last loss |
| FAULT-08 | test/e2e/fault_net_test.go: TestE2E/FAULT-08/restart, same wait as FAULT-07/restart | the same row and measurement, with the new wait |

## Writers

One writer, `test-writer`, for the five rows, in this order:

1. Servers: `internal/resources/servers/adapters_test.go` (SRV-01/seed_hold,
   SRV-01/seed_due_at_start; registered in `testAdapters`; reuses
   `newFakeEnv`, `newTestClock` and `callsOf`); about 180 lines.
2. Storage boxes: `internal/resources/storageboxes/snapshots_test.go`
   (SBOX-05/delete_gone, SBOX-17/prune_gone; registered in
   `TestStorageBoxes` of `storageboxes_test.go`; `fakeAgent.setAnswer`
   returns `&agentclient.JobError{Status: 404, Code: agentapi.JobErrNotFound}`);
   about 90 lines.
3. End to end: `test/e2e/life_test.go` (LIFE-04/netplan_private: alias IP
   and netplan file in the fixture setup, check after the `save` cycle, a
   subtest in the serial LIFE block); about 70 lines.

The restructured tests are changed by the implementers of B4-04 and
B4-05, not by the writer.
