# Developing HLab

HLab is a local stand-in for Hetzner Cloud and the Storage Box. A Python daemon,
`hlabd`, answers like Hetzner's APIs, and servers are real QEMU/KVM VMs. Everything
runs in one privileged Docker container.

This file explains how the code fits together and which rules hold it together.
CLAUDE.md lists the rules that are easy to break, [STYLE.md](STYLE.md) the coding
standards. `bin/hlab test` checks the style and runs the unit tests.

## Glossary

The project's own words, in alphabetical order.

- **action**: Hetzner's record of a change (`/v1/actions/...`). Clients poll it until
  it is `success` or `error`. `lab.new_action()` creates one, `lab.finish()` ends it.
- **action table**: a resource's dict `{command: ActionSpec(...)}`, of type
  `ActionTable`, named `<KIND>_ACTIONS` (`SERVER_ACTIONS` in `api/servers.py`).
  `lab.act()` runs its rows. See Actions and flags.
- **ActionSpec**: one row of an action table (`lab.py`): Hetzner's name of the action,
  the function that plans it, whether it takes a flag (`flag`), and how it answers
  (`answer`).
- **command**: the last part of an action's URL, such as `poweroff` in
  `/servers/{id}/actions/poweroff`. Hetzner's name for the resulting action may differ
  (`stop_server`).
- **commit, committed state**: when the outermost `with state.change():` block ends,
  `State.save()` serializes the state into `State.committed` and hands it to the
  writer thread. In the unit tests, `State.check_committed()` compares the state with
  it to find changes made outside a block. See The state.
- **desired**: `api/desired.py`, pure functions that read the state and return plain
  data describing what the system should look like.
- **fabric**: `hlabd/fabric/`, and the `Fabric` object with its parts (`vms`,
  `network`, `firewall`, ...; the table is in `fabric/__init__.py`). It does the work
  on the system and never sees the state.
- **flag**: a field such as `_locked` or `_busy` on a resource, meaning that an action
  is working on it; a second action gets 423. The flags are `Flag` and `FLAGS` in
  `lab.py`.
- **held, `lab.holding()`**: code holds flags only inside `with lab.holding() as
  held:`. `held` is a `HeldFlags` object; `held.claim(...)` sets a flag, and every flag
  still held comes off when the block ends.
- **in-between status**: a server status that lasts only while an action runs, such
  as `starting` or `rebuilding` (`IN_BETWEEN` in `lab.py`).
- **lab-only records**: what the state holds but the API never shows as it is: the
  fields that start with `_` (a server's `_lab`, which is a `ServerLab` in
  `records.py`; the flags; `_dead`; `_copying`) and the top-level records `faults`,
  `netem`, `net_routes`, `box_users` and `box_uids` (`lab_records()` in `state.py`).
- **owner** (of the state): the event loop's thread. Only it may read or change the
  state, and `State.check_owner()` enforces that. See The state.
- **picture**: a complete description of one part of the system (fault sets, latency,
  firewall, DHCP, routes), computed from the state and put in place as a whole.
- **picture_lock**: `lab.picture_lock`, an `asyncio.Lock` held while a picture is
  computed and put in place, so an older picture never overwrites a newer one.
- **plan**: a `Plan` (`lab.py`), returned by an action's function. It says what the
  action will do (`steps`, `server`, `extra`, ...).
- **state**: everything the fake API knows, in memory and saved as JSON in the lab's
  volume (`State` in `state.py`). Resources are kept by kind (`KIND_TYPES`) and ID.
- **steps**: `Plan.steps`, the slow part of an action: an async function that runs in
  the background after the answer has gone out.
- **system**: `api/system.py`, which puts the state in place. It reads the state on
  the loop (mostly through `desired`) and hands plain data to the fabric in a thread.
- **verb**: a command of `scripts/hlab.sh` (or `scripts/box.sh`), such as
  `create-volume`: the shell function's name with dashes. The fabric runs verbs
  through `Shell.run` (`fabric/shell.py`).

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
  → scripts/hlab.sh <verb>  most changes to the system (box.sh for the Storage Box)
