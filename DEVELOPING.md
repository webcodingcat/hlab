# Developing HLab

HLab is a local stand-in for Hetzner Cloud and the Storage Box. A Python daemon,
`hlabd`, answers like Hetzner's APIs, and servers are real QEMU/KVM VMs. Everything
runs in one privileged Docker container.

This file explains how the code fits together and which rules hold it together.
CLAUDE.md lists the rules that are easy to break, [STYLE.md](STYLE.md) the coding
standards. `bin/hlab test` checks the style and runs the unit tests.

## Glossary

- **action**: Hetzner's record of a change (`/v1/actions/...`), polled by clients until
  it is `success` or `error`.
- **action table**: a resource's `ActionTable` (`lab.py`), named `<KIND>_ACTIONS`: its
  rows, the type its actions name it by, its flag. `lab.act()` runs its rows.
- **ActionSpec**: one row of an action table: Hetzner's action name, the function that
  plans it, whether it takes a flag, how it answers.
- **command**: the last part of an action's URL (`poweroff` in
  `/servers/{id}/actions/poweroff`). Hetzner's action name may differ (`stop_server`).
- **commit**: the end of the outermost `with state.change():` block. The state is
  serialized and handed to the writer thread.
- **desired**: `api/desired/`, pure functions from the state to plain data that
  describe what the system should look like.
- **fabric**: `hlabd/fabric/`, the parts that do the work on the system
  (`vms`, `network`, `firewall`, ...). It never sees the state.
- **flag**: a field such as `_locked` on a resource: an action works on it, and a
  second action gets 423. The flags are `Flag` and `FLAGS` in `lab.py`.
- **held**: the `HeldFlags` of `with lab.holding() as held:`, the only way code holds
  flags.
- **in-between status**: a server status that lasts only while an action runs, such
  as `starting` (`IN_BETWEEN` in `lab.py`).
