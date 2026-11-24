# Phase B4: Backlog: seed ISO on early network attach, snapshot rollback leftovers, private network after cloud start, duplicate convention tests

Taken in by the user with /wcc:change on 2026-11-24.

## Items

- [ ] minor: internal/resources/conventions_test.go: TestResources/API-05 and API-07 repeat over every endpoint the paging and label cases that internal/api/list_test.go and internal/labels/labels_test.go already cover in full; about 200 to 300 lines more to read and keep up, take in only when the tests get in the way, e.g. a rework of api.List (the per-endpoint sort rows of API-06 stay: each resource has its own sort table) (source: user after phase 02)
- [ ] bug: internal/resources/servers/adapters.go: a server attached to a network in the first seconds of its boot (as OpenTofu does with a `network {}` block) loses its seed ISO, because `SyncAdapters` calls `rewriteSeed` and takes it out of DVD slot 0 while cloud-init still looks for it; the server keeps hostname `ubuntu`, gets no public IP and is unreachable; `TestE2E/CLI-04/tofu` fails about every second run, also on `main` (source: phase 19 close)
- [ ] bug: internal/storageagent/rollback_linux.go, internal/resources/storageboxes: when a rollback job fails after the data swap (subvolume sync, clear-stale or the accounting check), hlabd keeps the records of the `Newer` snapshots whose subvolumes may be gone; `delete_snapshot` on them then fails for good with the agent's `not_found`, and an automatic one stops `prune` on every plan run, so the user cannot delete them and the plan's retention stops (source: phase 20 verifier)
- [ ] bug: networking after cloud start: after the VMs resume from saved state, private pings between two servers drop in blocks (about 100 pings in a row, later bursts of about 60 %) for up to about 5 minutes, also without the faults applier; users see lost connections right after starting the cloud; cause not found (source: phase 27 27-04 e2e)

Notes (CONTEXT.md, E47, E48):

- Private network after cloud start: E47 found the cause in Hyper-V's
  restore and the guest's `hv_netvsc` driver; no hlab step removes it
  (Q27). The item closes with a note in docs/deviations.md (B4-07). Its
  side finding, the guest hook overriding the user's netplan settings on
  a private link after a carrier drop, is fixed in B4-02, and the restart
  rows get a waiting condition that matches the measured drops (B4-05).
- Seed ISO: Q26 chose the delayed swap: an early attach finishes as fast
  as today, and the seed swap runs by itself once the hold has passed.
- Due seed swaps are done at the end of `Restore`, not in `Recover` as
  CONTEXT.md "Technical choices" puts it: `Recover` runs before the VMs
  are resumed, and a DVD change on a Saved VM is untested (E35 covers only
  Paused VMs). At the end of `Restore` every VM is resumed, powered on or
  off, so the normal swap path with its hold applies.
- An empty slot 0 after a crash between eject and insert is found by the
  `seed_due` note, which is set before the eject; the platform cannot read
  a DVD drive and gets no new method.

## Interfaces

```go
// internal/resources/servers (package servers)

type Deps struct {
	// ...
	// SeedHold is how long after a boot hlab started a seed swap waits,
	// so cloud-init reads the seed it booted with (E48: up to 37 s);
	// 0 = none. internal/cloud sets 90 s.
	SeedHold time.Duration
}

type Internal struct {
	// ...
	// SeedDue is set before the seed in slot 0 is ejected or its swap is
	// put off, and cleared once the new seed is inserted; the end of
	// Restore swaps the seed of every server that still has it.
	SeedDue bool `json:"seed_due,omitempty"`
}
```

Behaviour the tests build on:

- `SyncAdapters` on a server whose last hlab-started boot is less than
  `SeedHold` ago (by `Deps.Clock`) does everything as today except the
  seed: it sets `SeedDue`, makes no `SetDVD` call on slot 0 and returns.
  Once the hold has passed, the module swaps the seed (eject, write,
  insert) from the record as it is then and clears `SeedDue`. After the
  hold `SyncAdapters` swaps at once, as today.