```

The unit tests check every answer against Hetzner's OpenAPI specification
(`tests/spec_check.py`), so a missing field or a wrong type fails a test.

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
| `lab.py` | the core: actions (`Plan`, `act`, `run_action`), flags, Hetzner's timings, `blocking` for threads; it imports nothing from `api/` |
| `state.py` | the state and the lab-only records; the owner check, `change()`, the writer thread |
| `records.py` | one TypedDict per kind with the fields of the resource, including the lab-only fields (a server's `_lab`, its NICs); mypy checks them |
| `api/hetzner.py` | the JSON shapes of the Cloud API (`<thing>_json(lab, thing)`) |
| `api/<resource>.py` | one resource each: checks, its action table, routes |
| `api/nics.py` | a server's NICs in cloud networks: attach, detach, alias IPs (these are server actions) |
| `api/public_addresses.py` | what primary IPs and Floating IPs share: free addresses, reverse DNS |
| `api/storagebox/` | the Storage Box API (a separate app with its own JSON shapes) |
| `api/restart.py` | what happens when the lab starts again: flags are cleared, servers come back |
| `api/watcher.py` | the VM watcher: a server whose VM ended by itself becomes off |
| `api/control.py` | lab control under `/_lab/` (faults, clients, reset); no resource module imports it |
| `api/faults.py` | the faults and netem settings in the state; they are dropped together with the server, client or link they point to |
| `api/desired.py` | pure functions from the state to plain data (see desired) |
| `api/system.py` | puts the state in place on the system (see system) |
| `fabric/` | changes the system, with plain data as arguments; its parts are listed in `fabric/__init__.py` |
| `config.py` | configuration defaults and the lab's addresses (the scripts get them as `HLAB_*`) |
| `daemon.py`, `cli.py` | the container's main process; the `hlab` command inside the container |

### Layers

- **`api/` decides.** It checks requests, changes the state and starts actions. It
  never changes the system itself: it calls a `lab.fabric` method, always in a thread
  through `await lab.blocking(...)`. The few files that `api/` reads on its own (the
  lab's public key in `api/metadata.py`, `/etc/passwd` in `api/control.py`) are read in
  a thread too.
- **`fabric/` does the work.** It knows nothing about HTTP and never sees the state;
  it gets IDs, plain values and its own small dataclasses.
- **Only `fabric/` changes the system while the lab runs.** Mostly it runs verbs of
  `scripts/hlab.sh` (and `box.sh`). It also handles the lab's own files under `/hlab`
  in Python: VM directories, console logs (trimmed), volume files, and the files a
  verb then loads (nft scripts, dnsmasq and DNS configuration). Outside the fabric,
  the daemon changes the system only in `daemon.set_up`, before the loop starts, and
  in the state's writer thread (`state.json`).

`lab.py` connects the layers, and every API module gets the same `Lab` object.

## Actions and flags

### Action tables

Every resource lists its actions in a table: `{command: ActionSpec(Hetzner's name,
function)}`. The table is a constant `<KIND>_ACTIONS` of type `ActionTable` in the
resource's module (`grep -rn "ActionTable\[" hlabd` finds them all). The keys are
Hetzner's commands and the names are Hetzner's action names, so never rename them
when you rename a function.

A function is named after its command (`reboot`, `add_subnet`). Primary IPs and
Floating IPs share most of their commands, so their functions start with the resource
(`primary_ip_assign`, `floating_ip_assign`). Elsewhere the command alone is the name,
even where another resource has the same command (`change_type` in `api/servers.py`
and in `api/storagebox/boxes.py`).

The function gets `(lab, resource, body)`, checks the request and returns a `Plan`.
It is always `async def`, and `act()` awaits it. Most never await anything, because
they only check and change the state. Some must ask the system before they can
decide: `reset_password`, for example, asks whether the VM runs, through
`await lab.blocking(...)`.

The plan has these fields:

- `steps`: an async function that does the slow part through `lab.fabric`. `None`
  means the change is already complete; as on Hetzner, it still gets an action, which
  is finished right away.
- `extra`: more fields for the answer, such as `root_password`.
- `server`: the server the steps work on. It stays locked while they run, and if they
  fail, it leaves its in-between status (such as `starting`).
- `resources`: more resources the action is about.
- `finished`: more actions that the change causes, finished right away. Each is a pair
  of Hetzner's name and the resources. Only tables that answer with a list of actions
  use it (see below).

`lab.act()` handles the flags for every table, so an action function never sets a flag
itself. An action that only changes the record has `flag=False` in its row and takes
no flag (`change_protection`, `change_dns_ptr`). Every resource with a plain delete
protection uses the shared `change_delete_protection` from `api/web.py` (primary IPs
wrap it with one extra check).

Firewall actions answer with a list of actions, as on Hetzner, and their rows say
`answer="actions"`. The list holds the action and then the plan's `finished` actions,
for example `set_firewall_rules` followed by `apply_firewall` with the servers. They
take effect right away and take no flag, because `run_firewall_action` passes none to
`act()`. After `act()` returns, it rewrites the firewall table.

### How flags flow

A flag on a resource means that an action is working on it, so a second action gets
423, as on Hetzner. The comment above `FLAGS` in `lab.py` lists the flags and what
each one means. Code holds flags only through `lab.holding()`, and `act()` uses it
too:

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

`act()` claims the resource's flag. If the plan names a different server, it also
claims that server's `_locked`. A flag is set to True and later removed, never set to
False. The API shows a server's `_locked` as its `locked` field (`server_json` in
`api/hetzner.py`). A lab restart removes every flag (`lab.clear_flags`).

New actions go through `act()`. Work that is not a Hetzner action on an existing
resource, such as creating or deleting a server, holds its flags through
`lab.holding()` itself (`grep -rn "lab.holding()" hlabd` lists these places):

```python
with lab.holding() as held:
    held.claim(server, "_locked", SERVER_LOCKED)  # 423 if it is set already
    ...  # refused or failed: the flags come off
    lab.run_action(action, steps, holding=held)  # the flags stay on until the steps end