- **lab-only records**: what the state holds but the API never shows as it is: fields
  starting with `_` (a server's `_lab`, the flags) and the top-level records `faults`,
  `netem`, `net_routes`, `box_users`, `box_uids`, typed properties of `State`
  (`lab.state.faults`).
- **owner**: the event loop's thread, the only one that may touch the state.
- **picture**: a complete description of one part of the system (fault sets, latency,
  firewall, DHCP, routes), computed from the state and put in place as a whole, under
  `lab.picture_lock`.
- **plan**: the `Plan` an action's function returns: what the action will do.
- **`# record:` comment**: marks a record used after an await on purpose, with the
  reason. See The state, Awaits.
- **state**: everything the fake API knows, in memory and saved as JSON (`State` in
  `state.py`), kept by kind (`SERVERS`, ...) and ID.
- **steps**: `Plan.steps`, the slow part of an action, run in the background after the
  answer has gone out.
- **system**: `api/system.py`, which puts the state in place: it reads the state on
  the loop (mostly through `desired`) and hands plain data to the fabric in a thread.
- **verb**: a command of `scripts/hlab.sh` (or `box.sh`), the shell function's name
  with dashes (`create-volume`).

## Where to start reading

One path shows most of the ideas: `lab.py` (`Plan`, `act`, `run_action`), the action
`poweroff` in `api/servers.py`, `system.kill_vm`, and the verb `kill-vm` in
`scripts/hlab.sh`. The two examples below walk along it. Then read The state.

## How a request flows

```text
HTTP request
  → daemon.py               one server; the address picks the app (Cloud API, Storage
                            Box API, metadata service; the web UI by its port);
                            every request is logged
  → api/web.py              auth, rate limit, injected faults, OpenAPI request check
  → api/<resource>.py       the route checks the request, changes the state, starts
                            an action
  → lab.py                  act() runs the action from the resource's table; flags
                            stop a second action; the slow part runs in the background
  → fabric/<part>.py        VMs, taps, routes, nftables, Storage Box accounts
  → scripts/hlab.sh <verb>  most changes to the system (box.sh for the Storage Box)
```

The unit tests check every answer against Hetzner's OpenAPI specification
(`tests/spec_check.py`), so a missing field or a wrong type fails a test.

## The pieces

| Path | What |
|---|---|
| `Dockerfile` | the lab image (Debian 13 packages: QEMU, dnsmasq, NSD, nftables, HAProxy, Pebble, OpenSSH, Python) and the development image on top (linters, unit tests) |
| `bin/hlab` | the host side, in Bash: build, start, stop, pass commands into the container |
| `scripts/hlab.sh`, `hlab.nft` | the verbs (taps, QEMU, nftables, tc, DHCP, DNS, volumes, images, namespaces) and the table `inet hlab` |
| `scripts/box.sh` | the Storage Box: sshd, chrooted accounts, snapshots |
| `tests/`, `itests/` | unit tests (one file per feature) and the OpenTofu smoke test; live tests with VMs |
| `tools/lint.sh`, `tools/fetch_specs.py` | the checks of STYLE.md; Hetzner's OpenAPI specs |

Inside `hlabd/`:

| Module | Job |
|---|---|
| `lab.py` | the core: actions, flags, Hetzner's timings, `blocking`; imports nothing from `api/` |
| `state.py`, `migrations.py` | the state, its kinds, the owner check, `change()`, the writer thread; the saved version and its migrations |
| `records.py` | one TypedDict per kind, lab-only fields included |
| `config.py` | the typed configuration, its defaults, the lab's addresses (`HLAB_*` for the scripts) |
| `api/<resource>.py` | one resource each: checks, action table, routes |
| `api/hetzner/` | the Cloud API's JSON shapes (`<thing>_json(lab, thing)`), one module per area |
| `api/resources.py` | what every resource changes alike: fields on PUT, delete protection |
| `api/nics.py`, `api/public_addresses.py` | a server's NICs in cloud networks; what primary and Floating IPs share |
| `api/storagebox/` | the Storage Box API, a separate app |
| `api/restart.py`, `api/watcher.py` | what happens when the lab starts again; a VM that ended by itself |
| `api/control.py`, `api/faults.py` | lab control under `/_lab/`; the faults in the state |
| `api/ui.py`, `ui/` | the web UI: an app on its own port (`config.UI_PORT`) with the JSON overview, the newest log lines and the `/_lab` routes of `api/control.py`; the page is plain HTML, CSS and JavaScript, no build step |
| `api/desired/`, `api/system.py` | see desired and system |
| `fabric/` | changes the system, with plain data as arguments (parts listed in `fabric/__init__.py`) |
| `daemon.py`, `cli.py` | the container's main process; `hlab` inside the container |

### Layers

- **`api/` decides.** It checks requests, changes the state and starts actions. It
  reaches the system only through `lab.fabric`, always in a thread
  (`await lab.blocking(...)`). The few files it reads itself (the lab key, `/etc/passwd`)
  are read in a thread too; only at start, before serving, does it read the OpenAPI
  specs, the Public Suffix List, the image files and the web UI's page.
- **`fabric/` does the work.** It knows nothing about HTTP and never sees the state; it
  gets IDs, plain values and its own small dataclasses (`tests/test_layers.py` checks
  that it does not touch `records.py`).
- **Only `fabric/` changes the system while the lab runs**: through the verbs, and the
  lab's own files under `/hlab` (VM directories, console logs, volume files, the files
  a verb loads). Outside it, only `daemon.set_up` before the loop starts and the
  state's writer thread (`state.json`).

## Actions and flags

### Action tables

Every resource lists its actions in a table in its module
(`grep -rn "ActionTable\[" hlabd`):

```python
SERVER_ACTIONS = ActionTable[Server](
    "server",  # how its actions name the resource
    {"poweroff": ActionSpec("stop_server", poweroff), ...},
    flag="_locked",  # held while an action runs; another one gets 423 with busy
    busy=SERVER_LOCKED,
)
```

The keys are Hetzner's commands and the names Hetzner's action names: never rename
them with a function. A table without a flag runs its actions side by side.

A function is named after its command (`reboot`, `add_subnet`), even where another
resource has the same command. Where two resources share commands in one module, the
second one's functions start with it: primary and Floating IPs (`primary_ip_assign`),
and RRSets next to their zone (`rrset_change_ttl`).

The function is `async def name(lab, resource, body) -> Plan`. It checks the request,
may change the state, and returns a plan. Most never await; some must ask the system
first (`reset_password` asks whether the VM runs). The plan's fields:

- `steps`: the slow part, an async function. `None` means the change is complete; as
  on Hetzner, it still gets an action, finished right away.
- `extra`: more fields for the answer (`root_password`).
- `server`: the server the steps work on. It stays locked while they run and leaves
  its in-between status if they fail.
- `resources`: more resources the action is about.
- `finished`: more actions the change causes, finished right away, for tables that
  answer with a list of actions.

`act()` handles the flags, so an action function never sets one. A row with
`flag=False` takes none (`change_protection`, `change_dns_ptr`). Firewall rows say
`answer="actions"`: the answer lists the action and then the `finished` ones
(`set_firewall_rules`, then `apply_firewall`).

### How flags flow

