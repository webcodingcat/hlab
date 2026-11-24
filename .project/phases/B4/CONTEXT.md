# Phase B4: context

Confirmed with the user on 2026-11-24. Research: E47 and E48 in
`research/EXPERIMENTS.md` (no RESEARCH.md).

## Assumptions

- Seed ISO: after a boot hlab starts (create, power on, reset, reboot, rebuild, change type, end of rescue, power on at cloud start) hlab does not swap seed slot 0 until a hold of 90 s has passed since that boot.
- Resume from saved state is not a boot and gets no hold; a due swap runs at once.
- An early network attach works at once (the guest hook configures the link live); only the seed swap waits.
- A boot hlab starts waits until a running seed swap of that server has finished, so it never boots with an empty drive.
- Before emptying the drive or putting a swap off, hlab notes "seed swap due" on the server; the note is cleared only once the new seed is inserted.
- Cloud start inserts the current seed into every server with a due swap and into every server whose slot 0 is empty (crash between eject and insert); a server powered on at that start waits out the hold first.
- A reboot started inside the guest is invisible to hlab and gets no hold.
- Seed proof: unit test with the test clock: an attach within the hold leaves the seed in slot 0, after the hold it is swapped; the test fails before the fix.
- Seed restart proof: unit test: a swap still due when hlabd restarts is done at cloud start, and an empty slot 0 is filled again.
- Seed e2e proof: `TestE2E/CLI-04/tofu` runs 5 times in a row green after the fix, noted under "Close".
- Rollback leftovers: when the storage agent reports `not_found` for a snapshot, deleting it (by hand or by plan retention) succeeds and removes hlab's record.
- A failed rollback still ends its action with an error; snapshots it removed stay listed until deleted, which now works.
- Rollback test: a plan run prunes past a snapshot whose delete returns `not_found` and goes on to delete the next old one.
- Private network after cloud start: no workaround; docs/deviations.md notes that after a cloud start from saved state each server's network drops for about 11 to 25 s, caused by Hyper-V and the guest's network driver; this closes the backlog item.
- Guest hook fix (side finding of E47): the guest hook no longer takes over a private link that the boot configured and that only lost its address during a carrier drop; the user's own netplan settings for that link stay in force.
- The guest hook also remembers the links it took over itself after a hot attach, so a later carrier drop (for example at cloud start) does not make it rewrite them and override a user's own netplan setting for such a link.
- Guest hook test: an e2e row in the existing save-and-start block puts a user netplan setting on a server's private link and checks it still works after the start.
- Servers created before B4 keep the old guest hook until they are rebuilt; docs/deviations.md says so in one sentence.
- Restart rows (FAULT-07/restart, FAULT-08/restart): "steady" means 8 s of continuous pings at 10 ms with no loss (longest observed gap between drop windows about 3 s), with its own 2 minute deadline and a failure message naming server, peer address and the last loss seen.
- Convention tests API-05: each list endpoint keeps one wiring check (`per_page=1&page=2` returns the second item, `meta.pagination` shows page 2); the full paging rules stay in `internal/api/list_test.go`.
- Convention tests API-07: each labeled resource keeps create with labels, update replaces labels, invalid labels on create and on update return `invalid_input` on `labels`, and "update without labels keeps them"; key and value rules stay in `internal/labels/labels_test.go`.
- Dropped as repeats: "a refused create stores nothing" and "a refused update keeps the old labels"; the two label tables become one.
- Test cleanup proof: test lines and run time of `task test -- ./internal/resources/... -run 'TestResources/API-0[57]'` before and after, noted under "Close".

## Technical choices

- Time of the last boot hlab started is kept in memory per server, set in one helper used by every VM start path; `rewriteSeed` checks it through the module's `Clock`.
- `Deps.SeedHold` (default 90 s) is set in `internal/cloud/cloud.go` next to `RescueSeedHold`.
- "Seed swap due" is an optional field `seed_due` in the internal part of the server record (no migration, per ARCHITECTURE.md "Data schema").
- One mutex per server orders seed writes against boots hlab starts; a delayed swap reads the current record when it runs.
- The cloud-start repair runs in the servers module's `Recover` through the existing `RewriteSeed`.
- `removeSnapshot` in `internal/resources/storageboxes/snapshots.go` treats `agentclient.JobError` with `agentapi.JobErrNotFound` as already gone; the agent and its job contract stay unchanged.
- Guest hook: a list in `/run/hlab-guest` of links that carried their private IP when first seen after boot, and of links the hook took over; for those it never writes `05-hlab-<mac>.network` again during that boot (`has_ip` / apply loop in `internal/resources/servers/vendordata.go`).
- Convention tests: only `internal/resources/conventions_test.go` changes, reusing its helpers.

## Answers

- Q26 (Q1 of the planner): an early network attach finishes as fast as today; the seed swap happens by itself once the hold has passed, and a swap still due at an hlabd restart is done at the next cloud start.
- Q27 (Q2 of the planner): no workaround for the network drops after resume; documented in docs/deviations.md.
