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
| 1b | servers, host link, reachability | **in progress: next 1b-3** |
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

### Open for 1b

- The first real `ActionTable` comes with the server actions.
- `endInterruptedActions` trims the finished actions once per action.
- Give the resources one helper for "unique within the project" (SSH keys scan
  every key twice today).
- Hetzner ran a reboot while a poweroff was running, where `Lab.Act` answers 423.
  The real server's JSON (no `datacenter`, `locked` `false`) is recorded in
  `compat/requirements.md`.
- `hlabd --platform fake` listens on `127.0.0.1`, the CLI's default is
  `198.18.0.1`: until the host link exists, take `HLAB_CONTROL` from hlabd's log
  (`bin/dev smoke` does).
- Not yet: the other platforms (`hlabd` without `--platform fake` says so),
  `hlab start`, 503 for requests that arrive while the lab stops, `PUT` and
  `DELETE` of images (3a), the catalog's image sources for the image cache.

### Later phases

- The injected fault's delay runs on the wall clock, not the lab's time scale
  (2c).
- Stop requests from signing out and the tray (6b).

## Phase 1b

| Step | What | State |
|---|---|---|
| 1b-1 | `pshost`, the Hyper-V hypervisor, the live tests (`bin/dev itest`) with the hypervisor's contract tests | done |
| 1b-2 | the appliance's build and the agent; agent tests in a namespace | done |
| 1b-3 | HostNet, the appliance's start and watchdog, the life cycle | next |
| 1b-4 | image cache, `seed`, servers and Primary IPs | open |
| 1b-5 | dev loop, `install.ps1`, the CLI from WSL2 | open |
| 1b-6 | appliance update (A/B) with running servers | open |

### What is there after 1b-1

- `internal/platform/windows/pshost`: up to four long-running `powershell.exe`,
  JSON over stdin and stdout, kill and restart on cancel or crash. Its unit tests
  run against a real `powershell.exe` (`bin/dev test --windows`, CI on Windows).
- The Hyper-V hypervisor: VMs (Gen2, Secure Boot, static memory, NICs with MAC
  and port mode, empty DVD drive), power, state and heartbeat, `FindVMs` by
  notes, disks (dynamic, differencing, grow, flatten, attach and detach while
  running), seed ISO with IMAPI2. `windows.New` returns it; `hlabd` still runs
  only `--platform fake`.
- The platform interfaces lost what no phase before 3a calls:
  `CheckpointDisk` (3a), `Counters` (4), `Console` (6a), and
  `HostNet.SetPortVLAN` (a NIC's port mode comes with the NIC);
  `system.VMRunning` is gone.
- `bin/dev itest` builds `itest/` for Windows (tag `live`) and runs it through
  interop, without elevation: the developer is in the group Hyper-V
  Administrators. The hypervisor's contract tests pass against Hyper-V in about
  30 s and leave nothing behind.
- `BenchmarkChangeWithManyActions` (`internal/lab`): with five projects of 1000
  actions, one `Change` that sets an action's progress takes about 1.3 ms
  (before: 17 ms), a `Change` that only reads 0.05 ms (before: 7 ms), an
  action without steps (created, finished, the oldest dropped) 1.5 ms, on a
  Ryzen AI Max+ 395. A Tx copies and compares single records instead of whole
  kinds, and `state.Encode` reuses the JSON of the records that did not change;
  the file stays the same, byte for byte.

### Open for 1b-2 and later

- Live tests that need administrator rights (HostNet, NRPT) come with 1b-3.
  They run through a task "HLab itest" in the Task Scheduler (highest rights,
  created once as administrator): `bin/dev itest` copies the test `.exe` to
  `C:\HLabDev\itest`, starts the task with `schtasks /run` and reads the
  output and exit code, without UAC. Only the developer and administrators may
  write that folder. 1b-3 brings forward the part of `dev-install` (1b-5) that
  creates the task.
- The seed's contents (with name servers) come with `internal/seed` (1b-4); the
  ISO's format is checked: label `cidata`, Joliet names.
- `ShutdownVM` against a real guest (shutdown service) is checked with the first
  server (1b-4); the contract tests have VMs without an OS.

### What is there after 1b-2

- `internal/agentproto`: HTTP and JSON between hlabd and the agent (vsock port
  5000), the pictures `links`, `ruleset`, `dhcp`, `metadata` with their checks.
- `internal/picture`: the four pictures as pure functions of a `picture.Lab`
  (host link, MAC set, servers), with golden files. The fixed ruleset: servers
  reach the host only on 8080 and 8081, the uplink only the Internet, no IPv6
  to the uplink, no rule that needs conntrack to see a connection start.
- `internal/agent` and `cmd/hlab-agent`: applies whole pictures (netlink, `nft
  -f`, dnsmasq, the metadata service on `169.254.169.254`), announces its
  addresses when a carrier comes up (gratuitous ARP and neighbor
  advertisements, built in Go), leaves the uplink to networkd. Its tests run in
  a network namespace: `bin/dev test --agent` (Docker), CI job `agent` with sudo.
- `bin/dev appliance`: Debian 13 with mmdebstrap, without root, as a dynamic
  VHDX for Gen2 with Secure Boot (Debian's signed shim and grub), with the agent
  before `sysinit.target`, networkd only for the uplink, unbound as the lab's
  resolver, a boot ruleset that forwards nothing.

### Open for 1b-3 and later

- Boot the appliance under Hyper-V (1b-3): the VHDX is built but has not
  started in a VM yet. hlabd still pushes no pictures; `system` gets the agent
  client (hv_sock, `go-winio`) and `lab` builds `picture.Lab` from its state.
- The servers in `picture.Lab` come from the server records (1b-4).
- unbound's fixed config names the default host link addresses; when the
  ranges become settings, its addresses come with a picture (phase 2b `dns`).
- dnsmasq is restarted when its configuration changes, not sent SIGHUP: it reads
  its ranges and interfaces only at its start. Its leases are static, so a
  restart loses nothing.

Next: 1b-3, HostNet, the appliance's start and watchdog, the life cycle.

Not there yet: the commands of later phases.
