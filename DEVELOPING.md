# Developing HLab

How the code fits together, and what to touch for the usual changes. Style rules are in
[STYLE.md](STYLE.md); `bin/hlab test` checks them and runs the unit tests.

## How a request flows

```text
HTTP request
  → hlabd/daemon.py         one server; the address picks the app (Cloud API,
                            Storage Box API, metadata service); every request is logged
  → api/web.py              auth, rate limit, injected faults, OpenAPI request check
  → api/<resource>.py       route → module function `f(lab, ...)`: check the request,
                            change the state (on the event loop), start an action
  → lab.py                  `act()` runs an action from the resource's table; the slow
                            part runs in the background; flags stop a second action
                            on the same resource
  → fabric/<part>.py        VMs, taps, routes, nftables, Storage Box accounts
  → scripts/hlab.sh <verb>  the only place that touches the system (box.sh for the box)
```

Every answer is checked against Hetzner's OpenAPI specification in the unit tests
(`tests/spec_check.py`), so a field that is missing or wrongly typed fails a test.

## The pieces

The repository:

| Path | What |
|---|---|
| `Dockerfile` | the lab image (Debian 13 packages: QEMU, dnsmasq, nftables, OpenSSH, Python/FastAPI; the live tests) and the development image on top (linters, the unit tests) |
| `bin/hlab` | host side (Bash: Linux, macOS, Git Bash or WSL on Windows): build, start, stop, pass commands into the container |
| `scripts/hlab.sh` | low-level primitives: taps, QEMU, nftables, tc, DHCP, DNS, volumes, images |
| `scripts/hlab.nft` | the lab's nftables table `inet hlab`; `hlab.sh init-net` puts the addresses in and loads it |
| `scripts/box.sh` | the Storage Box: sshd, chrooted accounts, snapshots |
| `hlabd/` | the lab's daemon (below) |
| `tests/` | unit tests by feature, the OpenTofu smoke test |
| `itests/` | live tests with VMs |
| `tools/lint.sh` | the style and type checks of [STYLE.md](STYLE.md) |
| `tools/fetch_specs.py` | downloads Hetzner's OpenAPI specs (the image build, the unit tests outside Docker) |

Inside `hlabd/`:

| Module | Job |
|---|---|
| `lab.py` | state, actions (`Plan`, `act`, `run_action`), the flags that stop two actions on one resource (`FLAGS`) |
| `state.py` | the state as JSON in the volume; resources by kind, plus the lab's own records (its docstring); its owner check, `change()`, the thread that writes it |
| `records.py` | the fields of every resource, the lab's own included (a server's `_lab`, its NICs): one TypedDict per kind, checked by mypy |
| `api/hetzner.py` | the JSON shapes of the Cloud API (`<thing>_json(lab, thing)`) |
| `api/<resource>.py` | one resource each: checks, its action table, routes |
| `api/nics.py` | a server's NICs in cloud networks: attach, detach, alias IPs (server actions, in `SERVER_ACTIONS`) |
| `api/public_addresses.py` | what primary and Floating IPs share: free addresses, reverse DNS |
| `api/storagebox/` | the Storage Box API (its own app, its own JSON shapes) |
| `api/restart.py` | what happens after the lab (re)starts: flags cleared, servers back |
| `api/control.py` | lab control under `/_lab/` (faults, clients, reset); no resource module imports it |
| `api/faults.py` | the faults and netem in the state, dropped with the server, client or link they point at |
| `api/desired.py` | pure functions: the state in, plain data out – what the system should look like (fault sets, latency, DHCP, taps, routes, firewall rules per server, boot volumes) |
| `api/system.py` | puts the state in place: reads it on the loop (through `desired.py`), hands the plain data to the fabric in a thread, stores what the fabric reports back (`net_routes`); whole pictures in the order they were computed (`lab.picture_lock`); a VM's start and stop across the parts (`start_vm`, `kill_vm`, `delete_vm`) |
| `fabric/` | carries out, with plain data as arguments; it never sees the state (records handed in are read only for what never changes: ID, name, tap, MAC). `Fabric` (`__init__.py`) holds one object per part, each given what it uses in its constructor: `shell` (`shell.py`, runs hlab.sh), `vms` (`vm.py`), `volumes` (`volumes.py`), `faults` (`faults.py`: nft sets, latency), `network` (`network.py`: taps, DHCP, DNS, routes, Floating IPs, clients), `firewall` (`firewall.py`: the firewall table), `box` (`storagebox.py`); `addresses.py` has the address rules (MACs, gateways, tables) |
| `config.py` | configuration defaults and the lab's addresses (the scripts get them as `HLAB_*`) |
| `daemon.py`, `cli.py` | the container's main process; the `hlab` command inside the container |

