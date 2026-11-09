# Roadmap

HLab is rebuilt in Go. Phases 1 to 7 target Windows 11 with Hyper-V, used from WSL2;
Linux follows in phase 8. Each step ends with its state recorded here, so the next
one starts from this file. How a step got there is in its commit messages.

The earlier Python implementation (Docker, QEMU) is on the branch `legacy`, tagged
`v1-python`. It is the reference for behaviour the rebuild takes over: Hetzner's
answers, the rules for actions and flags, the state and its migrations,
the tests. Look there before writing such a part anew.

## Phases

| Phase | What | State |
|---|---|---|
| 1a | core without VMs: state, lab, web basics, catalog, reading resources, SSH keys, lab control, `--platform fake` | done |
| 1b | servers, host link, reachability | done |
| 2a | Floating IPs, firewalls, private networks | done |
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

## Now

Phase 2a is done with its review, 2a-R. Next is phase 2b: first 2b-0,
which measures against the real API what 2b and the open points below
need, then "disk ID, volumes, placement groups" and "DNS, rDNS, host DNS",
then 2b-R.

## Open

### Known gaps

Faults and deviations from Hetzner, each as "level, phase: finding": the
level from the review that found it (must, should, optional; accepted
for what the operator keeps), the phase or step that closes it.

- **must, 2b-0:** not measured on Hetzner, built as assumed: whether
  `delete_subnet` refuses a subnet that holds only a member's alias IPs
  (`planDeleteSubnet` looks at the members' own addresses); whether a
  server's attach while the network runs an `add_subnet` is 423 (the lab
  runs it, in the server's queue); a server's actions sent while a
  Primary IP's assign locks it wait behind it (DEVELOPING.md, Actions);
  whether `delete_subnet` with an `ip_range` that is not the subnet's
  first address (`10.0.1.5/24`) finds the subnet (the lab compares the
  text as given).
- **should, 2b (own step):** a NIC that Hyper-V fails to remove (a
  detach, a network's delete) stays in the VM without a record; the VLAN
  pool's rotation keeps it out of new networks for a while. A check that
  compares a VM's NICs with the record closes this and any other such
  rest (new platform method, contract test).
- **accepted:** the servers' connections to the Internet break at
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
- **should, 2c:** `sshFingerprint`
  (`internal/api/cloud/ssh_keys.go`) reports a failure as a second string
  instead of an `error`; the key types are written out twice
  (`sshKeyInvalid`, `sshKeyTypes`); `registerSSHKeys` holds every handler
  as a closure, over 80 lines.
- **should, 6b:** after the first Ctrl+C or stop signal, a second one does
  not end a stop that hangs (`signal.Notify` stays active in both
  platforms' life cycles); `signal.Stop` after the first would let it.
- **optional, 2b (with volumes):** a type `holds` that takes and frees an
  action's flags, queue and lock queues in one place (`Lab.start`,
  `Lab.releaseQueues`, `Lab.end`); volumes bring more `Locks`.
- **optional, 2c:** one step
  `Machine.SendPictures` for the actions that only send pictures
  (`ChangeFirewall`, `ChangeNetwork`, `ChangeAliasIPs`), at the cost of
  their names; `planSteps` and `planStop` build the same closure.
- **optional, 8:** sync the data folder after `state.json`'s rename
  (`state.Writer`), so that the rename outlives a power cut on Linux.
- **optional, 2b (with `holds`):** `Lab.Create` and `run` take a small
  struct instead of two functions that may be nil; the section Actions
  moves from DEVELOPING.md to `docs/actions.md`.
- **should, 2c:** one helper for the tests' waiting loops (a deadline and
  a sleep, written out a dozen times in `internal/api/cloud` and
  `internal/lab`).
- **should, 6b:** `pshost.Host.give` reads `closed` under the mutex but
  puts the process into `idle` after it: a `Close` in between leaves that
  process running; harmless today, as `Close` comes after the lab's.
- **optional, 2c:** a random ID per start of hlabd in the pictures'
  header, so that an old generation never equals a new one.
- **accepted, 2a-4:** 50
  firewalls per project, where Hetzner allows 50 per account; a
  `remove_from_resources` of the server ID 1 is 404 as for any missing
  server, where Hetzner answers 422 "value required to be larger"; a MAC
  of digits only is quoted in the metadata's YAML; the metadata's
  `private-networks` sorted by `interface_num`; `DELETE` and `PATCH` on
  an action and a selector's per-server actions on a new selector, built
  as assumed (not measured).
- **note:** the firewall rules do not block the lab's own range as a
  source on 8090; the appliance's ruleset keeps the servers off it.

### Comes with phase X

- **2b:** volumes and placement groups in a server's create request are
  ignored until then.
- **2b:** `change_dns_ptr` (DNS); unbound's configuration and the uplink's
  unreachable route name the default ranges, and the ranges become
  settings with a picture.
- **2c:** an injected fault's delay is the user's parameter (`delay_ms`),
  no Hetzner time, and runs on the wall clock as set; the lab has no
  time scale any more. Only its test waits for real.
- **3a:** `PUT` and `DELETE` of images.
- **4:** tests of the ruleset's counters for traffic between servers (the
  agent's counters).
- **5:** `boxes.vhdx` moves to the new appliance at an update.
- **6b:** stop requests from signing out and the tray; the tray replaces
  the start menu entry's window, whose closing ends hlabd as a crash
  would.

## Phase 1a

| Step | What | State |
|---|---|---|
| 1a-1 | repo skeleton: Go module, minimal commands, layer test, `bin/dev`, `bin/dev.ps1`, CI, docs | done |
| 1a-2 | `internal/state` and `internal/lab` with tests: transactions, saving, migrations, actions with a test clock (gone since 2a-5); platform interfaces, fake, contract tests, Windows stubs | done |
| 1a-3 | `internal/web` and `internal/spectest`; projects, tokens | done |
| 1a-4 | catalog and the reading resources; SSH keys | done |
| 1a-5 | lab control, CLI, `hlab env`; web UI skeleton | done |
| 1a-6 | checks against the real API | done |
| 1a-R | review of phase 1a, smoke tests, CI on every push | done |

### What is there

- `hlabd --platform fake` runs on Linux, WSL2 and Windows: `hlab.yaml` (ports), the user's `catalog.yaml`, the project `default` in a new lab, the
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
  Hetzner's statuses and queue (a power action during another waits
  instead of 423). A create records the server and plans its actions in one
  transaction (`Lab.Create`); a start reserves its room in its plan, so
  starts sent at once cannot overcommit the host's memory (412
  `resource_unavailable`); a create also needs a tenth of the data folder's
  drive free; the reserves are settings (`room` in `hlab.yaml`). A rename
  answers at once and sends the pictures in the background.
  `want_running` changes only in the actions' plans, so queued actions
  keep the order they were sent in. A failed create removes its VM
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
| 2a-0 | `lab.Machine`, where an action's parts live | done |
| 2a-1 | Floating IPs | done |
| 2a-2 | firewalls | done |
| 2a-3 | private networks | done |
| 2a-4 | networks and firewalls in a create, metadata, state version 2 | done |
| 2a-5 | no made-up times | done |
| 2a-6 | IPv4 by DHCP for a server with IPv6 only | done |
| 2a-R | review of phase 2a | done |

