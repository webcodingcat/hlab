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
| 1b | servers, host link, reachability | **in progress: next 1b-R** |
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

- `endInterruptedActions` trims the finished actions once per action.
- Give the resources one helper for "unique within the project" (SSH keys,
  servers and Primary IPs each scan their kind today).
- `hlabd --platform fake` listens on `127.0.0.1`, the CLI's default is
  `198.18.0.1`: with the fake platform, take `HLAB_CONTROL` from hlabd's log
  (`bin/dev smoke` does).
- Not yet: `PUT` and `DELETE` of images (3a). (`hlab start` came with
  1b-5; the Windows platform and 503 while the lab stops with 1b-3; the first
  action tables, the server JSON, the queue instead of 423 and the catalog's
  image sources with 1b-4.)

### Later phases

- The injected fault's delay runs on the wall clock, not the lab's time scale
  (2c).
- Stop requests from signing out and the tray (6b).

## Phase 1b

| Step | What | State |
|---|---|---|
| 1b-1 | `pshost`, the Hyper-V hypervisor, the live tests (`bin/dev itest`) with the hypervisor's contract tests | done |
| 1b-2 | the appliance's build and the agent; agent tests in a namespace | done |
| 1b-3 | HostNet, the appliance's start and watchdog, the life cycle | done |
| 1b-4 | image cache, `seed`, servers and Primary IPs | done |
| 1b-5 | dev loop, `install.ps1`, the CLI from WSL2 | done |
| 1b-6 | appliance update (A/B) with running servers | done |
| 1b-R | review of phase 1b | next |

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

- Done in 1b-4: the seed's contents (with name servers) in `internal/seed`;
  `ShutdownVM` against a real guest (the server live test: off 8 s after the
  shutdown).

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

### Open for 1b-3 and later (from 1b-2)

- Done in 1b-4: the servers in `picture.Lab` come from the server records.
- unbound's fixed config and the uplink's unreachable route name the default
  ranges; when the ranges become settings, they come with a picture (phase 2b
  `dns`).
- dnsmasq is restarted when its configuration changes, not sent SIGHUP: it reads
  its ranges and interfaces only at its start. Its leases are static, so a
  restart loses nothing.
- The metadata summary leaves out `private-networks` on a server without a
  network, HLab's says `[]` (2a, `compat/requirements.md`).

### What is there after 1b-3

- `hlabd` runs on Windows (`--platform windows`, the default there) with
  administrator rights: the host link, the appliance, the APIs on `198.18.0.1`
  and the lab control also on `127.0.0.1:8090`. The appliance's release disk is
  `hlab-appliance.vhdx` next to `hlabd` or `--appliance`.
- `HostNet` and `HostDNS` for Windows (`hostnet.ps1`): the switches `HLab-Link`
  (internal, host `198.18.0.1/29` and `2001:db8:0:ffff::1/64`, routes of
  `198.18.0.0/15` and `2001:db8::/32` through the appliance) and `HLab-Public`
  (private), addresses and routes in the active store only; `CheckRanges`
  names the range and the interface that collide; NRPT rules with the comment
  `hlab`. `HLab-Private` follows in 2a.