## Actions

Every resource lists its actions in a table, `{command: (Hetzner's name, function)}`.
The tables are `SERVER_ACTIONS` (`api/servers.py`; the network actions come from
`api/nics.py`), `VOLUME_ACTIONS`, `PRIMARY_IP_ACTIONS`, `FLOATING_IP_ACTIONS` and
`NETWORK_ACTIONS`, each in its resource's module, and `BOX_ACTIONS` and
`SUBACCOUNT_ACTIONS` in `api/storagebox/`.

A function is named after its command (`reboot`, `add_subnet`). If two resources share
a command, the resource goes in front (`primary_ip_assign`, `floating_ip_assign`). The
function gets `(lab, resource, body)`. It checks the request and returns a `lab.Plan`.
It is `def`, or `async def` when it must ask the system before it decides (a script
through `await lab.blocking(...)`: `reset_password` asks whether the VM runs); `act()`
awaits it then. The plan has:

- `steps`: an async function that does the slow part through `lab.fabric`. `None`
  means the change is done already, so the action finishes at once.
- `extra`: fields the answer adds, such as `root_password`.
- `server`: the server the steps work on. It stays locked while they run. If they
  fail, it leaves its in-between status.
- `resources`: further resources the action concerns.

`lab.act()` runs every table the same way, so an action function never sets a flag
itself. Two actions only change the record (`change_protection`, `change_dns_ptr`);
they take no flag. The delete protection of networks, primary IPs, Floating IPs,
volumes and Storage Boxes is one function: `change_delete_protection` in `api/web.py`.

Firewalls are the one exception. `FIREWALL_ACTIONS` (`api/firewalls.py`) maps each
command straight to a function, and `act()` does not run it. The function returns
Hetzner's finished actions itself, several at once, as on Hetzner.

### How flags flow

A flag on a resource says that an action works on it; a second action gets 423. The
flags are listed next to `FLAGS` in `lab.py`. Code holds them only through
`lab.holding()`, which `act()` uses too:

```text
request
  → act() or lab.holding(): held.claim(resource, flag)   423 if the flag is set already
  → the plan is made (in act(): the table's function)
      ├─ refused (ApiError) or failed → the holding block ends: flags off
      ├─ done at once (steps is None) → act() saves, held.release(): flags off,
      │                                 then answers with the finished action
      └─ steps                        → run_action(..., holding=held) takes the flags;
                                        the answer goes out, the action running;
                                        the steps run in the background (lab.spawn)
                                        → when they end (success, error, cancelled),
                                          run_action takes the flags off, then
                                          finishes the action
```

`act()` claims the resource's flag, and the `locked` flag of the plan's server if that
is another resource. New code uses `act()`. Some paths are no Hetzner action on an
existing resource: creating and deleting a server, creating a volume for a server,
deleting a volume, a network or a Storage Box, creating a sub-account, and the VM
watcher. They hold their flags through `lab.holding()` themselves:

```python
with lab.holding() as held:
    held.claim(server, "locked", SERVER_LOCKED)  # 423 if it is set already
    ...  # refused or failed: the flags come off
    lab.run_action(action, steps, holding=held)  # the flags stay on until the steps end
```

`held.release()` takes the flags off early. `held.keep()` leaves them on a record that
is gone (a deleted volume). No code outside `lab.py` calls `lab.claim()` or
`lab.release()`.

### Example: powering off a server

`POST /v1/servers/{id}/actions/poweroff`, from the request to the finished action:

1. `api/web.py` checks auth and the rate limit, and the request against the spec.
2. The route `server_action` (`api/servers.py`, `add_routes`) is `async def`, like
   every route: it awaits the body (`read_body`), then `run_server_action`.
3. `run_server_action` looks the server up (`lab.server_or_404`: 404) and awaits
   `lab.act(SERVER_ACTIONS, "poweroff", server, "server", body, "locked",
   SERVER_LOCKED)`.
4. `act()` finds `("stop_server", poweroff)` in the table (else `unsupported`), claims
   the server's `locked` flag (423 if another action runs) and calls
   `poweroff(lab, server, body)`. That returns `Plan(steps, server=server)`.
5. The plan has steps, so `act()` creates the action (`lab.new_action("stop_server",
   ...)`) and hands steps and flag to `lab.run_action`. It starts the steps as a task
   on the loop (`lab.spawn`). The route answers 201, the action `running`.