```

`held.release()` takes the flags off early. `held.keep()` leaves them on a record that
is gone, such as a deleted volume. Only `lab.py` calls `lab.claim()` and
`lab.release()`.

### Example: powering off a server

`POST /v1/servers/{id}/actions/poweroff`, from the request to the finished action:

1. `api/web.py` checks auth, the rate limit, and the request against the spec.
2. The route `server_action` (`api/servers.py`, `add_routes`) awaits the body
   (`read_body`), then `run_server_action`.
3. `run_server_action` looks up the server (`lab.server_or_404`, else 404) and awaits
   `lab.act(SERVER_ACTIONS, "poweroff", server, "server", body, "_locked",
   SERVER_LOCKED)`.
4. `act()` finds `ActionSpec("stop_server", poweroff)` in the table (else
   `unsupported`). It claims the server's `_locked` flag (423 if another action runs)
   and calls `poweroff(lab, server, body)`, which returns `Plan(steps, server=server)`.
5. The plan has steps, so `act()` creates the action (`lab.new_action`) and hands the
   steps and the flag to `lab.run_action`, which starts the steps as a task on the
   loop (`lab.spawn`). The route answers 201 with the action `running`.
6. The steps await `system.kill_vm(lab, server)`, set the status to `stopping`, wait
   `POWEROFF_SECONDS`, set it to `off` (`lab.set_status`, which saves), and wait
   `POWEROFF_END_SECONDS`. The next section shows what `kill_vm` does.
7. When the steps end, `run_action` takes the `_locked` flag off and finishes the
   action (`lab.finish`: `success`, or `error` with the message). If the steps failed,
   the server leaves `stopping` and gets the status its VM really has.

## Where code runs

Everything that touches the state runs on the event loop; everything that blocks runs
in a thread. The details:

- **The loop.** Every route is `async def`, so FastAPI's worker threads are not used.
  The steps of actions, the VM watcher, the snapshot plans and the restart are tasks
  on the loop too.
- **Threads.** Blocking work (scripts, files, guest agents) runs in a thread through
  `await lab.blocking(work, *args)`, so the API keeps answering meanwhile. `work` gets
  plain data and returns plain data, never the state. Usually it is a fabric method
  whose arguments are IDs (`sid`, `vid`), plain values or the fabric's small
  dataclasses (`PublicTap`, `ClientLink`, `Rule`, ...), built on the loop, mostly by
  `api/desired.py`. Two exceptions get an object that holds the state but leave the
  state alone: `state.flush`, and `catalogue.load_images` at start, which fills
  `lab.images`. `tests/test_layers.py` checks that the fabric neither imports nor
  annotates a type of `records.py`.
- **Files.** Once the lab serves requests, code on the loop neither reads nor writes
  files; that is thread work too (for example `fabric.vms.has_disk`, or the lab key in
  `api/metadata.py`). Only the start reads files on the loop: the OpenAPI specs, when
  the apps are built.
- **Cancelling.** A thread cannot be cancelled, so `lab.blocking` keeps track of its
  threads. A lab reset first cancels the actions (`lab.stop_actions`) and then waits
  for their threads, at most `THREADS_WAIT_SECONDS`, and logs the ones still running.
  Every hlab.sh call has a timeout (`Shell.run`: `TIMEOUT`, 120 s, or `LONG_TIMEOUT`
  for commands that copy data, such as image downloads and Storage Box snapshots), so
  a hung script fails its action instead of holding a thread forever.
- **Pictures.** Fault sets, latency, firewall, DHCP and routes are computed and handed
  over while `lab.picture_lock` is held, so they are put in place in the order they
  were computed (`api/system.py`). Routing a Floating IP takes the lock too
  (`system.route_floating_ip`). The lock is not re-entrant: a function that holds it
  must not call another function of `api/system.py` that takes it.
- **The fabric's lock.** Fabric methods run in threads side by side, for example when
  two servers start at once. `net_lock` (a thread lock) keeps them apart where it
  matters: taps are torn down, and nft, tc and dnsmasq are changed, only while it is
  held. So a method that found a tap still finds it when it configures it.
- **Waits.** Actions take as long as on Hetzner. The waits are Hetzner's measured
  timings, in one table in `lab.py` (`POWEROFF_SECONDS`, ...), and `lab.sleep()`
  scales them by `time_scale`.

### Example: a VM stops

This is `system.kill_vm`, which the power-off steps above await, step by step:

```text
event loop   the poweroff steps call `await system.kill_vm(lab, server)`
thread       `await lab.blocking(fabric.vms.kill, server["id"], server["name"])`
               the ID and name are read on the loop and passed as plain data;
               hlab.sh kill-vm runs; the loop serves other requests meanwhile