### What each step brought

- **2a-0:** `lab.Machine` for the machine's side; the rule where an action's plans and steps live (DEVELOPING.md, Actions); `ReadSecrets` takes a file of `null`; the watchdog's notes live only for a round.
- **2a-1:** Floating IPs (IPv4, IPv6 `/64`) with Hetzner's actions, routes in the `links` picture; new pictures after every undo (a failed server create, a Primary IP's assign and unassign); a server's create checks its location against its type; `l.read()`, `planFunc`.
- **2a-5:** no made-up times: actions end as soon as their work is done; `clock.go`, `Lab.Sleep`, `Lab.AddNote`, `Lab.RecordActionLater` and `time_scale` are gone (2a-R removed the key: an `hlab.yaml` that still sets it does not load); a selector's `apply_firewall` or `remove_firewall` is recorded finished at once (`Lab.RecordAction`); statuses such as `pending` and `attaching` show only while an action's work runs; `bin/dev itest` no longer ends with 141 when the output grows.
- **2a-6:** a server with IPv6 only gets a service address of `100.64.0.0/10` by DHCP, with Hetzner's routes (options 121 and 249) to `172.31.1.1`, the metadata service and `188.40.24.200`, and reaches the metadata service with it, nothing else; `network-config` with `dhcp` for IPv4; a private network's DHCP names its gateway as the server (the agent adds addresses in the picture's order); the agent's tests stop their dnsmasq.
- **2a-2 to 2a-4:** the sections below.
- **2a-R:** review of phase 2a: a failed action of a queued table undoes only if nothing was queued behind it (`UndoIfLast` on the networks' rows, a server's detach and `change_alias_ips`), and a detach takes its NIC out of the VM even when the pictures fail (no undo); a failed `add_subnet` keeps a subnet a server took an address in meanwhile; a network's delete removes the NICs only after the actions it aborted ended, and an aborted detach still takes its NIC out; an action aborted while it waits in a queue keeps the queue's order, and a waiting detach still takes its NIC out (`Plan.RunIfCanceled`); a failed `change_alias_ips` gives back its old alias IPs only if all are still free; why the firewalls' rows have no undo; a project's delete sends the pictures once, also without servers; an appliance that a start made anew and that then fails is shut down; a failed start leaves the server's status to the watchdog; a start of the lab turns off a server's VM that runs although the server does not want to run (hlabd ended during its poweroff); a network's delete removes its NICs only after the members' running actions, such as a create into it; the servers' watchdog interval is an option (`MachineOptions.ServerWatchInterval`), so the unit tests take it short; a network's members' statuses come from one pass over the actions; `time_scale` is gone from `hlab.yaml` (a file that sets it does not load); the docs after 2a-5 (no times left, a glossary, a table of a plan's fields, this file's "Now" and "Open").

### What 2a-4 brought

- **A server's create** takes `networks` and `firewalls` as Hetzner does,
  measured on 2026-11-09 (`compat/requirements.md`, 2a-4): its checks and
  texts, `next_actions` with an `attach_to_network` per network and an
  `apply_firewall` per firewall before the start, the answer's server in
  its networks with its firewalls `pending`; the VM has its NICs and the
  firewalls hold from the first boot. Floating IPs have no field in the
  create, also on Hetzner. `Lab.Create` has an `answer` that reads the
  records in its transaction, and a row may run on another resource's
  table (`Row.Table`).
- **The metadata:** `private-networks` with Hetzner's keys, `[]` without a
  network, never in the summary; a server without public addresses
  reaches the service through its network: a DHCP route to
  `169.254.169.254`, the service in each VRF (`tcp_l3mdev_accept`), no
  public NIC in its `network-config`. The seed's script waits for the
  private leases, so that the user-data's commands find them.
- **Firewalls:** `pending` while an apply or remove that names the server
  runs, also a remove; a label selector that takes a server or lets it go
  records an `apply_firewall` or `remove_firewall` (2a-5 made it end at
  once); 50 firewalls per
  project; a remove of a server the firewall does not reach answers
  Hetzner's text.
- **Unknown actions** answer per resource as Hetzner does: another
  method than GET 405 without a body for networks, firewalls, Floating
  IPs and Primary IPs, 404 for servers; a GET of a missing action, or of
  an ID that is no number, each resource's own text; a GET of a
  resource whose ID is no number too (`API.AnswerInvalidID`).
- **The state** is version 2: a private NIC keeps its number
  (`interface_num`) while others come and go, as on Hetzner; the
  migration numbers older NICs in their order.
- **Floating IPs:** a move records the `unassign_floating_ip` of the old
  server, as Hetzner does.
- **Networks:** `add_subnet` without a range takes a smaller free block
  when no `/24` is free; a delete while an action runs on it, a server's
  attach among them, aborts it (`Lab.ChangeAborting`); other members show
  `updating` while the action runs (not measured; 2a-5 removed the 10 s).
- **Smoke tests:** `examples/tofu-2a` (network, subnet, firewall, Floating
  IP, two servers) and the hcloud CLI's commands of 2a.
- **Measured live:** both servers of the new live test joined their
  network at the create; the one without public addresses read its
  metadata through the network.
- The appliance changed (the agent, the ruleset): `bin/dev appliance`
  before `bin/dev itest`; an installed HLab updates its appliance at the
  next start. `state.json` moves to version 2: an older hlabd refuses it
  (`state.json.v1` stays).

### What 2a-3 brought

- **Networks** (create, get, update, delete, lists, members, action lists;
  `add_subnet`, `delete_subnet`, `add_route`, `delete_route`,
  `change_ip_range`, `change_protection`) and a server's
  `attach_to_network`, `detach_from_network` and `change_alias_ips`,
  measured on 2026-11-08 (`compat/requirements.md`, Private networks):
  50 subnets and 100 routes per network, 3 networks and 5 alias IPs per
  server, Hetzner's order of checks, the address from the last subnet,
  a network's actions queued, a network deleted with servers in it, the
  server's network actions only among the network's. The members show
  `attaching` while an attach runs and `updating` while alias IPs change.
- **On Hyper-V** the switch `HLab-Private` (private) carries a VLAN per
  network: the appliance's `private` NIC is a trunk, a server's NIC an
  access port, hot-plugged into a running VM. A server's private MAC is
  `86:00:` and a counter.
- **In the appliance** each network is a VLAN `private.<vlan>` in a VRF
  `vrf-net-<id>` with the gateway; only the addresses handed out and the
  network's routes are routed, the rest is dropped silently. A dnsmasq
  instance per network (`hlab-dnsmasq@<device>`) gives Hetzner's lease:
  a `/32`, options 121 and 249, MTU 1450, a day, no router, no name
  servers. Two networks with the same range stay apart; no firewall
  touches a private network.
- **The public lease** lasts a day, as Hetzner's (before: infinite).
- **In the guest** the seed's vendor-data does what Hetzner's hc-utils
  does: a udev rule starts dhcpcd on each private NIC, also a hot-plugged
  one. Deviations, named in docs/servers.md: that script, the NIC names
  `eth1` to `eth3`, a direct way between two guests of a VLAN if set up by
  hand.
- **Measured live:** the attach's address is in a running guest 1.4 to
  3.5 s after the attach was sent (Hetzner: 7.8 s), and a ping between two
  servers gets through after 6.4 to 7.4 s.
- The appliance changed (agent, units): `bin/dev appliance` before `bin/dev
  itest`; an installed HLab updates its appliance at the next start and
  creates the switch `HLab-Private`.

### What 2a-2 brought

- **Firewalls** (create, get, update, delete, lists, `set_rules`,
  `apply_to_resources`, `remove_from_resources`, action lists), measured
  on 2026-11-08 (`compat/requirements.md`): the rules in Hetzner's form,
  500 effective rules, 5 firewalls per server (also through selectors),
  label selectors that follow new and relabelled servers, `pending` and
  `applied` in a server's `public_net.firewalls`, a firewall applied only
  through selectors can be deleted.
- **In the data plane** a firewall filters a server's Primary and
  Floating IPs, also between servers of a project; answers pass without
  conntrack (TCP with ACK and without SYN, as in the fixed part). The
  lab's endpoints and the host's APIs pass any firewall: a deviation for
  the API, named in docs/servers.md.
- **An address nobody holds** answers nothing (Hetzner: no answer to ping
  or TCP); before, the ruleset's comment said it was rejected.
- **Measured live:** a new connection gets through 0.4 s after a
  `set_rules` that opens its port.
- The appliance changed (the agent sets blackhole routes): `bin/dev
  appliance` before `bin/dev itest`; an installed HLab updates its
  appliance at the next start.

Not there yet: the commands of later phases.