- The agent dialer over hv_sock (`go-winio`, `VsockServiceID(5000)`).
- New in the platform: `Hypervisor.DeleteDisk`, the port mode `Plain` (the
  appliance's link and uplink), `LinkSpec.Gateways` (one per address family) and
  `LinkSpec.PrivateSwitches`, `platform.SwitchPrefix` (`HLab-`).
- The life cycle (13.1–13.3): `Lab.StartMachine` checks the ranges, connects
  the host, removes stray VMs, starts or takes over the appliance, sends every
  picture and clears old NRPT rules; `Lab.Close` shuts the appliance down and
  removes the host link and the NRPT rules. While the lab stops, the APIs and
  the lab control answer 503 `unavailable` (but for status and shutdown). A
  crash of hlabd leaves the appliance and the link running; the next start
  takes them over without a restart.
- The appliance boots under Hyper-V: Secure Boot, the agent over hv_sock, the
  four pictures, unbound resolving through the uplink, IPv4 and IPv6 on the host
  link. The watchdog checks every 2 s (VM, heartbeat, agent) and restarts it after
  three failures, or at once when the VM is off; it sends the pictures an agent
  lacks.
- The uplink has its own routing table (`uplink`, 200, networkd): only traffic
  from `public` and the appliance's own looks it up, after the main table, and
  `198.18.0.0/15` is unreachable in it.
- Fixed in the appliance: unbound did not start without its trust anchor
  (`dns-root-data`, a Recommends that `--variant=apt` leaves out); the agent
  left networkd's uplink down after renaming it, so it got no DHCP lease;
  `serial-getty@ttyS0` restarted every 10 s without a COM port (masked).
- Accepted deviations from the plan: the watchdog restarts an appliance that
  is off at once; after a stop the appliance is made anew (`DeleteDisk`)
  instead of reconnecting its NICs.
- The admin live tests run through the task "HLab itest" (`bin/dev.ps1
  dev-install`, once as administrator; `bin/dev itest`), without UAC.
- `--platform fake` runs the same start on the fake machine, with a fake agent
  (`fake.Agent`).
- Measured on a Ryzen AI Max+ 395 (Windows 11, Hyper-V): the appliance from
  nothing (disk, VM, boot, every picture) 11.8 s; after it was turned off hard,
  back with its pictures after 6.0 s (before the immediate restart: 17.6 s);
  `hlabd` cold 18.9 s until the lab control answers, taking over a running
  appliance after a crash 4.3 s, stopping 5.2 s. All live tests: 90 s
  (hypervisor 30 s, host network 6 s, NRPT 1 s, appliance 30 s).

### Open for 1b-4 and later (from 1b-3)

- Settled in 1b-4: a start detaches and attaches the public NIC of every
  server VM that is off, so it is on `HLab-Public` again; no new platform
  method.
- Done in 1b-5: `hlab start` and the dev loop (`HLab dev`, firewall rule for
  WSL2).

### What is there after 1b-4

- **Servers** (`POST`, `GET`, `PUT`, `DELETE /servers`, the lists, `poweron`,
  `poweroff`, `shutdown`, `reboot`, `reset`, `change_protection`, the action
  lists): Debian 13 and Ubuntu 26.04 as Gen2 VMs with Secure Boot, static
  memory, a differencing disk of the type's size on the cached image, the
  seed in the DVD drive and the public NIC isolated on `HLab-Public`.
  Statuses and times as Hetzner's; the JSON in Hetzner's order of fields,
  `locked` always `false`.
- **Hetzner's queue, not 423.** Measured on 2026-11-08: a power action sent
  during another (also during the create) gets 201 and runs when the one
  before has ended. The server table is `Queued`; `reboot` and `reset` start
  a server that is off, as on Hetzner. Primary IP actions still lock the IP
  (423 `locked` for a delete meanwhile, measured).
- **Primary IPs** (`POST`, `GET`, `PUT`, `DELETE`, the lists, `assign`,
  `unassign`, `change_protection`): a server's own ones (`primary_ip-<id>`,
  `auto_delete`) or given ones; `assignee_type` `"unassigned"`, the default
  `dns_ptr`, `unassign` clearing `auto_delete`, the errors with their details
  `null`, all as measured. Addresses from `198.19.0.0/16` and one `/64` per
  IPv6 from `2001:db8:1::/48`, handed out in order.
- **The image cache:** the catalog names each image's qcow2 and checksums;
  the first server of an image loads it, `internal/imagefmt` converts it in
  Go to a fixed VHD, and `Convert-VHD` makes the dynamic VHDX
  `images/cache/<sha256>.vhdx`. Debian's genericcloud image loads in 25 s.
- **`internal/seed`:** NoCloud `meta-data`, `user-data`, `vendor-data` and
  `network-config` v2 (IPv4 by DHCP, IPv6 `::1` with `fe80::1`, always the
  lab's resolvers), written as JSON. The metadata service's pictures come
  from the server records.
- **The life cycle with servers:** a start takes over running server VMs,
  reconnects and starts those that want to run; a stop shuts them all down
  (`shutdown_timeout` in `hlab.yaml`, 60 s), `want_running` stays; a watchdog
  sets a server whose guest shut down `off`. A create or poweron beyond the
  host's room (memory static, reserve 25 % or 6 GB, the appliance's 1 GB,
  4 vCPUs per thread; `Hypervisor.HostResources`) is 412
  `resource_unavailable`.
- **Tests:** API tests with spec checks for servers, Primary IPs and their
  actions (`compat/requirements.md`); the lab's queue, room, takeover,
  watchdog and stop with the fake; `imagefmt` against qcow2 files of
  `qemu-img`; the seed's golden files. The live test creates a Debian server
  through the Cloud API and checks SSH, the metadata over IPv4 and IPv6, reset,
  the guest's shutdown and the delete. `bin/dev smoke` runs the hcloud CLI on
  servers and Primary IPs, and `examples/tofu-1b` has a server with a Primary
  IP.