event loop   kill_vm reads the server's taps from the state (a plain list)
thread       `await lab.blocking(fabric.network.taps_down, taps)`
event loop   `system.write_dhcp`: under `lab.picture_lock`, `desired.dhcp_hosts`
             computes the DHCP hosts from the state
thread       `await lab.blocking(fabric.network.write_dhcp, hosts)`
event loop   back in the steps: `lab.set_status(server, "stopping")` changes the
             state in a `with state.change():` block; the block's end commits
             (`State.save()` serializes the state on the loop)
writer       the writer thread writes the file and fsyncs it
```

Between two awaits only this task runs, so what it reads and changes in the state is
consistent. Across an await, other tasks may change the state (see The state, Awaits).

### The packet path of a public address

Four pictures decide who may send what and where an address goes. Each is rebuilt as
a whole from the state when something changes: `api/desired.py` computes it, and
`api/system.py` hands it to the fabric.

- routes (`system.route_floating_ip`, `system.reroute_floating_ips`; in the end
  `hlab.sh set-route`),
- the nft sets of `table inet hlab` (`system.apply_faults`): faults, the sources each
  tap may send from, the members of cloud networks,
- the firewall table `inet hlabfw` (`system.apply_firewall`; `ruleset` in
  `fabric/firewall.py` builds it),
- tc on the taps (`system.apply_latency`): latency between locations, netem faults.

Moving a Floating IP, for example, changes its route and calls `system.apply_faults`
(the new server may now send from it) and `system.apply_latency`.

## The state

**One owner.** The event loop owns the state: everything reachable from `state.data`
(records, flags, statuses, the lab-only records). Only code on the loop's thread reads
or changes it, so two pieces of code never change it at the same time, and between two
awaits nothing else runs. A check and the change after it therefore fit together, and
a loop over the state sees no change in the middle.

`state.check_owner()` enforces this. Every accessor of `State` calls it, and it raises
AssertionError on any other thread. The daemon makes its loop the owner at start
(`lab.own_state()`). The unit tests record every breach, even one in background steps
where the error would only fail the action, and then fail the test (`state_owner_kept`
in `tests/conftest.py`).

**Changes.** Every change to the state happens inside a `with lab.state.change():`
block: records, flags, actions, the lab-only records.

```python
with lab.state.change():
    check_unique_name(lab.state["volumes"].values(), name, volume, "volume")
    volume["name"] = name
