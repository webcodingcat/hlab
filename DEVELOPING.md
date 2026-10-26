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
| `Dockerfile` | the lab image (Debian 13 packages: QEMU, dnsmasq, nftables, OpenSSH, Python/FastAPI) and the development image on top |
| `bin/hlab`, `bin/hlab.ps1` | host side: build, start, stop, pass commands into the container |
| `scripts/hlab.sh` | low-level primitives: taps, QEMU, nftables, tc, DHCP, DNS, volumes, images |
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
| `lab.py` | state, actions (`Plan`, `act`, `run_action`), the flags that stop two actions on one resource (read its docstring first) |
| `state.py` | the state as JSON in the volume; resources by kind, plus the lab's own records (read its docstring) |
| `records.py` | the fields of every resource, the lab's own included (a server's `_lab`, its NICs): one TypedDict per kind, checked by mypy |
| `api/hetzner.py` | the JSON shapes of the Cloud API (`<thing>_json(lab, thing)`) |
| `api/<resource>.py` | one resource each: checks, its action table, routes |
| `api/storagebox/` | the Storage Box API (its own app, its own JSON shapes) |
| `api/restart.py` | what happens after the lab (re)starts: flags cleared, servers back |
| `api/control.py` | lab control under `/_lab/` (faults, clients, reset); no resource module imports it |
| `api/faults.py` | the faults and netem in the state, dropped with the server, client or link they point at |
| `fabric/` | carries out: `vm.py`, `network.py`, `volumes.py`, `faults.py` (nft sets, latency), `firewall.py` (the firewall table), `storagebox.py`, `shell.py` (runs hlab.sh); `base.py` declares what the mixins share |
| `config.py` | configuration defaults and the lab's addresses (the scripts get them as `HLAB_*`) |
| `daemon.py`, `cli.py` | the container's main process; the `hlab` command inside the container |

## Actions

Every resource lists its actions in a table, `{command: (Hetzner's name, function)}`:
`SERVER_ACTIONS` (`api/servers.py`), `VOLUME_ACTIONS` (`api/volumes.py`),
`PRIMARY_IP_ACTIONS` and `FLOATING_IP_ACTIONS` (`api/ips.py`), `NETWORK_ACTIONS`
(`api/networks.py`), `BOX_ACTIONS` and `SUBACCOUNT_ACTIONS` (`api/storagebox/`);
`FIREWALL_ACTIONS` (`api/firewalls.py`) maps each command straight to its function,
which returns Hetzner's actions itself (below). Each function is named after its command
(`reboot`, `add_subnet`); where a module holds the same command for several
resources, the name is `<resource>_<command>` (`primary_ip_assign`,
`floating_ip_assign`). It gets `(lab, resource, body)`, checks the request and returns a
`lab.Plan`:

- `steps`: an async function doing the slow part through `lab.fabric`, or `None` when
  the change is done already (the action then finishes at once);
- `extra`: fields the answer adds (e.g. `root_password`);
- `server`: the server the steps work on (locked while they run; if they fail, it
  leaves its in-between status);
- `resources`: further resources the action concerns.

`lab.act()` runs these tables the same way: it marks the resource with its flag (423 if
it is marked already), calls the function, locks the plan's server if that is another
resource (423 if an action of it runs), creates the action and runs the steps; the flags
are taken off when the steps end. So an action function never sets a flag itself.
Actions that only change the record (`change_protection`, `change_dns_ptr`) take no
flag. The delete protection of networks, primary IPs, Floating IPs, volumes and Storage
Boxes is one function, `api/web.py`: `change_delete_protection`. Firewalls are the one
exception: `FIREWALL_ACTIONS` is `{command: function}` and not run by `lab.act()`; its
functions return Hetzner's finished actions themselves, several at once, as on Hetzner.

