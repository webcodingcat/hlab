# Roadmap

HLab is rebuilt in Go. Phases 1 to 7 target Windows 11 with Hyper-V, used from WSL2;
Linux follows in phase 8. Each step ends with its state recorded here, so the next
one starts from this file. How a step got there is in its commit messages.

The earlier Python implementation (Docker, QEMU) is on the branch `legacy`, tagged
`v1-python`. It is the reference for behaviour the rebuild takes over: Hetzner's
answers and timings, the rules for actions and flags, the state and its migrations,
the tests. Look there before writing such a part anew.

## Phases

| Phase | What | State |
|---|---|---|
| 1a | core without VMs: state, lab, web basics, catalog, reading resources, SSH keys, lab control, `--platform fake` | done |
| 1b | servers, host link, reachability | **in progress: next 1-L** |
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
| 1a-6 | checks against the real API | done |
| 1a-R | review of phase 1a, smoke tests, CI on every push | done |

### What is there

- `hlabd --platform fake` runs on Linux, WSL2 and Windows: `hlab.yaml` (ports,
  time scale), the user's `catalog.yaml`, the project `default` in a new lab, the
  Cloud API on 8080, the Hetzner API on 8081 (no routes yet), the lab control and
  the web UI on 8090, all on `127.0.0.1`. One `hlabd` per machine (the mutex
  `Global\HLab` on Windows, an flock on `/run/lock/hlab.lock` on Linux).
- The Cloud API: actions, locations, server types, load balancer types, pricing,
  images (Debian 13, Ubuntu 26.04), ISOs, SSH keys in full; the datacenter
  endpoints answer 410. Order of the checks, error texts, pagination, sorting,
  filters, label selectors, bodies and rate limits follow the real API, measured
  on 2026-11-07 (`compat/requirements.md`). Every API test checks its answers
  against the spec.
- The state: transactions that copy on first use, reads without copies, saving
  only real changes in the background; migrations with a sample file of
  version 1.
- The CLI `hlab`: `env` (sh and PowerShell; the token `hlab-env` stays the same),
  `status`, `stop`, `project`, `token`. The web UI is a skeleton: projects,
  tokens, locations and server types.
- Tests: `bin/dev test` (Linux, race detector), `bin/dev test --windows`, and
  `bin/dev smoke` with hcloud 1.70.2 and OpenTofu 1.13.9 (`examples/tofu-1a`).
  CI runs lint and the unit tests on Linux and Windows for every push.
- Reviews: four rounds, junior and senior 8/10 each time and satisfied.

### Open from 1a

- `hlabd --platform fake` listens on `127.0.0.1`, the CLI's default is
  `198.18.0.1`: with the fake platform, take `HLAB_CONTROL` from hlabd's log
  (`bin/dev smoke` does).
- Not yet: `PUT` and `DELETE` of images (3a).

## Phase 1b

| Step | What | State |
|---|---|---|
| 1b-1 | `pshost`, the Hyper-V hypervisor, the live tests (`bin/dev itest`) with the hypervisor's contract tests | done |
| 1b-2 | the appliance's build and the agent; agent tests in a namespace | done |
| 1b-3 | HostNet, the appliance's start and watchdog, the life cycle | done |
| 1b-4 | image cache, `seed`, servers and Primary IPs | done |
| 1b-5 | dev loop, `install.ps1`, the CLI from WSL2 | done |
| 1b-6 | appliance update (A/B) with running servers | done |
| 1b-R | review of phase 1b | done |
| 1-L | the live tests of phase 1 on the finished state | next |

### What is there

DEVELOPING.md and the files under `docs/` explain each part; this is the
list.