6. The steps run `await system.kill_vm(lab, server)`. It runs `fabric.vms.kill`
   (`hlab.sh kill-vm`) in a thread (`await lab.blocking(...)`), reads the server's taps
   and hands them to `fabric.network.taps_down` (`hlab.sh tear-down-tap` per tap), then
   writes the DHCP configs (`system.write_dhcp`). The steps set the status to
   `stopping` and, after `POWEROFF_SECONDS`, to `off` (`lab.set_status`, which saves),
   then wait `POWEROFF_END_SECONDS`.
7. When the steps end, `run_action` takes the `locked` flag off and finishes the action
   (`lab.finish`: `success`, or `error` with the message). If the steps failed, the
   server leaves `stopping` for what its VM really does.

## Where code runs

- Everything that reads or changes the state runs on the event loop: every route is
  `async def`, and so are the steps of actions (`lab.spawn`), the VM watcher, the
  snapshot plans and the restart. FastAPI's worker threads are not used.
- Blocking work (scripts, files, guest agents) runs in a thread through
  `await lab.blocking(work, *args)`, so the API keeps answering. `work` gets plain
  data and returns plain data, never the state: the fabric's methods, which get what
  `api/desired.py` computed, or a record only for fields that never change (ID, tap,
  MAC; `VMs.launch` and `VMs.create_disk` get the server type and the disk size as
  arguments). A thread cannot be cancelled, so `lab.blocking` keeps track of its
  threads, and a lab reset waits for them after cancelling the actions
  (`lab.stop_actions`).
- A whole picture of the system (fault sets, latency, firewall, DHCP, routes) is
  computed and handed over while `lab.picture_lock` (an `asyncio.Lock`) is held, so
  pictures are put in place in the order they were computed and an older one never
  overwrites a newer one (`api/system.py`). A Floating IP's route goes through it too
  (`system.route_floating_ip`). Inside the fabric, `net_lock` (a thread lock) keeps
  its threads apart: taps change only under it, so tc and nft find the taps they
  checked.
- The waits of actions are Hetzner's measured timings, one table in `lab.py`
  (`POWEROFF_SECONDS`, ...), scaled by `time_scale` in `lab.sleep()`.

## State

This is the one full explanation; `lab.py`, `state.py` and CLAUDE.md point here.

**One owner.** The event loop owns the state: everything reachable from `state.data`
(records, flags, statuses, the lab's own records) is read and changed only on the
loop's thread. So two pieces of code never change it at the same time, and between two
awaits nothing else runs: a check and the change after it fit together, a loop over
the state sees no change in the middle. `state.check_owner()` turns this into a check:
`state[kind]`, `state.data`, `change()`, `save()` and `new_id()` raise AssertionError
on another thread. The daemon makes its loop the owner at start (`lab.own_state()`).
The unit tests record every breach, also in background steps where the error only
fails the action, and fail the test (`state_owner_kept` in `tests/conftest.py`).

**Changes.** A change that must be saved is a `with lab.state.change():` block, a
transaction: if the block raises, the state is put back as it was (every dict and
list keeps its identity, so records held elsewhere stay valid), and nothing is saved;
if it succeeds, the state is saved. Nested blocks save once, with the outer one.

```python
with lab.state.change():
    check_unique_name(lab.state["volumes"].values(), name, volume, "volume")
    volume["name"] = name
```

The block must not await: it is a plain `with`, and another task would see the change
half done (`check_owner` refuses a task's access while another task's block is open;
`tests/test_state.py` also checks that no block in `hlabd/` awaits). Check first, then
change still reads best. A change outside a block (a flag, a plan's change that is
done at once) is saved by the next save: `act()` saves it, and so do
`lab.set_status()` and `lab.finish()`.

**Saving.** `state.save()` serializes the state on the loop (JSON, quick at the lab's
size; finished actions are capped) and hands it to a writer thread, which writes the
file atomically and fsyncs it. If saves come faster than the disk, only the newest is
written. `state.flush()` waits until everything saved is on disk: the daemon flushes
when it stops, a lab reset before it answers, and the unit tests before they read the
file.

**Awaits.** Across an await other tasks run: what was read before may have changed.
Flags (`lab.holding()`) keep other actions off a resource across awaits; a loop that
awaits per item (a script per server) loops over a copy (`list(...)`) and checks again
after the await where it matters (`stop_if_ended`, `make_available`).

## Recipes

**A new action**: write a function named after the command (see Actions), `(lab,
resource, body) -> Plan`, next to the others in the resource's module, and add it to
the table with Hetzner's name. If it gives the server a new in-between status (like
`rebuilding`), teach `lab.py` (`IN_BETWEEN`, for failures) and `api/restart.py`
(`status_after_restart`) what that status means.