```text
request
  → held.claim(resource, flag)        423 if it is set already
  → the plan is made
      ├─ refused or failed            → the holding block ends: flags off
      ├─ no steps                     → flags off, the action is finished
      └─ steps                        → run_action takes the flags over; the answer
                                        goes out; the steps run in the background;
                                        when they end, run_action takes the flags
                                        off and then finishes the action
```

`act()` claims the table's flag, and the server's `_locked` too if the plan names
another server. Work that is not an action on an existing resource (creating or
deleting a server) holds its flags itself (`grep -rn "lab.holding()" hlabd`):

```python
with lab.holding() as held:
    held.claim(server, "_locked", SERVER_LOCKED)  # 423 if it is set already
    ...  # refused or failed: flags off
    lab.run_action(action, steps, holding=held)  # on until the steps end
```

What happens to the held flags:

| Call | Effect |
|---|---|
| block ends | every flag still held comes off |
| `held.release()` | they come off now (the work goes on without them) |
| `held.take()` | they are handed over; `run_action` calls it and takes them off when the steps end |
| `held.keep()` | they stay on for good: the record is gone (a deleted volume) |

Where the flag and the state change must be undone together, open both in one `with`:
`with lab.holding() as held, lab.state.change():`. `change()` is the inner block, so
on an error it restores the state first, flags included, and `holding()` then has
nothing left to take off. Only `lab.py` calls `lab.claim()` and `lab.release()`.

### Example: powering off a server

`POST /v1/servers/{id}/actions/poweroff`:

1. `api/web.py` checks auth, the rate limit and the request.
2. The route looks the server up (404 if gone) and awaits
   `lab.act(SERVER_ACTIONS, "poweroff", server, body)`.
3. `act()` finds the row (else `unsupported`), claims `_locked` (423 if another action
   runs) and calls `poweroff`, which returns `Plan(steps, server=server)`.
4. `act()` creates the action and hands steps and flag to `run_action`, which starts
   the steps as a task. The route answers 201 with the action `running`.
5. The steps kill the VM (next section), set `stopping`, wait, set `off`, wait.
6. `run_action` takes the flag off and finishes the action (`success`, or `error` with
   the message; a failed server gets the status its VM really has).

## Where code runs

Everything that touches the state runs on the event loop; everything that blocks runs
in a thread.

- **The loop.** Every route is `async def`. The steps of actions, the VM watcher,
  snapshot plans, daily backups, the metrics sampler, the load balancers' health
  watcher and the restart are tasks on the loop too. The sampler and the health
  watcher keep what they read in `lab.readings`, in memory only, not in the state.
- **Threads.** Blocking work (scripts, files, guest agents) runs through
  `await lab.blocking(work, *args)`. `work` gets and returns plain data, never the
  state: IDs, values, or the fabric's dataclasses, built on the loop. Two exceptions
  hold the state but leave it alone: `state.flush`, and `catalogue.load_images` at
  start.
- **Files.** Once serving, code on the loop reads and writes no files.
- **The reset.** While `/_lab/reset` runs, `lab.resetting` is set: the APIs answer
  503, background jobs skip their turn, a second reset gets 409. A failed teardown
  step is logged and named in the answer; the others go on.
- **Cancelling.** A thread cannot be cancelled. A reset cancels the actions, then waits
  for their threads (at most `THREADS_WAIT_SECONDS`) and logs the rest. Every verb has
  a timeout (`Shell.run`), so a hung script fails its action.
- **Pictures.** Computed and handed over while `lab.picture_lock` is held, so they are
  put in place in the order they were computed. The lock is not re-entrant: a holder
  that takes it again raises RuntimeError instead of waiting forever.
- **The fabric's lock.** Fabric methods run side by side (two servers start at once).
  Taps are torn down, and nft, tc and dnsmasq changed, only under `net_lock`, so a
  method that found a tap still finds it when it configures it.
- **Waits.** Hetzner's measured timings are one table in `lab.py`; `lab.sleep()`
  scales them by `time_scale`.

### Example: a VM stops

```text
event loop   the poweroff steps: await system.kill_vm(lab, lab.current(SERVERS, sid))
             kill_vm reads the ID, name and taps from the record (plain data)
thread       fabric.vms.kill(sid, name)        hlab.sh kill-vm; the loop serves others
thread       fabric.network.taps_down(taps)
event loop   under picture_lock: desired.dhcp_hosts(state)
thread       fabric.network.write_dhcp(hosts)
event loop   lab.set_status(lab.current(SERVERS, sid), "stopping"): the server is
             looked up again and changed in a change() block, which commits
writer       the file is written and fsynced
```

