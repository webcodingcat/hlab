# Phase 09 context

Confirmed by the user at the start of the phase. The user's answers are
recorded in DECISIONS.md (phase 09 entries of 2026-11-19). Where RESEARCH.md
shows otherwise, the research applies.

## Assumptions (confirmed)

- Servers implement a new optional module interface in `internal/resource`: `Halt(ctx, behaviour)` runs at stop after the API and the jobs and before the appliance; `Restore(ctx)` runs at start after the first `PushAgent` and before the management API and the jobs; `internal/cloud` only calls them.
- The backlog item about servers `Reconcile` is not needed: VMs stay in Hyper-V across a stop.
- "Which servers ran" is the server record's status; `Halt` sets `off` for servers whose VM is already off and keeps `running` for running ones; start resumes or powers on every server with status `running` or `starting` whose VM is Saved or Off; `off` stays off; no new field.
- "save": `Halt` saves running VMs one after another; a failed save falls back to guest shutdown, then hard off, logged.
- "shutdown": `Halt` asks every running guest at once, waits 60 s in total, then turns off what still runs; VMs starting, stopping or paused are left as they are.
- A server that fails to resume or power on at start is logged; the cloud start continues.
- The appliance shuts down at stop and boots afresh at start, as today.
- Stop re-reads `stop_behaviour` from settings.toml; an invalid file keeps the value read at start.
- At start every server VM that is Off or Saved gets `AutomaticStopAction` for the current setting through a new Platform method `SetStopAction`.
- At a Windows shutdown (`StopHostShutdown`) `Halt` does nothing to the VMs; the status watch writes nothing once the shutdown has begun (flag set first in the preshutdown handler, RESEARCH.md topic 2); if that is not enough, a documented deviation.
- LIFE-07 is built (`hostnet.Up`/`Down`, `publishZones`); the phase adds the e2e proof; `WatchWSL` stays a stub.
- HOST-02: every transit and service address is derived from `public_range` (host .2/30, appliance .1, API listen and proxy, agent address and DNS listen, appliance network-config, NRPT target); the agent gets its addresses from its cidata seed.
- HOST-02: accepted is an IPv4 prefix /8 to /28, normalised to its network address; refused are overlaps with 0/8, 127/8, 169.254/16, 224/4 and up, 172.31.1.1, 185.12.64.1-2, naming the setting.
- HOST-02: the existing fatal `range_collision` stays; its overlap logic moves into a pure function.
- LIFE-08: `store.Open` returns `*store.IncompatibleError{Found, Want}` for a `user_version` other than 0 or 1 and for a file that is not a SQLite database (SQLITE_NOTADB/CORRUPT by code, RESEARCH.md topic 3); hlabd refuses with status `refused`, problem `state_incompatible`, naming both versions and `hlab cloud start --discard-state`.
- LIFE-08: `hlab cloud start --discard-state` passes the start parameter `discard-state`; the flag is the confirmation; hlabd discards only when the state is incompatible, otherwise ignores and logs it.
- LIFE-08: a discard renames `state.db`, `-wal`, `-shm` to `state.db.discarded-<UTC time>`, deletes every server VM of the profile by name prefix (new Platform method `VMs(ctx, prefix)`), the `disks\`, `volumes\`, `vms\` folders and `images\image-*.vhdx`; image and ISO cache, appliance and CA stay.
- LIFE-08: `hlabd uninstall-cleanup --vms-only` is left to phase 12.
- LIFE-09: own rotating writer in `internal/logfile`, no new dependency; `hlabd.log` rotates at 10 MB to `.1` to `.5`.
- LIFE-09: `GET /mgmt/v1/logs[?follow=1]` returns the last 1000 lines as NDJSON, with `follow` streams across rotations until the client disconnects.
- LIFE-09: `hlab logs [--follow]` uses the endpoint while the cloud runs and reads the files while it is stopped; human output `time LEVEL msg key=value...`, `--output json` passes the raw lines.
- Q14: `loadbalancers.IPv6` uses `0xA0000000|id`; an idempotent loadbalancers `Recoverer` at start moves `dns_ptr` entries from the old /64 to the new one with the same interface ID; IDs not enforced below 2^29; LB-01, LB-09 tests and deviations.md follow.
- e2e: LIFE-02, LIFE-03, LIFE-04, LIFE-07 run alone before the parallel rows, on one lazily built fixture (running and stopped server, network, floating IP via netplan, firewall, LB with TCP service, automounted volume, zone with record) and one stop/start cycle per behaviour; settings.toml restored at the end.
- e2e checks: LIFE-02 uptime continues; LIFE-03 the running server is back with a new boot, the stopped one stays off; LIFE-04 every resource after each cycle; LIFE-07 no hlab route or NRPT rule and forwarding as in `hostnet-restore.json` while stopped, all back after start.
- LIFE-05 is manual, no test row.
- Documentation last: commands.md, configuration.md, deviations.md, README.

## Additions by the user

- At start nothing reconciles the stored server status with the VM state before `Restore` has run; the normal reconciliation runs after it. Actions still running at stop end with `error` at start, unless this is already built.
- At a Windows shutdown the `AutomaticStopAction` set at the last start applies. If Hyper-V accepts `Set-VM -AutomaticStopAction` on running VMs (RESEARCH.md topic 1 leaves it open: verify it on a real server VM during implementation), it is also set whenever `stop_behaviour` changes; otherwise the documentation says a change applies to a Windows shutdown only after the next cloud start.
- `public_range` is also refused when it overlaps 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or 100.64.0.0/10.
- If the state holds assigned IPs (servers, primary IPs, floating IPs, LBs) outside a changed `public_range`, hlabd refuses to start with problem `public_range_changed`, naming the old range and `--discard-state`; the incompatible-state handling applies likewise (planner question Q1, answer a).
- `--discard-state` also removes the old state's NRPT rules of its zones and the tagged host objects `hostnet` cleans up.
- A state from a newer hlab version (higher `user_version`) says so explicitly in the message; a downgrade does not look like a broken file.
