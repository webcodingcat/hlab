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
                            change the state under `lab.state.lock`, start an action
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
| `bin/hlab`, `bin/hlab.ps1` | host side: build, start, stop, pass commands into the container |
| `scripts/hlab.sh` | low-level primitives: taps, QEMU, nftables, tc, DHCP, DNS, volumes, images |
| `scripts/hlab.nft` | the lab's nftables table `inet hlab`; `hlab.sh init-net` puts the addresses in and loads it |
| `scripts/box.sh` | the Storage Box: sshd, chrooted accounts, snapshots |
| `hlabd/` | the lab's daemon (below) |
| `tests/` | unit tests by feature, the OpenTofu smoke test |
| `itests/` | live tests with VMs |
| `tools/lint.sh` | the style and type checks of [STYLE.md](STYLE.md) |

There are two wrappers so that every host can drive the lab: `bin/hlab` for Bash
(Linux, macOS, Git Bash on Windows) and `bin/hlab.ps1` for Windows PowerShell without
Bash; they offer the same commands, so a change to one goes into the other too.

Inside `hlabd/`:

| Module | Job |
|---|---|
| `lab.py` | state, actions (`Plan`, `act`, `run_action`), the flags that stop two actions on one resource (`FLAGS`) |
| `state.py` | the state as JSON in the volume; resources by kind, plus the lab's own records (its docstring), the lock |
| `records.py` | the fields of every resource, the lab's own included (a server's `_lab`, its NICs): one TypedDict per kind, checked by mypy |
| `api/hetzner.py` | the JSON shapes of the Cloud API (`<thing>_json(lab, thing)`) |
| `api/<resource>.py` | one resource each: checks, its action table, routes |
| `api/nics.py` | a server's NICs in cloud networks: attach, detach, alias IPs (server actions, in `SERVER_ACTIONS`) |
| `api/public_addresses.py` | what primary and Floating IPs share: free addresses, reverse DNS |
| `api/storagebox/` | the Storage Box API (its own app, its own JSON shapes) |
| `api/restart.py` | what happens after the lab (re)starts: flags cleared, servers back |
| `api/control.py` | lab control under `/_lab/` (faults, clients, reset); no resource module imports it |
| `api/faults.py` | the faults and netem in the state, dropped with the server, client or link they point at |
| `fabric/` | carries out: `Fabric` (`__init__.py`) holds one object per part, each given what it uses in its constructor: `shell` (`shell.py`, runs hlab.sh), `vms` (`vm.py`), `volumes` (`volumes.py`), `faults` (`faults.py`: nft sets, latency), `network` (`network.py`: taps, DHCP, DNS, routes, Floating IPs, clients), `firewall` (`firewall.py`: the firewall table), `box` (`storagebox.py`); `addresses.py` has the address rules (MACs, gateways, tables). A VM's start and stop across the parts are `Fabric`'s own methods (`start_vm`, `kill_vm`, `delete_vm`) |
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
function gets `(lab, resource, body)`. It checks the request and returns a `lab.Plan`:

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
2. The route `server_action` (`api/servers.py`, `add_routes`) is `async def`: it awaits
   the body (`read_body`), then runs `run_server_action` in a thread
   (`await lab.blocking(...)`).
3. `run_server_action` looks the server up (`lab.server_or_404`: 404) and calls
   `lab.act(SERVER_ACTIONS, "poweroff", server, "server", body, "locked",
   SERVER_LOCKED)`.
4. `act()` finds `("stop_server", poweroff)` in the table (else `unsupported`), claims
   the server's `locked` flag (423 if another action runs) and calls
   `poweroff(lab, server, body)`. That returns `Plan(steps, server=server)`.
5. The plan has steps, so `act()` creates the action (`lab.new_action("stop_server",
   ...)`) and hands steps and flag to `lab.run_action`. It starts the steps as a task
   on the loop (`lab.spawn`). The route answers 201, the action `running`.
6. The steps run `await lab.blocking(lab.fabric.kill_vm, server)`. `Fabric.kill_vm`
   calls `vms.kill` (`hlab.sh kill-vm`) and `network.taps_down` (`hlab.sh
   tear-down-tap` per tap, then the DHCP config). The steps set the status to
   `stopping` and, after `POWEROFF_SECONDS`, to `off` (`lab.set_status` through
   `lab.blocking`), then wait `POWEROFF_END_SECONDS`.
7. When the steps end, `run_action` takes the `locked` flag off and finishes the action
   (`lab.finish`: `success`, or `error` with the message). If the steps failed, the
   server leaves `stopping` for what its VM really does.

## Where code runs

- Routes come in two forms. `def` routes run in FastAPI's worker threads. `async def`
  routes run on the event loop; they are used when the body must be awaited
  (`await read_body(request)`), and they hand the blocking work to
  `await lab.blocking(...)` (a thread), so the API keeps answering.
  For a new route: `def` when it needs no body, or only query and path parameters, and
  may block; `async def` with `await lab.blocking(...)` when it must await the body.
- Steps run as background tasks on the loop (`lab.spawn`); they call the fabric through
  `lab.blocking` too. So do the VM watcher and the snapshot plans. A thread cannot be
  cancelled, so `lab.blocking` keeps track of its threads, and a lab reset waits for
  them after cancelling the actions (`lab.stop_actions`).
- Code on the event loop never saves the state itself (a save writes and fsyncs the
  whole file): it hands whatever saves (`set_status`, `finish`, a `state.change()`
  block in a small function) to a thread with `await lab.blocking(...)`.