Between two awaits only this task runs, so what it reads is consistent. Across an
await, other tasks may change the state (The state, Awaits).

### The packet path of a public address

Four pictures decide who may send what and where an address goes: routes, the nft
sets of `inet hlab` (faults, allowed sources, network members), the firewall table
`inet hlabfw`, and tc on the taps (latency, netem). Moving a Floating IP, for example,
changes its route, then the nft sets (the new server may send from it) and tc.

## The state

**One owner.** Only code on the loop's thread reads or changes the state, so between
two awaits nothing else touches it: a check and the change after it fit together.
Every accessor calls `state.check_owner()`, which raises AssertionError on another
thread; the unit tests fail on any breach, even inside background steps.

**Changes.** Every change, flags and actions included, is inside a
`with lab.state.change():` block:

```python
with lab.state.change():
    check_unique_name(lab.state[VOLUMES].values(), name, volume, "volume")
    volume["name"] = name
```

The block is a transaction. If it raises, the state is restored in place (dicts and
lists keep their identity) and nothing is saved; if it succeeds, the state is
committed. Blocks nest, but only the outermost one snapshots, restores and commits, so
an error in a nested block must reach it.

The block must not await (`tests/test_state.py` checks it). A change that spans an
await is two blocks, one before and one after, or an undo (below). Helpers such as
`lab.set_status()` open their own block, which nests in the caller's.

The unit tests turn on `State.verify_changes`: each outermost block and the end of
each test compare the state with the committed one, so a change outside a block fails
with a diff. Tests set up state with `change_state(api, work, ...)`.

**Saving.** Each outermost block snapshots every dict and list of the state first,
so it can restore them, and committing serializes the state on the loop. Both are
quick at the lab's size (finished actions are capped by `MAX_ACTIONS`). A writer thread writes the file
atomically; if saves come faster than the disk, only the newest is written.
`state.flush()` waits until it is on disk (when the daemon stops, after a reset, in
tests).

**Kinds.** A kind is a typed key (`SERVERS`, `VOLUMES`, ...): `lab.state[SERVERS]` is
a `dict[int, Server]`. `ALL_KINDS` is the one list of kinds; `KIND_TYPES` finds one by
name at runtime.

**Versions.** The saved state has a `version`. `MIGRATIONS[n]` takes it from n to
n + 1, in order, on load; a file without a version is version 0. A newer file refuses
to load (`StateVersionError`), because saving would lose shapes this code does not
know. A new kind or lab-only record needs no migration.

**Awaits.** Across an await other tasks run, and a record may change or vanish. Flags
keep other actions off, but not a reset. So code keeps a record's ID across an await
and looks the record up again. IDs never change and are never reused; other fields
that never change (a tap's name) may be read from the old record.

Which lookup to use:

| Where | Lookup | If the record is gone |
|---|---|---|
| routes and action functions | `lab.get_or_404(KIND, id, what)` or a `<thing>_or_404` helper | 404 |
| steps of an action | `lab.current(KIND, id)` | `RecordGoneError`: the action ends with an error ("the server … was deleted while the action ran") |
| background work, cleanup after a failure | `lab.state.get(KIND, id)` | None: leave it alone, never hide the first error |

Before a step fails on a gone record, it removes what it made on the system. A loop
that awaits per item iterates over a copy of the IDs (`list(lab.state[SERVERS])`). A
function that awaits may take a record if it reads only its ID before the first await
and looks it up after each (`system.start_vm`).

`tests/test_await_rule.py` checks this in `api/` and `lab.py`, and that no dataclass
field holds a record. This fails:

```python
async def steps():
    server = lab.current(SERVERS, sid)
    await lab.blocking(lab.fabric.vms.kill, sid, server["name"])
    lab.set_status(server, "off")  # flagged: "12: server"
```

The test names the line and the name (`hlabd/api/servers.py:12: server`). The fix binds the
name again: `lab.set_status(lab.current(SERVERS, sid), "off")`. A deliberate exception
carries `# record: <why>` on the line of the use or right above it, for example a
running action, which stays in the state until it is finished.

**Undoing a failed step.** Code that records something before system work that may
fail wraps the work in `async with lab.undo_on_failure(undo):`. If it raises, `undo`
takes the change back on the system and in the state, and the error goes on.
`lab.put_back(KIND, id, field, old)` undoes a changed field. Only errors undo: a
cancelled step undoes nothing, because only a reset (which tears everything down) or
the daemon stopping (which keeps the state) cancels. Entering the block counts as an
await, so records inside it are looked up again.

## Recipes

**A new action**: `async def <command>(lab, resource, body) -> Plan` in the resource's
module, and a row `"command": ActionSpec("hetzner_name", function)`. Add `flag=False`
if it only changes the record, `answer="actions"` if Hetzner answers with a list. A new
in-between status goes into `IN_BETWEEN` (`lab.py`) and `status_after_restart`
(`api/restart.py`).

**A new field on a resource**: its TypedDict (`records.py`), the code that creates the
resource, its JSON shape (`api/hetzner/`), its PUT keys if it can change. The spec
check tells you if the shape is wrong. Old state files without it need a migration.

**Changing a record's shape** (a field added, renamed, moved, dropped, stored
differently):