```

The block is a transaction. If it raises, the state is put back as it was and nothing
is saved; every dict and list keeps its identity, so records that other code holds
stay valid. If it succeeds, the state is committed and saved. Blocks may nest, but only
the outermost one takes the snapshot, undoes and commits. So an error in a nested
block must reach the outermost block: if code in between catches it, the nested change
stays half done (the unit tests fail such a block).

The block must not await. It is a plain `with`, and another task would see the change
half done. `check_owner` refuses another task's access while a block is open, and
`tests/test_state.py` checks that no block in `hlabd/` awaits. A change that spans an
await (a script, say) is two blocks: one before the await and one after it, or one that
undoes the first if the script fails. Helpers that change the state, such as
`lab.claim()`, `lab.new_action()`, `lab.set_status()` and `lab.finish()`, open their
own block, which simply nests inside a caller's block.

The unit tests turn on `State.verify_changes`. Then each outermost block, and the end
of each test, checks that the state still equals the committed state
(`check_committed()`); a difference is a change made outside a block, and the
AssertionError shows it as a diff. The daemon leaves this off, because it serializes
the state once more per block. A test that needs to set up the state uses
`change_state(api, work, ...)` (`tests/conftest.py`).

**Saving.** Committing serializes the state to JSON on the loop. That is quick at the
lab's size, and the number of finished actions is capped (`MAX_ACTIONS`). A writer
thread then writes the file atomically and fsyncs it; if saves come faster than the
disk, only the newest is written. `state.flush()` waits until everything saved is on
disk. The daemon flushes when it stops, a lab reset flushes before it answers, and the
unit tests flush before they read the file.

**Awaits.** Across an await, other tasks run, so what was read before may have
changed. Flags (`lab.holding()`) keep other actions off a resource across awaits. A
loop that awaits for each item (a script per server) iterates over a copy
(`list(...)`). Where it matters, the code checks again after the await
(`stop_if_ended`, `make_available`).

## Recipes

**A new action**: write a function named after the command (see Action tables),
`async def name(lab, resource, body) -> Plan`, next to the others in the resource's
module. Add it to the table: `"command": ActionSpec("hetzner_name", function)`. Add
`flag=False` if it only changes the record, and `answer="actions"` if Hetzner answers
with a list of actions. If it gives the server a new in-between status (like
`rebuilding`), add the status to `IN_BETWEEN` in `lab.py` (for failures) and to
`status_after_restart` in `api/restart.py`.

**A new field on a resource**: add it to the resource's TypedDict (`records.py`), to
the code that creates the resource (`create_*`), to its JSON shape (`api/hetzner.py`,
or the Storage Box modules), and to its PUT keys if it can change. The spec check in
the unit tests tells you if the shape is wrong.

**Something new on the system** (a tap, a route, a file): add a function with a header
comment to `scripts/hlab.sh` and its verb to `main`. The verb is the function's name
with dashes: `create_volume` becomes `create-volume`. Call it from a method of the
right `fabric/` part, and call that method from the API. Addresses come from
`config.py` as `HLAB_*`. The method gets what it needs from the state as arguments: a
pure function in `api/desired.py` computes it, and `api/system.py` (or the caller)
reads it on the loop and passes it to the method in a thread
(`await lab.blocking(...)`).

**A new flag**: add it to `Flag` and `FLAGS` in `lab.py` and to the comment above
them, so that a restart clears it (`lab.clear_flags`). Add it to the record's
TypedDict as `NotRequired[bool]`.

**A new resource kind**: `tests/test_state.py` fails until the first three steps are
done.

1. `records.py`: its TypedDict, added to `Record` (and to `ActionRecord` if it has
   actions, to `ProtectedRecord` if it has a plain delete protection).
2. `state.py`: a row in `KIND_TYPES` (record type, a free ID range); `KINDS` and
   `ID_BASE` follow from it.
3. The `@overload`s of `State.__getitem__` (`state.py`) and `Lab.get_or_404`
   (`lab.py`): mypy needs them written out.
4. Its module `api/<kind>.py` (checks, action table, routes), added to
   `create_app` (`api/app.py`); its JSON shape in `api/hetzner.py`; its flag, if new
   (above).

## Tests

- `tests/`: unit tests, no VMs. `FakeShell` (`tests/conftest.py`) stands in for
  `hlab.sh` and records every call, and `time_scale = 0` skips Hetzner's waits. The
  `api` fixture is a test client of the Cloud API. It runs the app on its own loop,
  which owns the state, so a test reads or changes the state there:
  `api.on_loop(work, *args)` (`work` may be async) and `state_of(api, "servers")`.
  `bin/hlab test` is the reference: it runs all of them in the development image. The
  lab image contains only `itests/`.
- `tests/test_wrappers.py`: every command that `bin/hlab` dispatches is in its usage
  comment, and the other way round. `bin/hlab` reads the lab name from the sample
  configurations.
- `tests/test_scripts.py`: tests of the scripts themselves, in seconds and without
  VMs. Every verb that hlabd calls exists in `hlab.sh` or `box.sh`, takes a fitting
  number of arguments, and is its function's name with dashes; the nftables rulesets
  parse; `box-shell.sh` allows and denies the right commands. `nft -c` checks fully
  only with CAP_NET_ADMIN, which `bin/hlab test` gives the test container; without it,
  only nft's parser runs.
- `itests/`: live tests with VMs in a running lab (`bin/hlab itest`, ~15 min).
- `tests/tofu-smoke/run.sh`: OpenTofu with Hetzner's provider against a running lab.
  After a clean destroy it removes its local state files; `.terraform/` (the provider)
  stays and is ignored by git.

The unit tests also run without Docker, on Linux or macOS with Python 3.13 or newer.
From the repository's root:

```bash
python3 -m venv .venv && .venv/bin/pip install -e '.[dev]'
python3 tools/fetch_specs.py   # Hetzner's specs into .cache/spec/
.venv/bin/pytest -q
```

The tests look for the specs in `$HLAB_SPEC_DIR`, then in the image's
`/opt/hlab/spec`, then in `.cache/spec/`. Outside Docker the Python packages come
from PyPI (the image uses Debian's). Tests that need bash, nft, CAP_NET_ADMIN or /proc
skip and say why where those are missing (on macOS: the nftables and `lib.sh` tests).
Set `PYTHONDONTWRITEBYTECODE=1` to keep `__pycache__` out of the working copy (the
development image sets it).