- Boots hlab starts: create, power on, reset, reboot, rebuild, change
  type, the end of rescue, and the power-on of an Off VM in `Restore`. A
  Saved VM started by `Restore` is a resume, not a boot.
- `Restore` ends by swapping the seed of every server with `SeedDue`
  (with the hold for a server it has just booted).

## Plans

- [x] B4-01 Servers: seed swap held after a boot, due swaps at cloud start
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SRV-01 | Tests: internal/resources/servers/adapters_test.go
  - Reads: ARCHITECTURE.md "cidata", "Guest integration", "Cloud start
    and stop"; EXPERIMENTS.md E48; DECISIONS.md "Private adapters of a
    server", "Rescue seed held 90 s after the boot into rescue";
    CONTEXT.md; Interfaces above
  - Size: ~250 lines in 7 files
  - [x] `internal/resources/servers/servers.go` (`Deps.SeedHold`,
    `Internal.SeedDue`, per-server boot times and mutexes in `Module`),
    `internal/resources/servers/adapters.go` (`rewriteSeed`: `SeedDue`
    set before the eject or the deferral; the delayed swap waits on
    `Deps.Clock` until boot + hold in a goroutine bound to the cloud's
    run, then rewrites from the current record under the server's mutex,
    leaving slot 0 alone while rescue holds it; `SeedDue` cleared after
    the insert), `internal/cloud/cloud.go` (`seedHold = 90 * time.Second`
    next to `rescueSeedHold`)
  - [x] Note the boot and hold the server's mutex around the power call
    in every start path: `bootPower` and the start in `rebootFromRescue`
    (`rescue.go`), create (`servers.go`), `rebuild.go`, `changetype.go`;
    `lifecycle.go` `Restore` notes a boot only for a VM that was Off, then
    swaps the seed of every server with `SeedDue`

- [x] B4-02 Guest hook: leave boot-configured links alone after a carrier drop
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: LIFE-04 | Tests: test/e2e/life_test.go (row LIFE-04/netplan_private, run in B4-06)
  - Reads: ARCHITECTURE.md "Guest integration"; EXPERIMENTS.md E47;
    DECISIONS.md "Guest hook for private adapters", "Guest picks up
    private network changes live"; CONTEXT.md
  - Size: ~40 lines in 1 file
  - [x] `internal/resources/servers/vendordata.go`, `sync_networks` of the
    guest hook: a second state file in `/run/hlab-guest` lists the links
    that carried their private IP when the hook first saw them after boot.
    Such a link, and a link the hook took over itself (`networks.json`), is
    never configured again during that boot, even after it lost its
    address. `networks.json` keeps its format; a detached link leaves both
    lists; Python 3.6 compatible like the rest of the hook

- [x] B4-03 Storage boxes: a snapshot the agent no longer has is deleted
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: SBOX-05, SBOX-17 | Tests: internal/resources/storageboxes/snapshots_test.go
  - Reads: DECISIONS.md "Rollback on the storage VM", "Snapshot records
    and automatic snapshots"; CONTEXT.md
  - Size: ~20 lines in 1 file
  - [x] `internal/resources/storageboxes/snapshots.go`, `removeSnapshot`:
    an `*agentclient.JobError` with `Code == agentapi.JobErrNotFound`
    counts as already gone; the record is deleted and the caller
    (delete_snapshot, `prune`) goes on

- [x] B4-04 Convention tests: one wiring case per endpoint for paging and labels
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: API-05, API-07 | Tests: internal/resources/conventions_test.go (restructured, see TESTS.md "Restructured tests")
  - Reads: CONTEXT.md "Assumptions" (convention tests); TESTS.md
    "Restructured tests"
  - Size: ~-150 lines in 1 file
  - [x] Measure before: the line count of `testListPaging` and
    `testLabelsOnResources` and the run time of
    `task test -- ./internal/resources/... -run 'TestResources/API-0[57]'`
    (median of three runs)
  - [x] `internal/resources/conventions_test.go`: `testListPaging` checks
    per endpoint only `per_page=1&page=2` (the second item of the full
    list; `meta.pagination` page 2, per_page 1 and the full list's
    total_entries). `testLabelsOnResources` becomes one table over every
    labeled resource (ssh_keys, servers, placement_groups, zones, rrsets,
    load_balancers, certificates, storage_boxes, subaccounts): create with
    labels, PUT with labels replaces, PUT without labels keeps, invalid
    labels on create (for the resources that check it today) and on PUT
    answer 422 `invalid_input` on `labels`. The checks "refused create
    stores nothing" and "refused PUT keeps the labels" go. Helpers are
    reused
  - [x] Measure after the same way; both values go to "Close"