- Measured on a Ryzen AI Max+ 395 (Windows 11, Hyper-V), with the image
  cached: create until SSH answers 13 s, after a reset 7 s, a shutdown until
  `off` 8 s. All live tests: 130 s (hypervisor 30 s, host network 6 s, NRPT
  1 s, appliance 31 s, server 62 s).

### Open for 1b-5 and later

- The free disk space of the room check (plan, 5 no. 18: 10 % free) is not
  checked yet; the operator left it for later.
- Volumes, networks, firewalls and placement groups in a server's create
  request are ignored until their phase (2a, 2b).
- `change_dns_ptr` of servers and Primary IPs comes with DNS (2b).
- Settled after 1b-4: the errors of a Primary IP a server cannot take
  (another server's, another location's, the other type) are measured and
  taken over; a server whose create HLab's end cut short is built anew at the
  next start; a start removes the cached images that no entry and no disk
  needs; the metadata's MAC addresses are quoted in its YAML.

### What is there after 1b-5

- **The dev loop:** `bin/dev deploy [--keep-vms]` builds in WSL2, stops the
  dev loop's `hlabd` (`hlab stop`, or `schtasks /end` as a crash), copies it to
  `C:\HLabDev\bin` and starts it through the task "HLab dev" (`--data
  C:\HLabDev\data`, highest rights, S4U, normal priority), without UAC.
  `bin/dev.ps1 dev-install` adds the task and the firewall rule "HLab dev" and
  protects all of `C:\HLabDev`. Measured on a Ryzen AI Max+ 395: 29 s with the
  appliance made anew (stop 9 s, start 20 s), 9 s with `--keep-vms` (stop 2 s,
  takeover 7 s).
- **`install/install.ps1`** (as administrator), from `build/package` (`bin/dev
  package`): Hyper-V on, `C:\Program Files\HLab`, the start menu entry "HLab"
  (as administrator, minimized), `hlab.exe` on the machine's PATH, the hv_sock
  service, the firewall rule "HLab" (hlabd.exe, TCP 8080, 8081, 8090 on
  `198.18.0.1`), `C:\ProgramData\HLab`, the Linux CLI in `~/.local/bin` of the
  default WSL distribution. `-Uninstall` stops HLab and removes VMs,
  switches, NRPT rules and everything it added; `-KeepData` keeps the data
  folder. Played through on the
  operator's machine (through the task "HLab itest"): install, update,
  `hlab start` from WSL2 with UAC (24 s until it answered), `hlab stop`,
  uninstall with HLab stopped and with HLab running; nothing of HLab was left
  on the host. No Windows service and no autostart.
