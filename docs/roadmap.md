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
| 1a | core without VMs: state, lab, web basics, catalog, reading resources, SSH keys, lab control, `--platform fake` | in progress |
| 1b | servers, host link, reachability | open |
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
| 1a-5 | lab control, CLI, `hlab env`; web UI skeleton | **next** |
| 1a-6 | checks against the real API (read only) | open |

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

Not there yet: `bin/dev test --agent` (it reports "not yet implemented"), `deploy`,
`itest` and `smoke`.
