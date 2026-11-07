# Roadmap

HLab is rebuilt in Go. Phases 1 to 7 target Windows 11 with Hyper-V, used from WSL2;
Linux follows in phase 8. Each step ends with its state recorded here, so the next
one starts from this file.

The earlier Python implementation (Docker, QEMU) is on the branch `legacy`, tagged
`v1-python`. It is the reference for behaviour the rebuild takes over: Hetzner's
answers and timings, the rules for actions and flags, the state and its migrations,
the tests. Look there before writing such a part anew.

## Phases

| Phase | What | State |
|---|---|---|
| 1a | core without VMs: state, lab, web basics, catalog, reading resources, SSH keys, lab control, `--platform fake` | done |
| 1b | servers, host link, reachability | **next: 1b-1** |
| 2a | Floating IPs, firewalls, private networks | open |
| 2b | volumes, placement groups, DNS | open |
| 2c | tools for tests | open |
| 3a | snapshots, backups, rebuild, type change | open |
| 3b | lab images, import and export, Packer, ISOs, rescue | open |
| 4 | load balancers, certificates, metrics | open |
| 5 | Storage Boxes | open |
| 6a | complete web UI, console | open |
| 6b | tray, installer | open |
| 7 | benchmark and switch-over | open |
| 8 | Linux | open |

## Phase 1a

| Step | What | State |
|---|---|---|
| 1a-1 | repo skeleton: Go module, minimal commands, layer test, `bin/dev`, `bin/dev.ps1`, CI, docs | done |
| 1a-2 | `internal/state` and `internal/lab` with tests: transactions, saving, migrations, actions with a test clock; platform interfaces, fake, contract tests, Windows stubs | done |
| 1a-3 | `internal/web` and `internal/spectest`; projects, tokens | done |
| 1a-4 | catalog and the reading resources; SSH keys | done |
| 1a-5 | lab control, CLI, `hlab env`; web UI skeleton | done |
| 1a-6 | checks against the real API (read only) | done |
| 1a-R | review of phase 1a, smoke tests | done |

After 1a-2: `hlabd` does not start the lab yet (1a-5). There is no `internal/config`
yet; `lab.Options` takes the data folder and the time scale directly, and
`hlab.yaml` comes when the service reads it. `internal/system` holds only what the
life cycle needs.

After 1a-3: `internal/web` checks tokens, rate limit, permission and injected
faults and answers in Hetzner's error format; `internal/api/cloud` has its first
endpoints, `GET /actions?id=…` and `GET /actions/{id}`, and every API test checks
its answers against the spec (`internal/spectest`). Projects, tokens, limits and API
faults are in the lab's core; nobody calls them yet but tests: the lab control and
the CLI come in 1a-5, which also wires the Cloud API into `hlabd`. `compat/` has its
first state. The order of the checks, the error texts and details, pagination and
the `Link` header were measured on the real API (2026-11-07) and follow it; what is
still open for 1a-6 is at the end of `compat/requirements.md`. Requests are not yet checked against the spec's request
schemas (the old HLab did; it comes with the first endpoint that takes a body).

