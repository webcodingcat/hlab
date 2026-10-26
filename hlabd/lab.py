"""The lab's core: its state, the fabric that carries out changes, and actions.

`api/` decides what happens (checks requests, changes the state, starts actions);
`fabric/` carries it out (VMs, taps, nftables, the Storage Box, through scripts/*.sh).
This module ties them together. Every API module gets one `Lab` and works the same way:
check the request, change the state under `state.lock`, and let an action do the slow
part (VMs, disks, networks) in the background.

The locking rule: every change to the state happens under `state.lock`. That covers any
dict or list reachable from `state.data`, flags and statuses included, in routes, action
steps, the fabric and the restart alike. Worker threads (routes, `blocking`) and the
event loop (steps, watchers) share the state, and `state.save()` writes it as JSON under
the lock, so a change outside it could break a save half way. A read that loops over the
state holds the lock too, or a change in another thread breaks the loop ("dictionary
changed size during iteration"). Slow work (scripts, VM calls, sleeps) stays outside the
lock: take it only around the change, the loop, or the save. The lock is re-entrant, so
helpers that lock may be called with it held.

Every resource lists its actions in a table, `{command: (Hetzner's name, function)}`.
The function checks the request and returns a `Plan`; `act()` runs it the same way for
every resource. A change that is done at once still gets an action, finished right
away, as on Hetzner. Firewalls are the exception: `FIREWALL_ACTIONS` maps each command
straight to a function that returns Hetzner's actions itself (`api/firewalls.py`).

Flags stop two actions from working on one resource at the same time. An action sets
a flag on its resource (`claim()`; 423 if the flag is set already, as on Hetzner). The
flag comes off when the action's steps end (`run_action(..., holding=...)`), or at once
when nothing was started (`release()`). `act()` does this for the resource and for the
server its plan names. A few places do it by hand, without `act()`: creating and
deleting a server, creating a volume for a server, deleting a volume, deleting a
network, deleting a Storage Box, creating a sub-account, and the VM watcher. The flags:

- `locked`: a server with an action running (shown by the API, as on Hetzner).
- the others, in `FLAGS`: `_moving`: a Floating IP being moved; `_busy`: a volume or
  Storage Box in an action; `_deleting`: a network or Storage Box being deleted;
  `_creating`: a sub-account being set up.

Fields starting with `_` are the lab's own and never shown by the API. A restart of the
lab clears the flags of actions (`api/restart.py`, `clear_flags()`). A Storage Box
snapshot being copied is marked `_copying`; it is no action flag: a restart removes such
a snapshot, which was cut off (`api/storagebox/__init__.py`, `restore()`).
"""

import asyncio
import logging
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import dataclass, field
from datetime import datetime, timezone

from .errors import ApiError, locked, not_found, unsupported
from .fabric import Fabric
from .records import Action, AnyRecord, Server
from .state import State, server_by_name

log = logging.getLogger(__name__)

# the action flags besides a server's `locked` (see the module docstring)
FLAGS = ("_busy", "_moving", "_deleting", "_creating")

# actions that change only the record: they take no flag and run beside the others
RECORD_ONLY = ("change_protection", "change_dns_ptr")

# the answer when a server has an action running
SERVER_LOCKED = "server is locked by another action"

# the in-between statuses of a server, left for what its VM does when an action fails
IN_BETWEEN = ("initializing", "starting", "stopping", "rebuilding", "deleting")

# --- the cloud's timings --------------------------------------------------------------
# How long Hetzner Cloud takes, in seconds, as measured there; `Lab.sleep()` scales them
# by time_scale (0 in the unit tests). A power off, for one, reports off after ~9 s and
# its action ends ~1 s later; a reset or reboot runs again after ~15 s.
CREATE_START_SECONDS = 5  # a new server: starting → running
DELETE_SECONDS = 2  # a server's VM is gone → its delete action ends
POWEROFF_SECONDS = 8.7  # power off: stopping → off
POWEROFF_END_SECONDS = 1.2  # power off: off → the action ends
SHUTDOWN_SECONDS = 1  # shutdown: the power button is pressed → the action ends
POWERON_SECONDS = 7  # power on: starting → running
POWERON_END_SECONDS = 1  # power on: running → the action ends
RESTART_SECONDS = 13  # reboot or reset: starting → running
RESTART_END_SECONDS = 2  # reboot or reset: running → the action ends
REBUILD_SECONDS = 10  # rebuild: the new disk is there → the action ends
CHANGE_TYPE_SECONDS = 30  # change_type: the new type is set → the action ends
NIC_ATTACH_SECONDS = 3  # attach_to_network: the NIC is in → the action ends
NIC_DETACH_SECONDS = 2  # detach_from_network: the NIC is out → the action ends
ALIAS_IPS_SECONDS = 2  # change_alias_ips: routed → the action ends
VOLUME_SECONDS = 2  # a volume's create, attach, detach, resize: done → the action ends
FLOATING_IP_MOVE_SECONDS = 3  # a Floating IP's route moves to its new server
BOX_CREATE_SECONDS = 5  # a Storage Box: its account is set up → active


