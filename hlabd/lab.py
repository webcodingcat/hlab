"""The lab's core: its state, the fabric that carries out changes, and actions.

`api/` decides what happens: it checks a request, changes the state and starts an
action. `fabric/` carries it out through scripts/*.sh. This module ties the two
together; every API module gets the one `Lab`.

Locking: every change to the state, and every loop over it, holds `state.lock`. Slow
work stays outside the lock. See DEVELOPING.md, State and locking.

Actions: every resource lists its actions in a table, `{command: (Hetzner's name,
function)}`. The function checks the request and returns a `Plan`; `act()` runs it the
same way for every resource. As on Hetzner, a change that is done at once still gets an
action, finished right away. Firewalls are the exception (`api/firewalls.py`).

Flags: a flag on a resource says that an action works on it, so a second one gets 423,
as on Hetzner. Code holds flags only through `holding()`: what is still held when the
block ends comes off, however it ends. `run_action()` takes over the flags of steps
that run in the background and takes them off when they end. The flags are listed at
`FLAGS`; DEVELOPING.md, Actions, shows how they flow.

Fields starting with `_` are the lab's own; the API never shows them.
"""

import asyncio
import logging
from collections.abc import Awaitable, Callable, Coroutine, Iterator
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from .api import system
from .errors import ApiError, locked, not_found, unsupported
from .fabric import Fabric
from .records import Action, ActionRecord, AnyRecord, Server
from .state import State, server_by_name

log = logging.getLogger(__name__)

# the slow part of an action, run in the background (`Plan.steps`, `run_action`)
type Steps = Callable[[], Awaitable[None]]

# a resource's action table: {Hetzner's command: (Hetzner's action name, function)};
# the function checks the request for a record of the table's kind and plans the action
type ActionTable[R: ActionRecord] = dict[
    str, tuple[str, Callable[[Lab, R, dict], Plan]]
]

# Finished actions kept in the state. Hetzner keeps every action; the lab keeps the
# recent ones only, because every save writes the whole state: above MAX_ACTIONS, the
# oldest finished ones go until KEPT_ACTIONS are left.
MAX_ACTIONS = 1000
KEPT_ACTIONS = 500

# The action flags. Each says that an action works on the resource (423 for another):
# - `locked`: on a server; the API shows it, as Hetzner's does (not in FLAGS: it is a
#   field of every server, set to False instead of removed)
# - `_busy`: a volume or Storage Box in an action
# - `_moving`: a Floating IP being moved
# - `_deleting`: a network or Storage Box being deleted
# - `_creating`: a sub-account being set up
# A lab restart takes them all off (`api/restart.py`, `clear_flags()`). A new flag goes
# here, so the restart clears it. `_copying` on a Storage Box snapshot is not an action
# flag: a restart removes such a snapshot, because its copy was cut off
# (`api/storagebox/__init__.py`, `restore()`).
FLAGS = ("_busy", "_moving", "_deleting", "_creating")

# actions that change only the record: they take no flag and may run alongside others
RECORD_ONLY = ("change_protection", "change_dns_ptr")

# the 423 message when an action already runs on the server
SERVER_LOCKED = "server is locked by another action"

# a server's in-between statuses; if an action fails, the server leaves them for the
# status its VM really has
IN_BETWEEN = ("initializing", "starting", "stopping", "rebuilding", "deleting")

# --- Hetzner's timings ----------------------------------------------------------------
# How long Hetzner Cloud takes, in seconds, as measured there; `Lab.sleep()` scales them
# by time_scale (0 in the unit tests). For example, a power off reports off after ~9 s
# and its action ends ~1 s later; a reset or reboot runs again after ~15 s.
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
    steps: Steps | None = None
    # fields the answer adds next to "action" (e.g. a new root password)
    extra: dict = field(default_factory=dict)
    # the server whose status the steps change: `act()` keeps it locked while they run
    # (423 if another action runs), and if they fail it leaves its in-between status
    server: Server | None = None
    # (id, type) of further resources the action concerns (e.g. the target server)
    resources: list[tuple[int, str]] = field(default_factory=list)