After 1a-4: `internal/catalog` holds Hetzner's catalog with its real IDs (built in,
changed by a `catalog.yaml` in the data folder); the lab takes it in
`lab.Options.Catalog`, but nothing loads the user's file yet: `hlabd` does that when
1a-5 wires it. The Cloud API answers locations, server types, load balancer types,
pricing, images (the catalog's system images, Debian 13 and Ubuntu 26.04) and ISOs,
and the SSH keys in full; the datacenter endpoints answer 410 as Hetzner's do.
Request bodies are checked (`web.DecodeJSON`, `web.Check`). The catalog comes from
the real API's answers, and the error texts, sorting and filters of these endpoints
and of the SSH keys were measured on it (2026-11-07, `compat/requirements.md`);
1a-6 is left with what that file lists as still open. Not yet: `PUT` and `DELETE`
of images and their actions (with snapshots, 3a); the catalog's image sources for
the image cache (1b).

After 1a-5: `hlabd --platform fake` runs on Linux, WSL2 and Windows: `hlab.yaml`
(`internal/config`: ports, time scale), the user's catalog, the project `default`
in a new lab, the Cloud API on 8080, the Hetzner API on 8081 (no routes yet), the
lab control and the web UI on 8090, all on `127.0.0.1`. One `hlabd` per machine
(the mutex `Global\HLab` on Windows, an flock on `/run/lock/hlab.lock` on Linux;
`internal/platform/linux` has only this life cycle so far). The CLI `hlab` has
`env` (sh and PowerShell; the project's token `hlab-env` stays the same, kept in
`env-tokens.json`), `status`, `stop`, `project` and `token`; it finds the
token as the Windows HLab will write it, also from WSL2 through `/mnt/c`, and
calls `198.18.0.1:8090` unless `HLAB_CONTROL` says otherwise. The web UI is a
skeleton: projects, tokens, locations and server types; Host and Origin checks, a
cookie instead of a token. Checked by hand in WSL2 with hcloud 1.70.2:
`server-type list`, `location list`, `ssh-key create/list/delete` after
`eval "$(hlab env)"`, and OpenTofu 1.13.9 with `examples/tofu-1a` (apply, an
empty plan after it, destroy). Not yet: the other platforms (`hlabd` without
`--platform fake` says so), `hlab start` (1b), 503 for requests that arrive while
the lab stops (no slow stop yet: the ports close at once), the formal smoke tests
(1a-R), stop requests from signing out and the tray (6b). Also fixed in 1a-5: the
state writer wrote the same content twice when a change came while it woke up;
on Windows the second write failed when a reader held `state.json`
("access denied"), which made `TestChangeSavesTheNewState` fail now and then.

After 1a-6: the rest of `compat/requirements.md` was measured on the real API
(2026-11-07, "Label selectors, bodies and servers"), with SSH keys and one short-
lived server in the empty test project. HLab now gives Hetzner's reasons for every
label selector error, refuses an empty value (`k=`), and selects as Hetzner does
(`k!=v` needs the label; the odd forms `!k=v`, `k=!v`, `k=a=b`); a PUT names only
its first invalid field, in the details too; bodies may be up to 64 MiB (Hetzner
took that much); every resource's action endpoints share the budget of
`/actions`. The real server's JSON (no `datacenter`, `locked` `false`) is recorded
for 1b. Open for 1b: Hetzner ran a reboot while a poweroff was running, where
HLab's `Lab.Act` answers 423. Phase 1a is ready for its review (1a-R).

After 1a-R: two review rounds (a junior and a senior developer, 8/10 each
round, both satisfied). Fixed: a change after `Lab.Close` (a request that
outlived the stop timeout) panicked on the writer's closed channel and is now
logged and dropped; a huge `page` overflowed in `web.Paginate`; label errors
come in a fixed order. The layer test reads the table of DEVELOPING.md, its
one copy. DEVELOPING.md says when records may outlive their Tx (to be read
only) and that every `Change` saves the whole state, and has a request's path
and a glossary. `bin/dev smoke` runs the smoke tests (DEVELOPING.md, Tools);
the OpenTofu example is `examples/tofu-1a`. The smoke tests passed in WSL2
with hcloud 1.70.2 and OpenTofu 1.13.9.

Open for 1b, from the review:

- Reads copy whole kinds (`lab.View`, `TokenByHash` on every request,
  `TakeAPIFault`), and a Tx that only read saves the whole state. Cheap now;
  with servers and actions polled in 1b it may need a read path without copies
  and a Tx that knows which kinds changed. Decide before the hot paths grow.
- Once `hlabd` listens on the host link, settle who may reach port 8090: the
  web UI's cookie gives what the control token gives.
- The web UI's calls through `ServeAs` count against the project's rate limit
  and take its injected faults; the `Link` header behind `ServeAs` names the
  control port with a Cloud API path.
- Smaller: `lab.Open` could report a new lab instead of `cmd/hlabd` checking
  `state.json` itself; the default project is made before the ports listen;
  rate limit buckets of deleted projects stay.

Next: 1b-1, `pshost` and the Hyper-V hypervisor with the contract tests.

Not there yet: `bin/dev test --agent` (it reports "not yet implemented") and
the commands of later phases.
