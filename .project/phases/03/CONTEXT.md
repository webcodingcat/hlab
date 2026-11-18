# Phase 03 context

Confirmed assumptions and answers from the start of the phase.

- 03-01 refactor first: `servers` implements `resource.Reconciler` (platform, image store, agent push, settings in `servers.Deps`); `servers.Machines` and `internal/cloud/servers.go` go away; `Reconcile` never changes power state; SRV-01 and SRV-16 run again end-to-end afterwards.
- Power actions: `poweron`, `poweroff` (hard), `shutdown` (graceful via Shutdown IC, action succeeds once sent), `reboot` (graceful), `reset` (hard `Restart-VM`); each locks the server and sets a transitional status.
- A status watcher polls `platform.State` every few seconds while the cloud runs and updates servers without a running action.
- SRV-20 status sequences: create initializing→starting→running; poweron starting→running; poweroff/shutdown stopping→off; rebuild rebuilding→running; change_type migrating→running; delete deleting.
- Reboot: keep `InitiateReboot` if it works on Ubuntu/Debian guests, else hyperv does guest shutdown, wait for off with a deadline, start; interface unchanged; the BACKLOG `later` item is checked off by this phase.
- Rebuild: `protected` while rebuild protection is on; hard off, recreate disk from a system image, rewrite seed ISO with same instance-id/MAC/IP; optional `user_data` (falls back to the previous); starts the server; returns `root_password` without SSH keys.
- change_type: only on a stopped server (`server_not_stopped`); smaller disk, arm or unknown/deprecated type gives `invalid_server_type`; `SetVM` for cores/RAM; `upgrade_disk` resizes the disk, growpart grows it at boot.
- Root password: random in hlabd; only a SHA-512 crypt hash (in-house implementation) leaves hlabd, via vendor-data `chpasswd` or `agentapi.Metadata.PasswordReset`; vendor-data sets `ssh_pwauth: true`.
- reset_password: needs a running server; pushed to the agent, applied by the guest hook `hlab-guest.timer`; confirmation via a new field in `agentapi.Status`; no confirmation in 120 s gives `server_error`.
- META-01: the agent serves 169.254.169.254:80 with Hetzner's paths and full YAML (hand-written), selects the server by source IP and segment, and enforces the IP/MAC binding on that input path.
- SRV-21: sampler reads `platform.Metrics` every ~10 s for running servers, keeps 24 h in memory; `/servers/{id}/metrics` derives cpu, disk and network series; `platform.Sample` gets op and packet counters.
- SRV-18: checked synchronously in the handler (412 `resource_unavailable`) and again in the action, for create, poweron and change_type (rebuild: disk only); more cores than logical CPUs refused.
- Q3 answer (SRV-18 formula): RAM live: type RAM ≤ the host's current free physical RAM minus `reserve_ram_mb`; stopped servers hold nothing. Disk: remaining growth of existing thin disks (nominal minus current file size) plus the new nominal disk ≤ free disk in the data folder minus `reserve_disk_gb`.
- Tests: SRV-18 and SRV-20 as unit tests against a fake platform; e2e rows SRV-02, 07, 08, 09, 10, 21, META-01.
- Documentation: `docs/user/deviations.md` notes reset_password via the guest hook, 24 h in-memory metrics at 10 s, shutdown reports only the sent request, and the reboot fallback if used.