class HeldFlags:
    """The flags one piece of work holds, given out by `Lab.holding()`."""

    def __init__(self, lab: "Lab"):
        """Hold no flag yet."""
        self.lab = lab
        self.flags: list[tuple[AnyRecord, str]] = []

    def claim(self, item: AnyRecord, flag: str, message: str) -> None:
        """Set a flag on a resource and hold it, or raise 423 (`message`)."""
        self.lab.claim(item, flag, message)
        self.flags.append((item, flag))

    def release(self) -> None:
        """Take every held flag off, in the order they were claimed."""
        flags, self.flags = self.flags, []
        for item, flag in flags:
            self.lab.release(item, flag)

    def keep(self) -> None:
        """Leave the held flags set for good (on a record that is gone)."""
        self.flags = []

    def take(self) -> list[tuple[AnyRecord, str]]:
        """Hand the held flags over to whoever takes them off later (`run_action`)."""
        flags, self.flags = self.flags, []
        return flags


class Lab:
    """The lab's core: configuration, state, fabric, and the actions that run."""

    def __init__(self, cfg: dict, state: State, fabric: Fabric):
        """Set up the lab from its configuration, state and fabric."""
        self.cfg, self.state, self.fabric = cfg, state, fabric
        # factor on Hetzner's timings (0 = instant)
        self.scale = float(cfg["api"]["time_scale"])
        # injected API faults (/_lab/api-fault). Only code on the event loop reads or
        # changes it (the middleware, the async control routes): the middleware counts
        # it down, and a worker thread changing it meanwhile could undo that count.
        self.api_fault: dict[str, int] = {}
        self.clear_api_fault()
        # the running actions, as tasks on the event loop (`spawn`)
        self.tasks: set[asyncio.Task] = set()
        # the lab's own jobs for as long as it runs (daemon.run). Held here because
        # asyncio keeps tasks only weakly; not in `tasks`, so a reset leaves them
        self.jobs: set[asyncio.Task] = set()
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
        """Wait as long as Hetzner would (scaled by time_scale)."""
        if self.scale > 0:
            await asyncio.sleep(seconds * self.scale)

    async def blocking[**P, T](
        self, work: Callable[P, T], *args: P.args, **kwargs: P.kwargs
    ) -> T:
        """Run blocking work (scripts, files, saving the state) in a thread.

        So the API keeps answering. A cancelled caller stops waiting at once, but the
        thread runs on until `work` returns. It stays in `threads` until then, so a lab
        reset can wait for it.
        """
        future = asyncio.ensure_future(asyncio.to_thread(work, *args, **kwargs))
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

    def spawn(self, coro: Coroutine[Any, Any, None]) -> None:
        """Run a coroutine as a task on the lab's event loop, from any thread.

        The task is in `tasks` before this returns. A worker thread (a sync route) lets
        the loop create the task and waits until it has; so `tasks` holds only asyncio
        Tasks, and only the loop changes it.
        """
        try:
            asyncio.get_running_loop()
        # a worker thread: the loop is set at start (daemon.run; the test clients)
        except RuntimeError:
            assert self.loop is not None
            asyncio.run_coroutine_threadsafe(self.start_task(coro), self.loop).result()
            return
        self.track(asyncio.create_task(coro))

    async def start_task(self, coro: Coroutine[Any, Any, None]) -> None:
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
            if len(actions) > MAX_ACTIONS:
                finished = [
                    aid for aid, x in actions.items() if x["status"] != "running"
                ]
                for aid in finished[: len(actions) - KEPT_ACTIONS]:
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
        """Finish an action: success, or error with `message`, and save the state.

        It saves: on the event loop, call it through `blocking`.
        """
        with self.state.change():
            action.update(
                {
                    "progress": 100,
                    "finished": now(),
                    "status": "error" if message else "success",
                }
            )
            if message:
                action["error"] = {"code": "action_failed", "message": message}
        if message:
            log.warning(
                "action %s %s failed: %s", action["id"], action["command"], message
            )
        else:
            log.info("action %s %s done", action["id"], action["command"])

    def run_action(
        self,
        action: Action,
        steps: Steps,
        server: Server | None = None,
        next_actions: list[Action] | None = None,
        holding: HeldFlags | None = None,
    ) -> None:
        """Run an action's steps in the background, then finish the action.

        The flags `holding` holds are handed over: they stay set while the steps run,
        and this function takes them off when the steps end, however they end: success,
        failure, or cancelled (a lab reset). When the steps fail, the action ends with
        an error, its next actions fail with it, and the server (if any) leaves its
        in-between status (starting, rebuilding, ...) for what its VM really does:
        running or off.
        """
        flags = holding.take() if holding else []

        async def run_steps() -> None:
            message = None
            try:
                await steps()
            # reported through the action, as Hetzner does
            except Exception as error:
                log.exception("action %s failed", action["command"])
                message = str(error)
                for next_action in next_actions or []:
                    if next_action["status"] == "running":
                        await self.blocking(self.finish, next_action, message)
                if server is not None and server["status"] in IN_BETWEEN:
                    alive = await self.blocking(self.fabric.vms.is_alive, server)
                    await self.blocking(
                        self.set_status, server, "running" if alive else "off"
                    )
            except asyncio.CancelledError:
                message = "the action was cancelled (lab stopped or reset)"
                raise
            finally:
                for item, flag in flags:
                    self.release(item, flag)
                # saved with the flags off; the thread runs on even if the task is
                # cancelled again meanwhile, and a lab reset waits for it
                await self.blocking(self.finish, action, message)

        steps_run = run_steps()
        try:
            self.spawn(steps_run)
        except BaseException:  # never started: nothing else takes the flags off
            steps_run.close()
            for item, flag in flags:
                self.release(item, flag)
            raise

    def act[R: ActionRecord](
        self,
        actions: ActionTable[R],
        command: str,
        item: R,
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

        The flags are held through `holding()`: if the function refuses the request, or
        a flag is set already, they come off as the block ends. If the plan has no steps
        (done at once), they come off before the answer; otherwise `run_action()` takes
        them off when the steps end, whether they succeed, fail or are cancelled.
        """
        if command not in actions:
            raise unsupported(f"the action {command!r}")
        name, plan_action = actions[command]
        if command in RECORD_ONLY:
            flag = None
        with self.holding() as held:
            if flag:
                held.claim(item, flag, busy_message)
            plan = plan_action(self, item, body)
            if plan.server is not None and plan.server is not item:
                held.claim(plan.server, "locked", SERVER_LOCKED)
            resources = [(item["id"], item_type), *plan.resources]
            if plan.steps is None:
                with self.state.change():  # flags off, then the block saves
                    held.release()
                return {"action": self.finished_action(name, resources), **plan.extra}
            action = self.new_action(name, resources)
            self.run_action(action, plan.steps, plan.server, holding=held)
        return {"action": action, **plan.extra}

    @contextmanager
    def holding(self) -> Iterator[HeldFlags]:
        """Hold flags for a block: whatever is still held when it ends comes off.

        This is the one place that takes flags off on every path: a refused request,
        an error, or success. Flags handed to
        `run_action()` or kept (`HeldFlags.keep`) are no longer held here.
        """
        held = HeldFlags(self)
        try:
            yield held
        finally:
            held.release()

    def claim(self, item: AnyRecord, flag: str, message: str) -> None:
        """Set a flag on a resource, or raise 423 (`message`) if it is set already.

        The check and the change happen under one lock, so of two requests at the same
        time exactly one wins. Code outside this module claims through `holding()`.
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
        """Set a server's status and save the state.

        It saves: on the event loop, call it through `blocking`.
        """
        with self.state.change():
            server["status"] = status

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
            except Exception:  # the watcher keeps going; the error is in the log
                log.exception("VM watcher")

    def watch_vms(self) -> None:
        """Trim console logs and mark servers off whose VM has ended."""
        with self.state.lock:  # a copy: the loop runs scripts
            servers = list(self.state["servers"].values())
        for server in servers:
            self.fabric.vms.trim_console(server)
            if server["status"] == "running" and not server.get("locked"):
                self.stop_if_ended(server)

    def stop_if_ended(self, server: Server) -> None:
        """Mark a running server off if its VM has ended; remove its taps and DHCP."""
        if self.fabric.vms.is_alive(server):
            return
        with self.holding() as held:
            try:
                held.claim(server, "locked", SERVER_LOCKED)
            except ApiError:
                # an action started meanwhile: it owns the server now
                return
            if server["status"] != "running" or self.fabric.vms.is_alive(server):
                return
            log.info("server %s: its VM ended", server["name"])
            system.kill_vm(self, server)
            self.set_status(server, "off")