- **The CLI from WSL2:** the client finds the running lab's data folder in
  `%ProgramData%\HLab\instance` (in WSL2 through `/mnt/c`), so `hlab` finds the
  dev loop's lab and an installed one without `HLAB_TOKEN_FILE`; a connection
  gives up after 2 s (the host link's address is gone after a stop). `hlab
  start` (UAC through `Start-Process -Verb RunAs`, waits until the lab runs);
  `hlab stop` waits until the lab has stopped (`--no-wait`). From WSL2 with
  `hlab env`: `hcloud server create --image debian-13 --type cpx22`, SSH to its
  IPv4 (the guest sees `198.18.0.1`), delete.
- **The log:** `logs/hlabd.log` in the data folder (JSON lines, moved to
  `hlabd.log.1` at a start from 10 MB), also text on stderr.

### After 1b-5 (review of the step)

- **A server without a VM gets one with its disk.** A start gives a server
  whose VM is gone but whose disk is there a new VM around that disk
  (`Lab.restoreServer`, `system.CreateServer` keeps a disk that is there);
  only a server without a disk is built anew. So `install.ps1 -Uninstall
  -KeepData` removes the VMs, as the plan has it, and the next start brings
  the servers back.
- **`hlab ssh SERVER [COMMAND...]`** logs in as root with the lab's key: `hlabd`
  makes an Ed25519 key in the data folder (`lab-key`, `internal/sshkey`) and
  every server takes its public key from the seed and the metadata service;
  the lab control hands out address and key
  (`GET /_lab/projects/{name}/servers/{server}/ssh`). Servers created before
  the key existed do not have it.
- **Fixed: the VMs' state came late.** `Get-VM` in a long-running PowerShell
  process showed a VM's state up to seconds old when another process of the
  pool had changed it: the watchdog took a server that had just started for
  one that had shut down (it stopped wanting to run), and an appliance that
  was turned off for one running without heartbeat. Power and heartbeat now
  come from WMI (`Msvm_ComputerSystem`, `Msvm_HeartbeatComponent`). Measured
  since: the appliance turned off hard, the server answers again after 5.4 s
  (before 10 s); a guest's shutdown until `off` 4 s (before 8 to 11 s); the
  appliance shuts down in 1 s.
- **Fixed: two deletes at once.** `delete-disk` read the drives of every VM and
  failed on a VM that another delete was removing meanwhile.
- **The live tests** (`TestServersBootAnswerAndOutliveTheLab`, 170 s with
  the images cached) now cover the plan's 1b list as far as 1b-5 goes: Debian
  and Ubuntu until SSH; IPv4 and IPv6 from Windows, IPv4 from WSL2; the
  guest sees `198.18.0.1` (and `2001:db8:0:ffff::1`); Internet yes, 8090 and
  the private ranges behind the uplink no; servers meet only through the
  appliance (TTL 63); stop and start with the same servers, addresses and
  disks, also with a VM removed; a crash (another process killed) and the
  takeover without a boot; the appliance turned off hard (< 10 s); after the
  stop no switch, address, route or NRPT rule is left.

### Open for 1b-6 and later (from 1b-5)

- Still to test: the
  ruleset's counters for traffic between servers (the agent has no counters
  before phase 4; the TTL shows the way for now); the uninstall in a live
  test (played through by hand, see above).
- A dev loop start takes 20 s: about 9 s for the host link (switches) and 11 s
  for the appliance from nothing. The 1.7 s of the plan were measured without
  an appliance. `--keep-vms` keeps it at 9 s.
- Closing the start menu entry's window ends hlabd as a crash would (Windows
  allows a few seconds); the next start takes over. The tray (6b) replaces the
  window.
- The firewall rules have no extra block of the lab's own range as source on
  8090; the appliance's ruleset keeps the servers off it.

### What is there after 1b-6

- **The appliance's update** (`Lab.updateAppliance`): a start that takes over
  a running appliance of another release than the one that came with HLab
  replaces it without stopping the servers. The new appliance starts beside
  the old one with the other MAC set (`hlab-appliance-a` and
  `hlab-appliance-b`), its NICs on the same switches, and gets every picture;
  then the state names it (`lab.appliance`: release and MAC set), the old one
  is turned off and deleted with its system disk, and the new one sends its
  announcements again. If the new one does not come up, the old one runs on
  and the next start tries again. The triggers: `bin/dev deploy --keep-vms`
  after `bin/dev appliance`, and a crashed HLab started again after an
  update; after a normal stop the appliance is off, and the start makes it
  anew on the new release with the state's MAC set.
- **Releases in the data folder:** a start copies the release disk to
  `<data>/appliance/<sha256>.vhdx` (its SHA-256 is the release's ID) and the
  appliance runs on a differencing disk on the copy, so the disk next to
  `hlabd` is never held open. After the start the folder keeps only the
  running appliance's release and system disk and the newest release.
- **HLab's end during an update:** the next start keeps the appliance the
  state names (the old one until the new one has every picture, then the new
  one), removes the other as a stray VM and announces again with the
  pictures; an old one still of another release is updated anew. Unit tests
  with the fake cover the update, a new appliance that does not come up, and
  both cut-short cases.
- **The live test** (`TestServersBootAnswerAndOutliveTheLab`, now 240 s)
  updates the appliance with a running server: a copy of the release disk
  with another VHDX creator field is a new release. Measured on a Ryzen AI
  Max+ 395: the update takes 11 s (the new appliance's agent answers after
  9 s, its pictures 0.3 s, the switch 1.3 s); the server took no new TCP
  connection for 0.5 s at most (tried every 100 ms, so mostly the tries'
  spacing), and an SSH session printing every 100 ms stalled for 110 ms at
  most and lived on. Afterwards only the new appliance (set B) is left, and
  the appliance's folder holds only its release and `system-B.vhdx`.
- **Not in 1b-6:** `boxes.vhdx` moves to the new appliance with the Storage
  Boxes (phase 5); the generations of the pictures stay in memory, as before
  (the agent reports what it has, so a start sends everything again).
- An appliance VM of an HLab before 1b-6 (`hlab-appliance`) counts as stray
  and goes at the next start.

Next: 1b-R, the review of phase 1b.

Not there yet: the commands of later phases.
