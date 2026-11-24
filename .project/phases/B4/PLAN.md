# Phase B4: Backlog: seed ISO on early network attach, snapshot rollback leftovers, private network after cloud start, duplicate convention tests

Taken in by the user with /wcc:change on 2026-11-24.

## Items

- [ ] minor: internal/resources/conventions_test.go: TestResources/API-05 and API-07 repeat over every endpoint the paging and label cases that internal/api/list_test.go and internal/labels/labels_test.go already cover in full; about 200 to 300 lines more to read and keep up, take in only when the tests get in the way, e.g. a rework of api.List (the per-endpoint sort rows of API-06 stay: each resource has its own sort table) (source: user after phase 02)
- [ ] bug: internal/resources/servers/adapters.go: a server attached to a network in the first seconds of its boot (as OpenTofu does with a `network {}` block) loses its seed ISO, because `SyncAdapters` calls `rewriteSeed` and takes it out of DVD slot 0 while cloud-init still looks for it; the server keeps hostname `ubuntu`, gets no public IP and is unreachable; `TestE2E/CLI-04/tofu` fails about every second run, also on `main` (source: phase 19 close)
- [ ] bug: internal/storageagent/rollback_linux.go, internal/resources/storageboxes: when a rollback job fails after the data swap (subvolume sync, clear-stale or the accounting check), hlabd keeps the records of the `Newer` snapshots whose subvolumes may be gone; `delete_snapshot` on them then fails for good with the agent's `not_found`, and an automatic one stops `prune` on every plan run, so the user cannot delete them and the plan's retention stops (source: phase 20 verifier)
- [ ] bug: networking after cloud start: after the VMs resume from saved state, private pings between two servers drop in blocks (about 100 pings in a row, later bursts of about 60 %) for up to about 5 minutes, also without the faults applier; users see lost connections right after starting the cloud; cause not found (source: phase 27 27-04 e2e)