- `lab.api_fault` is read and changed only on the event loop (the middleware, async
  control routes), so the middleware's count of injected faults stays right.
- The waits of actions are Hetzner's measured timings, one table in `lab.py`
  (`POWEROFF_SECONDS`, ...), scaled by `time_scale` in `lab.sleep()`.

## State and locking

This is the one full explanation; `lab.py`, `state.py` and CLAUDE.md point here.

**Why.** Worker threads (routes, `lab.blocking`) and the event loop (steps, the VM
watcher) share one state, and `state.save()` writes all of it as JSON. A change from
another thread in the middle of a save, or of a loop, breaks it.

**Changes.** Every change to the state holds `lab.state.lock`: any dict or list
reachable from `state.data`, flags, statuses and the lab's own records included. That
holds in routes, action steps, the fabric and the restart alike. Slow work (scripts, VM
calls, `lab.sleep`) stays outside: take the lock only around the change and its save.
The lock is re-entrant, so a helper that locks may be called with it held.

A change that must be saved uses `state.change()`. It takes the lock and saves when the
block ends without an error, so a refused request saves nothing:

```python
with lab.state.change():
    volume["size"] = size
```

A block checks first, then changes. Nothing is rolled back: a block that raises after a
change leaves memory and file apart until the next save.

A plan's change that is done at once may use the plain lock, because `act()` saves it.
`lab.claim()`, `lab.release()` and `lab.new_action()` lock themselves.
`lab.set_status()` and `lab.finish()` lock and save, so on the event loop they go
through `lab.blocking` (see "Where code runs").

**Reads.** A loop over the state, or several reads that must fit together, hold the
lock. Without it, another thread breaks them ("dictionary changed size during
iteration", or a range and its gateway from two versions). A single lookup of one
record by key, or of one field, may go without. List routes build their whole answer
under the lock (`filtered`, the JSON shapes, `paginate`). The JSON shapes
(`api/hetzner.py`) and the lookups of `state.py` (`nics_in`, `server_by_name`, ...)
take the lock themselves, so any caller may use them. A loop that runs slow work (a
script per server) first copies what it needs under the lock (`list(...)`), then loops
over the copy without it.

**The check.** `state.lock` knows who holds it (`held_by_me()`): the thread, and on the
event loop the asyncio task. So one coroutine does not count as a holder while another
one holds the lock. `state.save()`, `state.new_id()` and the helpers that change
several records call `state.lock.assert_held()`, which raises AssertionError when the
caller does not hold the lock. Those helpers are `add_nic`, `setup_server_ips`,
`release_server_ips`, `record_attachment`, `checked_alias_ips`, `update_fields`,
`set_dns_ptr`, `check_limit`, the address pickers, the firewall ruleset and the fault
sets. The check costs two lookups, so it is always on. In an action's steps the error
only fails the action, so the unit tests record every breach and fail the test
(`locking_rule_kept` in `tests/conftest.py`). A new helper that expects its caller to
hold the lock says so and asserts it.

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

**A new flag**: add it to `FLAGS` in `lab.py` and to the list above it, so a restart
clears it (`api/restart.py`).

## The packet path of a public address

Who may send what, and where an address goes, comes from four places, each rebuilt as a
whole from the state when something changes:

- routes (`fabric/network.py`: `route_floating_ip`, `reroute_floating_ips`;
  `hlab.sh set-route`),
- the nft sets of `table inet hlab` (`fabric/faults.py`: `Faults.apply` – faults, the
  sources each tap may send from, cloud network members),
- the firewall table `inet hlabfw` (`fabric/firewall.py`: built by `ruleset`, applied
  by `Firewall.apply`),
- tc on the taps (`fabric/faults.py`: `Faults.apply_latency` – latency between
  locations, netem faults).

So moving a Floating IP changes its route and calls `faults.apply` (the new server may
send from it) and `faults.apply_latency`.

## Tests

- `tests/`: unit tests, no VMs. `FakeShell` (`tests/conftest.py`) stands in for
  `hlab.sh` and records every call; `time_scale = 0` skips Hetzner's waits.
  The `api` fixture is a test client of the Cloud API. Run with `bin/hlab test`, in the
  development image, which carries the unit tests (the lab image has only `itests/`).
- `tests/test_wrappers.py`: `bin/hlab` and `bin/hlab.ps1` dispatch and document the same
  commands and options (both read as text, the test container has no PowerShell);
  `bin/hlab` reads the lab name from sample configurations.
- `tests/test_scripts.py`: the scripts themselves, in seconds and without VMs: every
  verb hlabd calls exists in `hlab.sh`/`box.sh` with a fitting argument count (both
  sides parsed) and is its function's name with dashes, the nftables rulesets parse
  (`hlab.sh init-net`'s, rendered from `hlab.nft`, the faults' sets, the firewall
  table), `box-shell.sh`'s allow/deny decisions with stubbed commands,
  and `lib.sh`'s `stop_pid`/`is_alive`. `nft -c` checks fully only with
  CAP_NET_ADMIN, which `bin/hlab test` gives the test container; without it only nft's
  parser runs and the full check is skipped.
- `itests/`: live tests with VMs in a running lab (`bin/hlab itest`, ~15 min).
- `tests/tofu-smoke/run.sh`: OpenTofu with Hetzner's provider against a running lab.