1. `migrations.py`: `migrate_<n>_to_<n+1>(data)`, which changes the loaded JSON in
   place (IDs are strings there). It uses literal field names, not the current
   constants, and a docstring says what changed and why. A kind may be missing
   (`data.get("servers", {})`).
2. Append it to `MIGRATIONS`; `STATE_VERSION` follows.
3. `tests/test_migrations.py`: a test on data of the old shape, and the shape in
   `old_state()` for the chain test.

**Something new on the system**: a function with a header comment in
`scripts/hlab.sh` and its verb in `main`; a method of the right `fabric/` part that
runs it; the API calls the method through `lab.blocking`. What the method needs from
the state is computed by a function in `api/desired/` and passed as plain data.

**A new flag**: `Flag`, `FLAGS` and the comment above them in `lab.py` (so a restart
clears it), and `NotRequired[bool]` in the record's TypedDict.

**A new resource kind** (`tests/test_state.py` fails until steps 1 and 2 are done):

1. `records.py`: its TypedDict, added to `Record` (and `ActionRecord`,
   `ProtectedRecord` where they fit).
2. `state.py`: its `Kind` (name, record type, ID range, noun), added to
   `RECORD_KINDS`; the rest follows. No migration needed.
3. `api/<kind>.py` (checks, action table, routes), added to `create_app`; its JSON
   shape in `api/hetzner/`; its flag, if new.

## Tests

Which test catches a broken rule:

| Rule | Caught by |
|---|---|
| only the loop touches the state | every unit test (`state_owner_kept` in `tests/conftest.py`) |
| changes only inside `change()` blocks | every unit test (`State.verify_changes`) |
| no `change()` block awaits; a new kind is registered fully | `tests/test_state.py` |
| no record kept across an await | `tests/test_await_rule.py` |
| the fabric never sees a record | `tests/test_layers.py` |
| answers look like Hetzner's | the spec check of every API test (`tests/spec_check.py`) |
| every verb exists and takes fitting arguments | `tests/test_scripts.py` |
| `bin/hlab`'s usage names its commands | `tests/test_wrappers.py` |

- `tests/`: unit tests without VMs. `FakeShell` stands in for `hlab.sh` and records
  every call; `time_scale = 0` skips the waits. The `api` fixture runs the app on its
  own loop, which owns the state: tests touch it through `api.on_loop(work, *args)` and
  `state_of(api, "servers")`.
- `tests/test_wrappers.py`: every command `bin/hlab` dispatches is in its usage
  comment, and the other way round.
- `tests/test_scripts.py`: every verb hlabd calls exists, takes a fitting number of
  arguments and is its function's name with dashes; the nftables rulesets parse
  (fully only with CAP_NET_ADMIN, which `bin/hlab test` gives); `box-shell.sh` allows
  and denies the right commands.
- `itests/`: live tests with VMs (`bin/hlab itest`, ~15 min).
- `tests/tofu-smoke/run.sh`: OpenTofu with Hetzner's provider against a running lab.

`bin/hlab test` is the reference. It builds the development image from the working
copy and runs every unit test there; `bin/hlab lint` checks the mounted working copy,
so both see the same code. CI runs `bin/hlab test` on every push and pull request to
`main`; the live tests stay manual, since GitHub's runners have no `/dev/kvm`. CI may
turn red without a change here: the image fetches Hetzner's current specs, so a
changed spec shows where the lab falls behind. That is intended.

On Linux or macOS (Python 3.13+), the unit tests also run without Docker:

```bash
python3 -m venv .venv && .venv/bin/pip install -e '.[dev]'
python3 tools/fetch_specs.py   # Hetzner's specs into .cache/spec/
.venv/bin/pytest -q
```

The specs are looked up in `$HLAB_SPEC_DIR`, `/opt/hlab/spec`, then `.cache/spec/`.
Tests that need bash, nft, CAP_NET_ADMIN or /proc skip and say why. Set
`PYTHONDONTWRITEBYTECODE=1` to keep `__pycache__` out of the working copy.
