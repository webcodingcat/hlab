# Developing HLab

This file explains how the code fits together and what to change for the usual tasks.
The style rules are in [STYLE.md](STYLE.md). `bin/hlab test` checks them and runs the
unit tests.

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
  → scripts/hlab.sh <verb>  the only place that changes the system (box.sh for the box)
```

The unit tests check every answer against Hetzner's OpenAPI specification
(`tests/spec_check.py`). A field that is missing or has the wrong type fails a test.

## The pieces

The repository:

| Path | What |
|---|---|
| `Dockerfile` | the lab image (Debian 13 packages: QEMU, dnsmasq, nftables, OpenSSH, Python/FastAPI; the live tests) and the development image on top of it (linters, unit tests) |
| `bin/hlab` | the host side, in Bash (Linux, macOS, Git Bash or WSL on Windows): build, start, stop, pass commands into the container |
| `scripts/hlab.sh` | low-level commands: taps, QEMU, nftables, tc, DHCP, DNS, volumes, images |
| `scripts/hlab.nft` | the nftables table `inet hlab`; `hlab.sh init-net` fills in the addresses and loads it |
| `scripts/box.sh` | the Storage Box: sshd, chrooted accounts, snapshots |
| `hlabd/` | the daemon (below) |
| `tests/` | unit tests, one file per feature, and the OpenTofu smoke test |
| `itests/` | live tests with VMs |
| `tools/lint.sh` | the style and type checks of [STYLE.md](STYLE.md) |
| `tools/fetch_specs.py` | downloads Hetzner's OpenAPI specs (for the image build and for unit tests outside Docker) |

Inside `hlabd/`:

| Module | Job |
|---|---|
| `lab.py` | the core: actions (`Plan`, `act`, `run_action`), flags, Hetzner's timings, `blocking` for threads |
| `state.py` | the state, saved as JSON in the volume: resources by kind and the records only the lab uses (see its docstring); the owner check, `change()`, the writer thread |
| `records.py` | one TypedDict per kind with the fields of the resource, including the fields only the lab uses (a server's `_lab`, its NICs); mypy checks them |
| `api/hetzner.py` | the JSON shapes of the Cloud API (`<thing>_json(lab, thing)`) |
| `api/<resource>.py` | one resource each: checks, its action table, routes |
| `api/nics.py` | a server's NICs in cloud networks: attach, detach, alias IPs (these are server actions) |
| `api/public_addresses.py` | what primary IPs and Floating IPs share: free addresses, reverse DNS |
| `api/storagebox/` | the Storage Box API (a separate app with its own JSON shapes) |
| `api/restart.py` | what happens when the lab starts again: flags are cleared, servers come back |
| `api/control.py` | lab control under `/_lab/` (faults, clients, reset); no resource module imports it |
| `api/faults.py` | the faults and netem settings in the state; they are dropped together with the server, client or link they point to |
| `api/desired.py` | pure functions: they read the state and return plain data that says what the system should look like |
| `api/system.py` | puts the state in place on the system: reads it on the loop (through `desired.py`), hands the plain data to the fabric in a thread, and stores what the fabric reports back |
| `fabric/` | changes the system, with plain data as arguments; it never sees the state. Its parts are listed in `fabric/__init__.py` |
| `config.py` | configuration defaults and the lab's addresses (the scripts get them as `HLAB_*`) |
| `daemon.py`, `cli.py` | the container's main process; the `hlab` command inside the container |

## Actions

Every resource lists its actions in a table: `{command: ActionSpec(Hetzner's name,
function)}`. The table is a constant `<KIND>_ACTIONS` of type `ActionTable` in the
resource's module (`grep -rn "ActionTable\[" hlabd` finds them all). `ActionSpec` and
`act()` are in `lab.py`.

A function is named after its command (`reboot`, `add_subnet`). If two resources share
a command, the name starts with the resource (`primary_ip_assign`,
`floating_ip_assign`). The function gets `(lab, resource, body)`, checks the request
and returns a `lab.Plan`. It is a plain `def`. It is `async def` only when it must ask
the system before it can decide; for example, `reset_password` asks whether the VM
runs, through `await lab.blocking(...)`. Then `act()` awaits it. The plan has these
fields:

- `steps`: an async function that does the slow part through `lab.fabric`. `None`
  means the change is already complete, so the action finishes right away.
- `extra`: more fields for the answer, such as `root_password`.
- `server`: the server the steps work on. It stays locked while they run. If they
  fail, the server leaves its in-between status (such as `starting`).
- `resources`: more resources the action is about.
- `finished`: more actions that the change causes. They are finished right away. Each
  is a pair of Hetzner's name and the resources. Only a table that answers with a list
  of actions uses this (see below).

`lab.act()` runs every table in the same way, so an action function never sets a flag
itself. An action that only changes the record has `flag=False` in its row, so it takes
no flag (for example `change_protection`). The plain delete protection is one shared
function, `change_delete_protection` in `api/web.py`.

Firewall actions answer with a list of actions, as on Hetzner. Their rows say
`answer="actions"`. The answer lists the action and then the plan's `finished`
actions (for example `set_firewall_rules`, then `apply_firewall` with the servers).
They take effect right away and take no flag. After `act()`, the route rewrites the
firewall table.

### How flags flow

A flag on a resource says that an action is working on it. A second action gets 423.
The flags are listed next to `FLAGS` in `lab.py`. Code holds them only through
`lab.holding()`; `act()` uses it too:

```text
request
  → act() or lab.holding(): held.claim(resource, flag)   423 if the flag is set already
  → the plan is made (in act(): by the table's function)
      ├─ refused (ApiError) or failed → the holding block ends: flags off
      ├─ no steps (steps is None)     → act() takes the flags off and saves,
      │                                 then answers with the finished action
      └─ steps                        → run_action(..., holding=held) takes over the
                                        flags; the answer goes out with the action
                                        running; the steps run in the background
                                        (lab.spawn)
                                        → when they end (success, error, cancelled),
                                          run_action takes the flags off and then
                                          finishes the action
```

`act()` claims the flag of the resource. If the plan names a different server, it
also claims that server's `_locked` flag. Every flag is a field that starts with `_`:
it is set to True and later removed. The API shows a server's `_locked` as its
`locked` field (`server_json`).

New code uses `act()`. Some work is not a Hetzner action on an existing resource, for
example creating or deleting a server. Such code holds its flags through
`lab.holding()` itself (`grep -rn "lab.holding()" hlabd` lists it):

```python
with lab.holding() as held:
    held.claim(server, "_locked", SERVER_LOCKED)  # 423 if it is set already
    ...  # refused or failed: the flags come off
    lab.run_action(action, steps, holding=held)  # the flags stay on until the steps end
```

`held.release()` takes the flags off early. `held.keep()` leaves them on a record that
is gone (a deleted volume). Only `lab.py` calls `lab.claim()` and `lab.release()`.

### Example: powering off a server

`POST /v1/servers/{id}/actions/poweroff`, from the request to the finished action:

1. `api/web.py` checks auth, the rate limit, and the request against the spec.
2. The route `server_action` (`api/servers.py`, `add_routes`) is `async def`, like
   every route. It awaits the body (`read_body`), then `run_server_action`.
3. `run_server_action` looks up the server (`lab.server_or_404`, else 404) and awaits
   `lab.act(SERVER_ACTIONS, "poweroff", server, "server", body, "_locked",
   SERVER_LOCKED)`.
4. `act()` finds `ActionSpec("stop_server", poweroff)` in the table (else
   `unsupported`). It claims the server's `_locked` flag (423 if another action runs)
   and calls `poweroff(lab, server, body)`, which returns `Plan(steps, server=server)`.
5. The plan has steps, so `act()` creates the action (`lab.new_action`) and hands the
   steps and the flag to `lab.run_action`. That starts the steps as a task on the loop
   (`lab.spawn`). The route answers 201 with the action `running`.
6. The steps call `await system.kill_vm(lab, server)`. Then they set the status to
   `stopping`, wait `POWEROFF_SECONDS`, set it to `off` (`lab.set_status`, which
   saves), and wait `POWEROFF_END_SECONDS`. The next section shows what `kill_vm` does.
7. When the steps end, `run_action` takes the `_locked` flag off and finishes the
   action (`lab.finish`: `success`, or `error` with the message). If the steps failed,
   the server leaves `stopping` and gets the status its VM really has.

## Where code runs

- All code that reads or changes the state runs on the event loop. Every route is
  `async def`. The steps of actions, the VM watcher, the snapshot plans and the restart
  are tasks on the loop too. FastAPI's worker threads are not used.
- Blocking work (scripts, files, guest agents) runs in a thread through
  `await lab.blocking(work, *args)`, so the API keeps answering. `work` gets plain
  data and returns plain data, never the state. Mostly `work` is a fabric method that
  gets what `api/desired.py` computed. A fabric method may also get a record, but it
  reads only fields that never change (ID, tap, MAC). A field that can change, such as
  a server's name or type, is passed as its own argument.
- A thread cannot be cancelled. So `lab.blocking` keeps track of its threads, and a
  lab reset waits for them after it has cancelled the actions (`lab.stop_actions`).
- Some work puts a whole picture of the system in place: fault sets, latency,
  firewall, DHCP, routes. It computes and hands over the picture while it holds
  `lab.picture_lock` (an `asyncio.Lock`). So pictures are put in place in the order
  they were computed, and an older one never overwrites a newer one (`api/system.py`).
  Routing a Floating IP takes the lock too (`system.route_floating_ip`).
- Inside the fabric, `net_lock` (a thread lock) keeps its threads apart. Taps change
  only while it is held, so tc and nft still find the taps they checked.
- Actions wait as long as Hetzner does. The waits are Hetzner's measured timings, in
  one table in `lab.py` (`POWEROFF_SECONDS`, ...); `lab.sleep()` scales them by
  `time_scale`.

### Example: a VM stops

This is `system.kill_vm`, which the power-off steps above await. It shows where each
part runs:

```text
event loop   the poweroff steps call `await system.kill_vm(lab, server)`
thread       `await lab.blocking(fabric.vms.kill, server, server["name"])`
               the name is read on the loop and passed as plain data;
               hlab.sh kill-vm runs; the loop serves other requests meanwhile
event loop   kill_vm reads the server's taps from the state (a plain list)
thread       `await lab.blocking(fabric.network.taps_down, taps)`
event loop   `system.write_dhcp`: under `lab.picture_lock`, `desired.dhcp_hosts`
             computes the DHCP hosts from the state
thread       `await lab.blocking(fabric.network.write_dhcp, hosts)`
event loop   back in the steps: `lab.set_status(server, "stopping")` changes the
             state in a `with state.change():` block; the block's end calls
             `state.save()`, which serializes the state on the loop
writer       the writer thread writes the file and fsyncs it
```

Between two awaits only this task runs, so its reads and changes of the state fit
together. Across an await, other tasks may change the state (see State, Awaits).

## State

This is the one full explanation; `lab.py`, `state.py` and CLAUDE.md point here.

**One owner.** The event loop owns the state: everything reachable from `state.data`
(records, flags, statuses, the records only the lab uses). Only code on the loop's
thread reads or changes it. So two pieces of code never change it at the same time.
Between two awaits nothing else runs: a check and the change after it fit together,
and a loop over the state sees no change in the middle. `state.check_owner()` checks
this rule. Every way into the state in `State` calls it, and it raises AssertionError
on another thread. The daemon makes its loop the owner at start (`lab.own_state()`).
The unit tests record every breach, also in background steps where the error would
only fail the action, and then fail the test (`state_owner_kept` in
`tests/conftest.py`).

**Changes.** A change that must be saved is a `with lab.state.change():` block. The
block is a transaction. If it raises, the state is put back as it was and nothing is
saved. Every dict and list keeps its identity, so records that other code holds stay
valid. If the block succeeds, the state is saved. Nested blocks save once, at the end
of the outer one.

```python
with lab.state.change():
    check_unique_name(lab.state["volumes"].values(), name, volume, "volume")
    volume["name"] = name
```

The block must not await. It is a plain `with`, and another task would see the change
half done. `check_owner` refuses a task's access while another task's block is open,
and `tests/test_state.py` checks that no block in `hlabd/` awaits. Checking first and
changing afterwards still reads best. Some changes happen outside a block, such as a
flag, or the change of a plan that has no steps. The next save stores them: `act()`
saves, and so do `lab.set_status()` and `lab.finish()`.

**Saving.** `state.save()` serializes the state to JSON on the loop. This is quick at
the lab's size, and the number of finished actions is capped. Then it hands the JSON
to a writer thread, which writes the file atomically and fsyncs it. If saves come
faster than the disk, only the newest is written. `state.flush()` waits until
everything saved is on disk. The daemon flushes when it stops, a lab reset flushes
before it answers, and the unit tests flush before they read the file.

**Awaits.** Across an await other tasks run, so what was read before may have changed.
Flags (`lab.holding()`) keep other actions off a resource across awaits. A loop that
awaits for each item (a script per server) loops over a copy (`list(...)`). Where it
matters, it checks again after the await (`stop_if_ended`, `make_available`).

## Recipes

**A new action**: write a function named after the command (see Actions), `(lab,
resource, body) -> Plan`, next to the others in the resource's module. Add it to the
table: `"command": ActionSpec("hetzner_name", function)`. Add `flag=False` if it only
changes the record, and `answer="actions"` if Hetzner answers with a list of actions.
If it gives the server a new in-between status (like `rebuilding`), add the status to
`IN_BETWEEN` in `lab.py` (for failures) and to `status_after_restart` in
`api/restart.py`.

**A new field on a resource**: add it to the resource's TypedDict (`records.py`), to
the code that creates the resource (`create_*`), to its JSON shape (`api/hetzner.py`,
or the Storage Box modules), and to its PUT keys if it can change. The spec check in
the unit tests tells you if the shape is wrong.

**Something new on the system** (a tap, a route, a file): add a function with a header
comment to `scripts/hlab.sh` and its verb to `main`. The verb is the function's name
with dashes: `create_volume` is `create-volume`. Call it from a method of the right
`fabric/` part, and call that method from the API. Addresses come from `config.py` as
`HLAB_*`. The method gets what it needs from the state as arguments: a pure function
in `api/desired.py` computes it, and `api/system.py` (or the caller) reads it on the
loop and passes it to the method in a thread (`await lab.blocking(...)`).

**A new flag**: add it to `Flag` and `FLAGS` in `lab.py` and to the comment above
them, so that a restart clears it (`lab.clear_flags`). Add it to the record's
TypedDict as `NotRequired[bool]`.

## The packet path of a public address

Four things decide who may send what, and where an address goes. Each is rebuilt as a
whole from the state when something changes: `api/desired.py` computes it, and
`api/system.py` hands it to the fabric.

- routes (`system.route_floating_ip`, `system.reroute_floating_ips`; in the end
  `hlab.sh set-route`),
- the nft sets of `table inet hlab` (`system.apply_faults`): faults, the sources each
  tap may send from, the members of cloud networks,
- the firewall table `inet hlabfw` (`system.apply_firewall`; `ruleset` in
  `fabric/firewall.py` builds it),
- tc on the taps (`system.apply_latency`): latency between locations, netem faults.

So moving a Floating IP changes its route and calls `system.apply_faults` (the new
server may now send from it) and `system.apply_latency`.

## Tests

- `tests/`: unit tests, no VMs. `FakeShell` (`tests/conftest.py`) stands in for
  `hlab.sh` and records every call. `time_scale = 0` skips Hetzner's waits. The `api`
  fixture is a test client of the Cloud API. It runs the app on its own loop, which
  owns the state, so a test reads or changes the state there:
  `api.on_loop(work, *args)` (`work` may be async) and `state_of(api, "servers")`.
  `bin/hlab test` is the reference: it runs all of them in the development image. Only
  that image contains the unit tests; the lab image has only `itests/`.
- `tests/test_wrappers.py`: every command that `bin/hlab` dispatches is in its usage
  comment, and the other way round. `bin/hlab` reads the lab name from the sample
  configurations.
- `tests/test_scripts.py`: tests of the scripts themselves, in seconds and without
  VMs. Every verb that hlabd calls exists in `hlab.sh` or `box.sh`, takes a fitting
  number of arguments, and is its function's name with dashes. The nftables rulesets
  parse. `box-shell.sh` allows and denies the right commands. `nft -c` checks fully
  only with CAP_NET_ADMIN, which `bin/hlab test` gives the test container; without
  it, only nft's parser runs. Tests whose tools (bash, nft, /proc) are missing skip
  and say why.
- `itests/`: live tests with VMs in a running lab (`bin/hlab itest`, ~15 min).
- `tests/tofu-smoke/run.sh`: OpenTofu with Hetzner's provider against a running lab.

The unit tests also run without Docker, on Linux or macOS with Python 3.13 or newer.
Run this from the repository's root:

```bash
python3 -m venv .venv && .venv/bin/pip install -e '.[dev]'
python3 tools/fetch_specs.py   # Hetzner's specs into .cache/spec/
.venv/bin/pytest -q
```

The tests look for the specs in `$HLAB_SPEC_DIR`, then in the image's
`/opt/hlab/spec`, then in `.cache/spec/`. Here the Python packages come from PyPI (the
image uses Debian's packages). Tests that need bash, nft, CAP_NET_ADMIN or /proc skip
where those are missing (on macOS: the nftables and `lib.sh` tests). `bin/hlab test`
runs everything.
