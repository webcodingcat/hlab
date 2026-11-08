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
| 1b | servers, host link, reachability | done |
| 2a | Floating IPs, firewalls, private networks | in progress |
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
- `CheckRoom` copies every server in its transaction (raised in the last
  review of 1a; fine for the few servers of a lab).

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
| 1-L | the live tests of phase 1 on the finished state | done |

### What is there

DEVELOPING.md holds the rules and recipes, the files under `docs/` explain
each part; this is the list.

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
  drive free; the reserves are settings (`room` in `hlab.yaml`). A rename
  answers at once and sends the pictures in the background. `want_running` changes only in the actions' plans, so queued
  actions keep the order they were sent in. A failed create removes its VM
  and files; the watchdog gives a server that a failed action left
  `starting` or `stopping` its VM's status, and ignores a look that an
  action overtook. A failed action's undo leaves alone what a later queued
  plan set.
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
  package`; `-Uninstall`, `-KeepData`; an update ends a running HLab as a
  crash would, so its servers run on and the next start updates the
  appliance), the start menu entry, `hlab start`,
  `hlab stop`, `hlab ssh` with the lab's key, the CLI from WSL2 finding the
  lab by itself; the dev loop `bin/dev deploy [--keep-vms]` through the task
  "HLab dev"; the log in `logs/hlabd.log`.
- **Tests:** unit tests with the fake machine and spec checks; the agent's
  tests in a network namespace; the live tests through the task "HLab itest"
  (contract tests, appliance, and `TestServersBootAnswerAndOutliveTheLab`:
  two servers through the Cloud API, SSH and metadata over IPv4 and IPv6,
  reachability from Windows and WSL2, isolation, stop and start, crash and
  takeover, the appliance turned off hard and updated with a running
  server and two SSH sessions, one of them quiet until the guest sends
  first after the update; a connection over IPv6 after 45 s without
  IPv6); `bin/dev smoke` with `examples/tofu-1b`.
- **Measured** on a Ryzen AI Max+ 395: a create until SSH answers 13 s (image
  cached); the appliance from nothing 11.8 s; after it was turned off hard,
  a server answers again within 5.4 s; the appliance's update 11 s, during which a server took no
  new connection for 0.5 s at most; the dev loop 29 s, 9 s with
  `--keep-vms`; all live tests about 5 minutes.

### Found by 1-L and fixed

The second run of 1-L on the finished state passed every check: lint,
the unit tests, the agent's tests, the live tests and the smoke tests,
and by hand an installed HLab (create, SSH, isolation, stop, start, a
hard end, an update of HLab over a running one, deleting a project,
`-KeepData`, the uninstall), the host clean at the end. An update over
a running HLab: the servers ran on, a session in which only the guest
sent stalled for 0.36 s at most, new connections failed for 1.2 s at
most; after 45 s without traffic, IPv6 from Windows connected in 40 ms
at most.

- A TCP connection from the host in which only the guest sent hung after
  the appliance's update: the new appliance took its first packet as `new`
  and rejected it. The ruleset now lets TCP with ACK and without SYN from
  the servers to the host through.
- IPv6 from the host to a guest took 5 s after a pause: the appliance had
  no link-local address on `link`, so the kernel could not solicit the
  host for the guests' answers. It has `fe80::2` now.
- `C:\ProgramData\HLab\instance` stays no longer after HLab ends, and the
  live tests write their own; the hv_sock registry key was never needed
  (hlabd only dials into VMs) and is gone from `install.ps1` and
  `dev-install`; tests for the Primary IPs' lists and action lists, the
  servers' sorting and Hetzner's texts per field.
- Deleting a project left its servers' VMs running until the next start;
  `Lab.DeleteProject` now removes them with their disks and seeds and
  sends the pictures.
- `Lab.Act` and `Lab.Create` started actions after `Lab.Close`; the lab
  now has a closed state, and `Close` waits for every goroutine.
- The appliance's watchdog sent a snapshot of the pictures without a lock,
  so an older generation could follow a newer one for one check; every
  sender now reads and sends the newest pictures under one lock.
- `Locks` did not hold off a server's own queued actions: a `poweron` or a
  delete ran beside a Primary IP's assign. They now wait behind it
  (Hetzner's answer is not measured).

### Known gaps

Faults and deviations from Hetzner, each with its level from the review
that found it (must, should, optional) and the phase that closes it.

- **Accepted for now:** the servers' connections to the Internet break at
  an update of the appliance, and at the watchdog's restart of it: the new
  appliance has none of the old one's NAT state, and after an update
  another uplink address too. Connections to the host and among the
  servers live on. Updates are rare and started by the operator; taking
  the connections over would need one uplink MAC for both sets, a NIC
  connected at run time and conntrack's state handed over.
- **should, 6b:** an update with `install.ps1` fails while an `hlab ssh`
  runs: `hlab.exe` waits for `ssh.exe` and holds its file, so the copy
  fails. hlabd has ended by then and its servers run on; the install
  works again once the session ends.
- **should, 2a:** a Primary IP's assign and unassign have no `Undo`: when
  their pictures fail, the action ends with an error but the IP stays
  assigned or unassigned.
- **must, 2a-1:** a server whose create fails stays in the appliance's
  pictures: its steps send them while the record is still there, and the
  undo that removes it (and its Primary IPs with auto_delete) sends none.
  Solved together with the missing `Undo` of a Primary IP's assign and
  unassign: after an undo, the pictures go out again.
- **should, 2c:** `TestQueuedActionsWaitBehindAnActionThatLocksTheirResource`
  checks that an action still waits with `time.Sleep(20ms)`
  (`internal/lab/actions_test.go`), so it can pass by mistake; a step
  that signals when it starts would make them exact.
- **should, with the next change there:** `sshFingerprint`
  (`internal/api/cloud/ssh_keys.go`) reports a failure as a second string
  instead of an `error`; the key types are written out twice
  (`sshKeyInvalid`, `sshKeyTypes`); `registerSSHKeys` holds every handler
  as a closure, over 80 lines.
- **should, 2c:** DEVELOPING.md does not say that the lab control's and
  `web`'s own tests send requests without `spectest` (there is no Hetzner
  spec for them); it uses words it does not explain (NRPT, hv_sock, VRF,
  conntrack, watchdog, room check); "Transactions" needs one plain
  sentence first: a Tx changes only its copies, a state never changes.
- **should, 2c:** DEVELOPING.md's layer table lists `schema` under
  `api/cloud` as HLab's own package, which does not exist yet, without
  saying that `api/cloud` uses `hcloud-go`'s until then; the table of
  flags is hard to map to `planAction` and `start`, and `Lab.start` (flags,
  queue, lock queues) wants either a worked example in "Actions" or a
  split in two.
- **should, 6b:** the README describes the installer as there, while the
  roadmap lists 6b (tray, installer) as open; say what 6b still lacks, and
  that `bin/dev package` copies `install/install.ps1` to `build/package`.
- **should, 2a:** a server's create accepts any location of the catalog,
  also one its server type does not offer; Hetzner refuses that (text and
  code not measured yet).
- **should, with the next change there:** `Lab.DeleteProject` sends every
  picture once per server it removes (`deleteServerVM`); removing the VMs
  first and sending the pictures once would do.
- **should, 6b:** after the first Ctrl+C or stop signal, a second one does
  not end a stop that hangs (`signal.Notify` stays active in both
  platforms' life cycles); `signal.Stop` after the first would let it.
- **should, with the next change there:** the comment on the requests that
  `refuseWhileStopping` lets through (`cmd/hlabd/service.go`) does not
  read.
- **should, 2c:** the servers' watchdog runs every 2 s on the wall clock
  (`serverWatchInterval`, `time.NewTicker`, not the lab's clock), so its
  unit tests wait seconds.
- **should, with each timing's phase:** `internal/lab/clock.go` holds
  timings of later phases and `ServerCreateStart`, which nothing uses (the
  start after a create waits `PowerOn`); not every timing says when it
  was measured.
- **2a:** the metadata summary says `private-networks: []` on a server
  without a network, where Hetzner leaves it out.
- The firewall rules do not block the lab's own range as a source on
  8090; the appliance's ruleset keeps the servers off it.
- **should, 2a:** a connection from a server to a lab address that nobody
  holds runs into a timeout (4 s), though the ruleset's comment says it is
  rejected; 2a makes the behaviour and the comment agree, as Hetzner does
  it.

### Comes with phase X

- **2a:** volumes, networks, firewalls and placement groups in a server's
  create request are ignored until then.
- **2b:** `change_dns_ptr` (DNS); unbound's configuration and the uplink's
  unreachable route name the default ranges, and the ranges become
  settings with a picture.
- **2c:** the injected fault's delay runs on the wall clock, not the lab's
  time scale.
- **3a:** `PUT` and `DELETE` of images.
- **4:** tests of the ruleset's counters for traffic between servers (the
  agent's counters).
- **5:** `boxes.vhdx` moves to the new appliance at an update.
- **6b:** stop requests from signing out and the tray; the tray replaces
  the start menu entry's window, whose closing ends hlabd as a crash
  would.

### Notes

- A start of the dev loop takes 20 s: 9 s for the host link's switches, 11 s
  for the appliance from nothing.
- The pictures' generations stay in memory, so a start sends every picture
  again.
- The uninstall is played through by hand, not in a live test.

## Phase 2a

### Steps

| Step | What | State |
|---|---|---|
| 2a-0 | `lab.Machine` for the machine's side; the rule where an action's plans and steps live (DEVELOPING.md, Actions); `ReadSecrets` takes a file of `null`; the watchdog's notes live only for a round | done |

Next: the next step of phase 2a.

Not there yet: the commands of later phases.