- [x] B4-05 E2E harness: steady private traffic is 8 s without loss
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: FAULT-07, FAULT-08 | Tests: test/e2e/fault_net_test.go (restart rows, waiting step only)
  - Reads: EXPERIMENTS.md E47; DECISIONS.md "The restart rows wait for
    steady private traffic before measuring"; CONTEXT.md
  - Size: ~40 lines in 1 file
  - [x] `test/e2e/fault_net_test.go`, `waitSteadyPing`: steady means one
    continuous ping run at 10 ms for 8 s (`ping -c 800 -i 0.01 -W 1`)
    with no loss, retried until it passes, with its own deadline of 2
    minutes; on timeout the failure names the server, the peer address and
    the last loss seen. The measurements of the rows stay unchanged

- [x] B4-06 End to end: seed hold, guest hook and restart rows on the dev cloud
  - Wave: 2 | Depends on: B4-01, B4-02, B4-05 | Review: alone
  - Requirements: CLI-04, LIFE-04, FAULT-07, FAULT-08 | Tests: test/e2e/life_test.go, test/e2e/fault_net_test.go, existing TestE2E/CLI-04/tofu
  - Reads: CONTEXT.md; EXPERIMENTS.md E47, E48; CLAUDE.md "Build and test commands"
  - Size: ~0-80 lines in at most 3 files (fixes only where a row fails)
  - [x] `task build`, restart the dev service, then
    `task e2e -- -run 'TestE2E/(LIFE-0[2347]|FAULT-0[78])'`
  - [x] `task e2e -- -run 'TestE2E/CLI-04/tofu'` five times in a row; the
    results go to "Close" (before: about every second run failed, phase 19)
  - [x] Fix in the files of B4-01, B4-02 or B4-05 what a failing row
    shows; rerun only that row

- [x] B4-07 Documentation
  - Wave: 3 | Depends on: B4-06 | Review: alone
  - Requirements: DOC-03, DOC-04, DOC-07 | Tests: existing tests of test/docs (no new rows)
  - Reads: SPEC.md "Documentation"; CONTEXT.md "Assumptions"; EXPERIMENTS.md E47
  - Size: ~10 lines in 1 file
  - [x] `docs/deviations.md` ("Networks, primary and floating IPs"):
    after a cloud start from saved state each server's network drops for
    about 11 to 25 s, caused by Hyper-V and the guest's network driver;
    one sentence that servers created before this release keep their own
    netplan settings on private links through such a drop only after a
    rebuild. Keep the file near 250 lines (tighten a sentence elsewhere
    if needed)

## Close

- B4-04: `testListPaging` + `testLabelsOnResources` 206 -> 164 lines (55+151 -> 53+111), `conventions_test.go` 1303 -> 1261 lines; run time of `task test -- ./internal/resources/... -run 'TestResources/API-0[57]' -count=1` (median of three) 0.99 s -> 1.05 s, mostly environment setup.
- B4-06 on 37c2961: `task e2e -- -run 'TestE2E/(LIFE-0[2347]|FAULT-0[78])'` ok in 829 s; `TestE2E/CLI-04/tofu` five times in a row, all green (162, 174, 163, 160, 164 s; before: about every second run failed). `TestE2E/LIFE-04/netplan_private` failed before the B4-02 fix for the right reason (`05-hlab-*.network` written, alias IP unreachable) and passes after it.