- **hlabd on Windows** (`--platform windows`, the default there), with
  administrator rights: the host link `HLab-Link` (`198.18.0.1/29`,
  `2001:db8:0:ffff::1/64`, routes of the lab's ranges through the appliance)
  and `HLab-Public`, the APIs on `198.18.0.1`, the lab control also on
  `127.0.0.1:8090`. `CheckRanges` refuses ranges that collide; NRPT rules
  carry the comment `hlab`. While the lab stops, requests get 503.
- **Hyper-V** through `pshost` (up to four long-running PowerShell
  processes): Gen2 VMs with Secure Boot and static memory, differencing
  disks, the seed ISO (IMAPI2), power and heartbeat from WMI; the agent over
  hv_sock (`go-winio`).
- **The appliance:** Debian 13 from Debian packages only, built without root
  (`bin/dev appliance`), with `hlab-agent`, which applies whole pictures
  (`links`, `ruleset`, `dhcp`, `metadata`) and announces its addresses. Its
  uplink has its own routing table. The watchdog checks it every 2 s and
  makes it anew after three failures, or at once when it is off. A start
  that takes over an appliance of an older release updates it without
  stopping the servers (MAC sets A and B).
- **Servers** (create, get, update, delete, lists, `poweron`, `poweroff`,
  `shutdown`, `reboot`, `reset`, `change_protection`, action lists): Debian 13
  and Ubuntu 26.04 on cached images (qcow2 converted in Go), NoCloud seeds,
  Hetzner's statuses, times and queue (a power action during another waits
  instead of 423). A create records the server and plans its actions in one
  transaction (`Lab.Create`); a start reserves its room in its plan, so
  starts sent at once cannot overcommit the host's memory (412
  `resource_unavailable`); a create also needs a tenth of the data folder's
  drive free. `want_running` changes only in the actions' plans, so queued
  actions keep the order they were sent in. A failed create removes its VM
  and files; the watchdog gives a server that a failed action left
  `starting` or `stopping` its VM's status.
- **Primary IPs** (create, get, update, delete, lists, `assign`, `unassign`,
  `change_protection`), addresses from `198.19.0.0/16` and `2001:db8:1::/48`
  in order, the errors as measured.
- **The life cycle:** a stop shuts the servers down (`shutdown_timeout`, 60 s)
  and keeps `want_running`; a start takes over running VMs, reconnects and
  starts the others as they want, gives a server without a VM one with its
  disk, or builds it anew without a disk; a server that cannot come back
  stays off, and the rest start. A start removes the disks and seeds of
  servers that are gone. A crash of hlabd leaves everything running.
- **For users and developers:** `install/install.ps1` (from `bin/dev
  package`; `-Uninstall`, `-KeepData`), the start menu entry, `hlab start`,
  `hlab stop`, `hlab ssh` with the lab's key, the CLI from WSL2 finding the
  lab by itself; the dev loop `bin/dev deploy [--keep-vms]` through the task
  "HLab dev"; the log in `logs/hlabd.log`.
- **Tests:** unit tests with the fake machine and spec checks; the agent's
  tests in a network namespace; the live tests through the task "HLab itest"
  (contract tests, appliance, and `TestServersBootAnswerAndOutliveTheLab`:
  two servers through the Cloud API, SSH and metadata over IPv4 and IPv6,
  reachability from Windows and WSL2, isolation, stop and start, crash and
  takeover, the appliance turned off hard and updated with a running
  server); `bin/dev smoke` with `examples/tofu-1b`.
- **Measured** on a Ryzen AI Max+ 395: a create until SSH answers 13 s (image
  cached); the appliance from nothing 11.8 s; after it was turned off hard,
  a server answers again within 5.4 s; the appliance's update 11 s, during which a server took no
  new connection for 0.5 s at most; the dev loop 29 s, 9 s with
  `--keep-vms`; all live tests about 5 minutes.

### Open for later phases

- Volumes, networks, firewalls and placement groups in a server's create
  request are ignored until their phase (2a, 2b); `change_dns_ptr` comes
  with DNS (2b). The metadata summary says `private-networks: []` on a
  server without a network, where Hetzner leaves it out (2a).
- unbound's configuration and the uplink's unreachable route name the default
  ranges; when the ranges become settings, they come with a picture (2b).
- Still to test: the ruleset's counters for traffic between servers (the
  agent has counters from phase 4); the uninstall in a live test (played
  through by hand).
- A start of the dev loop takes 20 s: 9 s for the host link's switches, 11 s
  for the appliance from nothing.
- Closing the start menu entry's window ends hlabd as a crash would; the
  tray (6b) replaces the window.
- The firewall rules do not block the lab's own range as a source on 8090;
  the appliance's ruleset keeps the servers off it.
- `boxes.vhdx` moves to the new appliance at an update with the Storage
  Boxes (5); the pictures' generations stay in memory, so a start sends
  every picture again.

- Raised in the reviews, left open: DEVELOPING.md still restates much of
  what the code's comments say, and its style is dense; a rename waits for
  the pictures to reach the appliance; the reserves of the room check are
  constants, not settings in `hlab.yaml`.

### Later phases

- The injected fault's delay runs on the wall clock, not the lab's time scale
  (2c).
- Stop requests from signing out and the tray (6b).

Next: 1-L, the live tests of phase 1 on the finished state.

Not there yet: the commands of later phases.