**A new field on a resource**: add it to the resource's TypedDict (`records.py`), where
the resource is created (`create_*`), to its JSON shape (`api/hetzner.py`, or the
Storage Box modules), and to its PUT keys if it can change. The spec check in the unit
tests tells you if the shape is wrong.

**Something new on the system** (a tap, a route, a file): add a function with a header
comment to `scripts/hlab.sh` and its verb to `main` (the function's name with dashes:
`create_volume` is `create-volume`), call it from a method of the fitting `fabric/`
part, and call that method from the API. Addresses come from `config.py` as `HLAB_*`.
What the method needs from the state comes as arguments: a pure function in
`api/desired.py` computes it, `api/system.py` (or the caller) reads it on the loop and
passes it to the method in a thread (`await lab.blocking(...)`).

**A new flag**: add it to `FLAGS` in `lab.py` and to the list above it, so a restart
clears it (`api/restart.py`).

## The packet path of a public address

Who may send what, and where an address goes, comes from four places, each rebuilt as a
whole from the state when something changes (computed by `api/desired.py`, handed over
by `api/system.py`):

- routes (`fabric/network.py`: `route_floating_ip`; `system.reroute_floating_ips`;
  `hlab.sh set-route`),
- the nft sets of `table inet hlab` (`system.apply_faults` → `Faults.apply` – faults,
  the sources each tap may send from, cloud network members),
- the firewall table `inet hlabfw` (`system.apply_firewall` → `Firewall.apply`, the
  table built by `ruleset` in `fabric/firewall.py`),
- tc on the taps (`system.apply_latency` → `Faults.apply_latency` – latency between
  locations, netem faults).

So moving a Floating IP changes its route and calls `system.apply_faults` (the new
server may send from it) and `system.apply_latency`.

## Tests

- `tests/`: unit tests, no VMs. `FakeShell` (`tests/conftest.py`) stands in for
  `hlab.sh` and records every call; `time_scale = 0` skips Hetzner's waits.
  The `api` fixture is a test client of the Cloud API; it runs the app on a loop of its
  own, which owns the state, so a test reads or changes the state there:
  `api.on_loop(work, *args)` (`work` may be async) and `state_of(api, "servers")`. `bin/hlab test` is the
  reference: it runs all of them in the development image, which carries the unit
  tests (the lab image has only `itests/`).
- `tests/test_wrappers.py`: every command `bin/hlab` dispatches is in its usage comment
  and back (both read as text); `bin/hlab` reads the lab name from sample
  configurations.
- `tests/test_scripts.py`: the scripts themselves, in seconds and without VMs: every
  verb hlabd calls exists in `hlab.sh`/`box.sh` with a fitting argument count (both
  sides parsed) and is its function's name with dashes, the nftables rulesets parse
  (`hlab.sh init-net`'s, rendered from `hlab.nft`, the faults' sets, the firewall
  table), `box-shell.sh`'s allow/deny decisions with stubbed commands,
  and `lib.sh`'s `stop_pid`/`is_alive`. `nft -c` checks fully only with
  CAP_NET_ADMIN, which `bin/hlab test` gives the test container; without it only nft's
  parser runs and the full check is skipped. Without bash, nft or /proc (`lib.sh`)
  the tests that need them skip and say why.
- `itests/`: live tests with VMs in a running lab (`bin/hlab itest`, ~15 min).
- `tests/tofu-smoke/run.sh`: OpenTofu with Hetzner's provider against a running lab.

The unit tests also run without Docker, on Linux or macOS with Python 3.13 or newer
(from the repository's root):

```bash
python3 -m venv .venv && .venv/bin/pip install -e '.[dev]'
python3 tools/fetch_specs.py   # Hetzner's specs into .cache/spec/
.venv/bin/pytest -q
```

The tests find the specs in `$HLAB_SPEC_DIR`, else in the image's `/opt/hlab/spec`, else
in `.cache/spec/`. Here the runtime comes from PyPI (the image has Debian's packages),
and the tests that need bash, nft, CAP_NET_ADMIN or /proc skip where those are missing
(macOS: the nftables and `lib.sh` tests); `bin/hlab test` runs everything.