New code uses `act()`. Setting flags by hand (`lab.claim()`, `lab.release()`) is only
for creating or deleting a resource, which is no Hetzner action on an existing one, and
for the places `lab.py`'s docstring lists: creating and deleting a server, creating a
volume for a server, deleting a volume, deleting a network, deleting a Storage Box,
creating a sub-account, and the VM watcher.

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
- `lab.api_fault` is read and changed only on the event loop (the middleware, async
  control routes), so the middleware's count of injected faults stays right.
- The waits of actions are Hetzner's measured timings, one table in `lab.py`
  (`POWEROFF_SECONDS`, ...), scaled by `time_scale` in `lab.sleep()`.

## State and locking

Worker threads and the event loop share one state, and `state.save()` writes all of it
as JSON. So the rule is: every change to the state – any dict or list reachable from
`state.data`, flags, statuses and the lab's own records included – happens under
`lab.state.lock`, in routes, action steps, the fabric and the restart alike.
`lab.claim()`, `lab.release()`, `lab.set_status()`, `lab.new_action()` and
`lab.finish()` take it themselves. Slow work (scripts, VM calls, `lab.sleep`) stays
outside: take the lock only around the change and its save. The lock is re-entrant, so a
helper that locks may be called with it held; helpers that change several records at
once (`add_nic`, `setup_server_ips`, ...) say that their caller holds it.

Reads follow one rule too: a loop over the state holds `lab.state.lock` while it loops,
or another thread changing the state breaks it ("dictionary changed size during
iteration"). List routes build their whole answer under the lock (`filtered`, the
JSON shapes, `paginate`); the JSON shapes (`api/hetzner.py`) and the lookups of
`state.py` (`nics_in`, `server_by_name`, ...) take it themselves, so any caller may use
them. A loop that runs slow work (a script per server) first copies what it needs under
the lock (`list(...)`), then loops over the copy without it.

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

**Something new on the system** (a tap, a route, a file): add a verb to
`scripts/hlab.sh` (a function with a header comment), call it from a `fabric/` method,
and call that method from the API. Addresses come from `config.py` as `HLAB_*`.

**A new flag**: name it in `lab.py`'s docstring and in its `FLAGS`, so a restart clears
it (`api/restart.py`).

## The packet path of a public address

Who may send what, and where an address goes, comes from four places, each rebuilt as a
whole from the state when something changes:

- routes (`fabric/network.py`: `route_floating_ip`, `reroute_floating_ips`; `hlab.sh route`),
- the nft sets of `table inet hlab` (`fabric/faults.py`: `apply_faults` – faults, the
  sources each tap may send from, cloud network members),
- the firewall table `inet hlabfw` (built by `fabric/firewall.py`: `ruleset`, applied
  by `fabric/network.py`: `apply_firewalls`),
- tc on the taps (`fabric/faults.py`: `apply_latency` – latency between locations,
  netem faults).

So moving a Floating IP changes its route and calls `apply_faults` (the new server may
send from it) and `apply_latency`.

## Tests

- `tests/`: unit tests, no VMs. `FakeShell` (`tests/conftest.py`) stands in for
  `hlab.sh` and records every call; `time_scale = 0` makes the cloud's waiting instant.
  The `api` fixture is a test client of the Cloud API. Run with `bin/hlab test`.
- `tests/test_scripts.py`: the scripts themselves, in seconds and without VMs: every
  verb hlabd calls exists in `hlab.sh`/`box.sh` with a fitting argument count (both
  sides parsed), the nftables rulesets parse (`hlab.sh net-init`'s, the faults' sets,
  the firewall table), `box-shell.sh`'s allow/deny decisions with stubbed commands,
  and `lib.sh`'s `stop_pid`/`is_alive`. `nft -c` checks fully only with
  CAP_NET_ADMIN (`docker run --cap-add NET_ADMIN`); without it, as in `bin/hlab test`,
  only nft's parser runs and the full check is skipped.
- `itests/`: live tests with VMs in a running lab (`bin/hlab itest`, ~15 min).
- `tests/tofu-smoke/run.sh`: OpenTofu with Hetzner's provider against a running lab.
