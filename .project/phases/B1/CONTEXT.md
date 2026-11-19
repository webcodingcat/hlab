# B1 Context

Confirmed with the user at the start of the phase (2026-11-19).

## Assumptions

- Plans by component (servers password/rebuild, servers vendordata, metrics, start-up recovery, e2e harness), then Documentation; TESTS.md rows only for items a test can show.
- E2E harness: `setup()` stops a running dev cloud before `buildAll`, waits for `stopped`, builds, starts it again and leaves it running; Taskfile unchanged; no new row, proved at the close by `task e2e -- -run <one row>` with the cloud running.
- Stale password reset: `Contribute` serves a pending reset only while its reset_password action is `running`; `rebuild` clears `Hlab.PasswordReset`; no start-up hook; unit tests on SRV-10.
- Metrics: `platform.Platform` gains `MetricsAll(ctx, vms []string) (map[string]Sample, error)`, one PowerShell run per tick for all VMs; `Metrics(vm)` stays, built on it; fake implements both; unit row on SRV-21.
- Rebuild from own snapshot/backup: optional `Hlab.InstanceID` (empty = server ID), every rebuild sets `<server id>-<rebuild action id>`; server and rescue seed use it; metadata `/instance-id` stays the server ID (META-01); unit test plus e2e row on SRV-08.
- Each decision taken without asking goes to DECISIONS.md with its cost of change; the guest-visible instance-id format is medium.
- Documentation: docs/dev/build-and-test.md (e2e stops and restarts a running dev cloud), docs/user/deviations.md (NoCloud instance-id after a rebuild).

## Answers

- Q1 = A (DECISIONS Q6): an optional `resource.Recoverer` removes images and volumes left `creating` with their partial files; cloud start calls it before the API serves and before the first run of the backup job. The ARCHITECTURE line saying Reconcile runs again after a cloud restart is corrected; Reconcile is not called at cloud start. Unit rows on SRV-12 (image), VOL-01 (volume), one cloud test showing the hook is wired.
- user_data: OpenTofu `cloudinit_config` with gzip delivers base64-encoded gzip. `seedUserData` detects base64 -> gzip -> MIME in layers, with one unit case each (plain, base64, gzip, MIME; combinations as the layers chain). The researcher checks whether cloud-init's Hetzner datasource decodes base64 user_data itself; if it does, hlab decodes base64 user_data with and without automount.
- SRV-08 e2e row (rebuild from a backup/snapshot of the same server) reuses the snapshot server of SRV-12 instead of booting its own.
- Research result: cloud-init's Hetzner datasource base64-decodes user_data itself (maybe_b64decode, validate=True, raw fallback); therefore hlab decodes base64 user_data for the NoCloud seed with and without automount (no-volume servers included). The metadata service keeps serving the raw user_data.