def now() -> str:
    """Return a timestamp as the API shows it."""
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


@dataclass
class Plan:
    """The plan of an action: its slow part, extra answer fields, what it locks.

    The functions of the action tables return it; `act()` carries it out.
    """

    # the slow part, run in the background; None: the change is done already
    steps: Callable[[], Awaitable[None]] | None = None
    # fields the answer adds next to "action" (e.g. a new root password)
    extra: dict = field(default_factory=dict)
    # the server whose status the steps change: `act()` keeps it locked while they run
    # (423 if another action runs), and if they fail it leaves its in-between status
    server: Server | None = None
    # (id, type) of further resources the action concerns (e.g. the target server)
    resources: list[tuple[int, str]] = field(default_factory=list)


class Lab:
    """The lab's core: configuration, state, fabric, and the actions that run."""

    def __init__(self, cfg: dict, state: State, fabric: Fabric):
        """Set up the lab from its configuration, state and fabric."""
        self.cfg, self.state, self.fabric = cfg, state, fabric
        # factor on the cloud's timings (0 = instant)
        self.scale = float(cfg["api"]["time_scale"])
        # injected API faults (/_lab/api-fault). Only code on the event loop reads or
        # changes it (the middleware, the async control routes): the middleware counts
        # it down, and a worker thread changing it meanwhile could undo that count.
        self.api_fault: dict[str, int] = {}
        self.clear_api_fault()
        # the running actions, as tasks on the event loop (`spawn`)
        self.tasks: set[asyncio.Task] = set()
        # the running threads of `blocking` (a thread cannot be cancelled)
        self.threads: set[asyncio.Future] = set()
        # system image name → its file (loaded at start)
        self.images: dict[str, str] = {}
        self.loop: asyncio.AbstractEventLoop | None = None

    def clear_api_fault(self) -> None:
        """Answer API requests normally again: no injected status, no delay."""
        self.api_fault.update(status=0, count=0, delay_ms=0)

    # --- running things ---------------------------------------------------------------

    async def sleep(self, seconds: float) -> None:
        """Wait as long as the cloud would (scaled by time_scale)."""
        if self.scale > 0:
            await asyncio.sleep(seconds * self.scale)

    async def blocking(self, fn, *args, **kwargs):
        """Run shell work (scripts, files) in a thread, so the API keeps answering.

        A cancelled caller stops waiting at once, but the thread runs on until `fn`
        returns. It stays in `threads` until then, so a lab reset can wait for it.
        """
        future = asyncio.ensure_future(asyncio.to_thread(fn, *args, **kwargs))
        self.threads.add(future)
        future.add_done_callback(self.thread_done)
        # shielded: cancelling the caller must not mark the thread's future done early
        return await asyncio.shield(future)

    def thread_done(self, future: asyncio.Future) -> None:
        """Forget a thread of `blocking` that has returned."""
        self.threads.discard(future)
        if not future.cancelled():
            # a caller that was cancelled meanwhile never sees the error, and asyncio
            # would log it as "never retrieved"
            future.exception()

    def spawn(self, coro: Coroutine) -> None:
        """Run a coroutine as a task on the lab's event loop, from any thread.

        The task is in `tasks` before this returns. A worker thread (a sync route) has
        the loop create the task and waits for that; so `tasks` holds only asyncio
        Tasks, and only the loop changes it.
        """
        try:
            asyncio.get_running_loop()
        except RuntimeError:  # a worker thread: the loop is set at start (daemon.run)
            assert self.loop is not None
            asyncio.run_coroutine_threadsafe(self.start_task(coro), self.loop).result()
            return
        self.track(asyncio.create_task(coro))

    async def start_task(self, coro: Coroutine) -> None:
        """Create a task for a coroutine on the running loop (see `spawn`)."""
        self.track(asyncio.create_task(coro))

    def track(self, task: asyncio.Task) -> None:
        """Keep a task in `tasks` until it ends."""
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    async def stop_actions(self) -> None:
        """Cancel the running actions and wait until they and their threads have ended.

        A cancelled action ends at once, but a thread it started in `blocking` runs on
        until its script returns, and that script may still start a VM or write a
        file. So this also waits for every thread of `blocking`. Afterwards no old
        action changes the lab any more.
        """
        tasks = list(self.tasks)
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        while self.threads:
            await asyncio.wait(list(self.threads))

    # --- lookups ----------------------------------------------------------------------

    def get_or_404(self, kind: str, item_id: int, what: str) -> AnyRecord:
        """Return a resource of a kind (e.g. "volumes") by ID, or raise 404 (`what`)."""
        item = self.state[kind].get(item_id)
        if not item:
            raise not_found(what)
        return item

    def server_or_404(self, sid: int) -> Server:
        """Return the server with this ID, or raise 404."""
        return self.get_or_404("servers", sid, "server")

    def server_by_name_or_404(self, name: str) -> Server:
        """Return the server with this name, or raise 404."""
        server = server_by_name(self.state, name)
        if not server:
            raise not_found(f"server {name!r}")
        return server

    # --- actions ----------------------------------------------------------------------

    def new_action(self, command: str, resources: list[tuple[int, str]]) -> Action:
        """Create a new action in status running, for the given (id, type) resources."""
        with self.state.lock:
            action: Action = {
                "id": self.state.new_id("actions"),
                "command": command,
                "status": "running",
                "progress": 0,
                "started": now(),
                "finished": None,
                "error": None,
                "resources": [{"id": i, "type": t} for i, t in resources],
            }
            actions = self.state["actions"]
            actions[action["id"]] = action
            # the state stays small: the oldest finished actions are removed
            if len(actions) > 10000:
                finished = [
                    aid for aid, x in actions.items() if x["status"] != "running"
                ]
                for aid in finished[: len(actions) - 5000]:
                    del actions[aid]
        log.info(
            "action %s %s %s",
            action["id"],
            command,
            " ".join(f"{t}:{i}" for i, t in resources),
        )
        return action

    def finished_action(self, command: str, resources: list[tuple[int, str]]) -> Action:
        """Create an action for a change that is done already, and finish it."""
        action = self.new_action(command, resources)
        self.finish(action)
        return action

    def finish(self, action: Action, message: str | None = None) -> None:
        """Finish an action: success, or error with `message`."""
        with self.state.lock:
            action.update(
                {
                    "progress": 100,
                    "finished": now(),
                    "status": "error" if message else "success",
                }
            )
            if message:
                action["error"] = {"code": "action_failed", "message": message}
            self.state.save()
        if message:
            log.warning(
                "action %s %s failed: %s", action["id"], action["command"], message
            )
        else:
            log.info("action %s %s done", action["id"], action["command"])

    def run_action(
        self,
        action: Action,
        steps,
        server: Server | None = None,
        next_actions: list[Action] | None = None,
        holding: list[tuple[AnyRecord, str]] | None = None,
    ) -> None:
        """Run an action's steps in the background, then finish the action.

        The flags in `holding` stay set while the steps run. This function takes them
        off when the steps end, however they end: success, failure, or cancelled (a lab
        reset). When the steps fail, the action ends with an error, its next actions
        fail with it, and the server (if any) leaves its in-between status (starting,
        rebuilding, ...) for what its VM really does: running or off.
        """

        async def run_steps():
            message = None
            try:
                await steps()
            # reported through the action, like the cloud does
            except Exception as error:
                log.exception("action %s failed", action["command"])
                message = str(error)
                for next_action in next_actions or []:
                    if next_action["status"] == "running":
                        self.finish(next_action, message)
                if server is not None and server["status"] in IN_BETWEEN:
                    alive = await self.blocking(self.fabric.vm_alive, server)
                    self.set_status(server, "running" if alive else "off")
            except asyncio.CancelledError:
                message = "the action was cancelled (lab stopped or reset)"
                raise
            finally:
                for item, flag in holding or []:
                    self.release(item, flag)
                self.finish(action, message)

        self.spawn(run_steps())

    def act(
        self,
        actions: dict,
        command: str,
        item: AnyRecord,
        item_type: str,
        body: dict,
        flag: str | None = None,
        busy_message: str = "",
    ) -> dict:
        """Run one action of a resource from its table and return the answer.

        The table's function checks the request and returns a `Plan`. Before that, the
        resource gets `flag` (423 `busy_message` if it is set already); a record-only
        action (`RECORD_ONLY`) takes no flag. If the plan names another server, that
        server gets its `locked` flag too (423 if another action runs on it).

        Who takes the flags off: if the function refuses the request, or a flag is set
        already, `act()` takes off what it set and re-raises. If the plan has no steps
        (done at once), `act()` takes them off before it answers. Otherwise it hands
        them to `run_action()`, which takes them off when the steps end, whether they
        succeed, fail or are cancelled.
        """
        if command not in actions:
            raise unsupported(f"the action {command!r}")
        name, plan_action = actions[command]
        if command in RECORD_ONLY:
            flag = None
        holding: list[tuple[AnyRecord, str]] = []
        try:
            if flag:
                self.claim(item, flag, busy_message)
                holding.append((item, flag))
            plan = plan_action(self, item, body)
            if plan.server is not None and plan.server is not item:
                self.claim(plan.server, "locked", SERVER_LOCKED)
                holding.append((plan.server, "locked"))
        except BaseException:  # refused: nothing runs, everything is free again
            for held, held_flag in holding:
                self.release(held, held_flag)
            raise
        resources = [(item["id"], item_type), *plan.resources]
        if plan.steps is None:
            for held, held_flag in holding:
                self.release(held, held_flag)
            self.state.save()
            return {"action": self.finished_action(name, resources), **plan.extra}
        action = self.new_action(name, resources)
        self.run_action(action, plan.steps, plan.server, holding=holding)
        return {"action": action, **plan.extra}

    def claim(self, item: AnyRecord, flag: str, message: str) -> None:
        """Set a flag on a resource, or raise 423 (`message`) if it is set already.

        The check and the change happen under one lock, so of two requests at the same
        time exactly one wins.
        """
        with self.state.lock:
            if item.get(flag):
                raise locked(message)
            item[flag] = True

    def release(self, item: AnyRecord, flag: str = "locked") -> None:
        """Take a flag off a resource (a server's `locked` by default)."""
        with self.state.lock:
            if flag == "locked":
                item["locked"] = False  # shown by the API
            else:
                item.pop(flag, None)

    def set_status(self, server: Server, status: str) -> None:
        """Set a server's status and save the state."""
        with self.state.lock:
            server["status"] = status
            self.state.save()

    # --- watching VMs -----------------------------------------------------------------

    async def vm_watcher(self) -> None:
        """Check the VMs every five seconds, as long as the lab runs.

        A guest that powers itself off (after a shutdown, or on its own) or crashes
        leaves its server off, as on Hetzner; console logs stay small.
        """
        while True:
            await asyncio.sleep(5)
            try:
                await self.blocking(self.watch_vms)
            except Exception:  # the watcher keeps going; the log has it
                log.exception("VM watcher")

    def watch_vms(self) -> None:
        """Trim console logs and mark servers off whose VM has ended."""
        with self.state.lock:  # a copy: the loop runs scripts
            servers = list(self.state["servers"].values())
        for server in servers:
            self.fabric.trim_console(server)
            if server["status"] == "running" and not server.get("locked"):
                self.stop_if_ended(server)

    def stop_if_ended(self, server: Server) -> None:
        """Mark a running server off if its VM has ended; its taps and DHCP go too."""
        if self.fabric.vm_alive(server):
            return
        try:
            self.claim(server, "locked", SERVER_LOCKED)
        except ApiError:
            # an action started meanwhile: it owns the server now
            return
        try:
            if server["status"] != "running" or self.fabric.vm_alive(server):
                return
            log.info("server %s: its VM ended", server["name"])
            self.fabric.kill_vm(server)
            self.set_status(server, "off")
        finally:
            self.release(server)
