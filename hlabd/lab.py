"""The core of the lab: its state, its fabric, and the actions.

`api/` decides what happens: it checks a request, changes the state and starts an
action. `fabric/` does the work on the system through scripts/*.sh. This module
connects the two. Every API module gets the same `Lab` object.

The event loop owns the state: all code that reads or changes it runs on the loop.
Blocking work (scripts, files) runs in threads through `blocking()`, with plain data
in and out. See DEVELOPING.md, State.

Actions: every resource lists its actions in a table, `{command: ActionSpec(Hetzner's
name, function)}`. The function checks the request and returns a `Plan`. It is
`async def` only when it must ask the system first. `act()` runs the plan in the same
way for every resource. On Hetzner, even a change that completes immediately gets an
action, which is finished right away; the lab does the same.

Flags: a flag on a resource says that an action is working on it, so a second action
gets 423, as on Hetzner. Code holds flags only through `holding()`. When the block
ends, every flag it still holds comes off, however the block ends. `run_action()`
takes over the flags of steps that run in the background and takes them off when the
steps end. The flags are listed at `FLAGS`; DEVELOPING.md, Actions, shows how they
flow.

Fields that start with `_` are used only by the lab. The API never shows them
directly; for example, a server's `_locked` is shown as `locked`.
"""

import asyncio
import inspect
import logging
from collections.abc import Awaitable, Callable, Coroutine, Iterator
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Literal, cast, overload

from .api import system
from .errors import ApiError, locked, not_found, unsupported
from .fabric import Fabric
from .records import (
    Action,
    ActionRecord,
    CloudNetwork,
    Firewall,
    FloatingIP,
    PrimaryIP,
    Record,
    Server,
    SSHKey,
    Volume,
)
from .state import KINDS, State, server_by_name

log = logging.getLogger(__name__)

# the slow part of an action, run in the background (`Plan.steps`, `run_action`)
type Steps = Callable[[], Awaitable[None]]

# How many finished actions the state keeps. Hetzner keeps every action. The lab keeps
# only the recent ones, because every save writes the whole state. When there are more
# than MAX_ACTIONS, the oldest finished ones are removed until KEPT_ACTIONS are left.
MAX_ACTIONS = 1000
KEPT_ACTIONS = 500

# The action flags. Each one says that an action is working on the resource, so
# another action gets 423. A flag is set to True and later removed; it is never set to
# False.
# - `_locked`: a server in an action; the API shows it as `locked` (`server_json` in
#   `api/hetzner.py`)
# - `_busy`: a volume or Storage Box in an action
# - `_moving`: a Floating IP that is being moved
# - `_deleting`: a network or Storage Box that is being deleted
# - `_creating`: a sub-account that is being set up
# A lab restart removes all of them (`Lab.clear_flags`). Add a new flag here, so that
# the restart clears it. `_copying` on a Storage Box snapshot is not an action flag.
# The restart removes such a snapshot, because its copy was cut off (`restore()` in
# `api/storagebox/__init__.py`).
type Flag = Literal["_locked", "_busy", "_moving", "_deleting", "_creating"]
FLAGS: tuple[Flag, ...] = ("_locked", "_busy", "_moving", "_deleting", "_creating")

# the 423 message when an action already runs on the server
SERVER_LOCKED = "server is locked by another action"

# a server's in-between statuses; if an action fails, the server gets the status that
# its VM really has instead
IN_BETWEEN = ("initializing", "starting", "stopping", "rebuilding", "deleting")

# --- Hetzner's timings ----------------------------------------------------------------
# How long Hetzner Cloud takes, in seconds, measured on Hetzner. `Lab.sleep()` scales
# them by time_scale (0 in the unit tests). For example, after a power off the server
# shows off after ~9 s, and the action ends ~1 s later. After a reset or reboot the
# server runs again after ~15 s.
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


def flag_fields(item: Record) -> dict[str, object]:
    """Return a record as a plain dict, so a flag can be set or removed by its name.

    Each record type declares its own flags (records.py). But when the flag's name is
    in a variable, mypy cannot match it to the record's fields.
    """
    return cast(dict[str, object], item)


@dataclass
class Plan:
    """The plan of an action: its slow part, extra answer fields, what it locks.

    The functions in the action tables return it, and `act()` runs it.
    """

    # the slow part, run in the background; None means the change is already complete
    steps: Steps | None = None
    # more fields for the answer, next to "action" (e.g. a new root password)
    extra: dict = field(default_factory=dict)
    # the server whose status the steps change: `act()` keeps it locked while they run
    # (423 if another action runs); if they fail, it leaves its in-between status
    server: Server | None = None
    # (id, type) of more resources the action is about (e.g. the target server)
    resources: list[tuple[int, str]] = field(default_factory=list)
    # more actions that the change causes, finished right away, after the action
    # itself: (Hetzner's name, its (id, type) resources), such as a firewall's
    # apply_firewall; only for a table that answers with a list of actions
    # (`ActionSpec.answer`)
    finished: list[tuple[str, list[tuple[int, str]]]] = field(default_factory=list)


@dataclass(frozen=True)
class ActionSpec[R: ActionRecord]:
    """One row of a resource's action table: what `act()` runs for a command."""

    # Hetzner's name of the action (`stop_server` for the command `poweroff`)
    name: str
    # checks the request for a record of the table's kind and plans the action; it is
    # `async def` when it must ask the system first (through `Lab.blocking`)
    plan: Callable[["Lab", R, dict], Plan | Awaitable[Plan]]
    # False: the action changes only the record; it takes no flag and may run
    # alongside another action (change_protection, change_dns_ptr)
    flag: bool = True
    # the key of the answer: one "action", or a list of "actions" (firewalls: the
    # action and `Plan.finished`)
    answer: Literal["action", "actions"] = "action"


# a resource's action table: {Hetzner's command: what `act()` runs for it}
type ActionTable[R: ActionRecord] = dict[str, ActionSpec[R]]


class HeldFlags:
    """The flags one piece of work holds, given out by `Lab.holding()`."""

    def __init__(self, lab: "Lab"):
        """Hold no flag yet."""
        self.lab = lab
        self.flags: list[tuple[Record, Flag]] = []

    def claim(self, item: Record, flag: Flag, message: str) -> None:
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

    def take(self) -> list[tuple[Record, Flag]]:
        """Hand the held flags over to whoever takes them off later (`run_action`)."""
        flags, self.flags = self.flags, []
        return flags


class Lab:
    """The core of the lab: configuration, state, fabric, and the running actions."""

    def __init__(self, cfg: dict, state: State, fabric: Fabric):
        """Set up the lab from its configuration, state and fabric."""
        self.cfg, self.state, self.fabric = cfg, state, fabric
        # factor on Hetzner's timings (0 = instant)
        self.scale = float(cfg["api"]["time_scale"])
        # injected API faults (/_lab/api-fault), counted down by the middleware
        self.api_fault: dict[str, int] = {}
        self.clear_api_fault()
        # the running actions, as tasks on the event loop (`spawn`)
        self.tasks: set[asyncio.Task] = set()
        # background jobs that run as long as the lab runs (daemon.run). They are held
        # here because asyncio keeps only weak references to tasks. They are not in
        # `tasks`, so a reset does not cancel them
        self.jobs: set[asyncio.Task] = set()
        # the running threads of `blocking` (a thread cannot be cancelled)
        self.threads: set[asyncio.Future] = set()
        # system image name → its file (loaded at start)
        self.images: dict[str, str] = {}
        # held while a whole picture of the system (fault sets, latency, firewall, DHCP,
        # routes) is computed and put in place, so that the newest picture always
        # comes last (api/system.py)
        self.picture_lock = asyncio.Lock()

    def own_state(self) -> None:
        """Make the running event loop the state's owner (daemon.run, test clients)."""
        asyncio.get_running_loop()  # raises outside the loop
        self.state.own()

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
        """Run blocking work (scripts, files) in a thread, so the API keeps answering.

        `work` gets plain data and returns plain data. It never gets the state, and it
        never reads a record field that can change, because only the loop may use the
        state. A cancelled caller stops waiting at once, but the thread keeps running
        until `work` returns. Until then it stays in `threads`, so a lab reset can wait
        for it.
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
            # read the error: a caller that was cancelled in the meantime never sees
            # it, and asyncio would log it as "never retrieved"
            future.exception()

    def spawn(self, coro: Coroutine[Any, Any, None]) -> None:
        """Run a coroutine as a task on the event loop, in `tasks` until it ends."""
        task = asyncio.create_task(coro)
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    async def stop_actions(self) -> None:
        """Cancel the running actions and wait until they and their threads have ended.

        A cancelled action ends at once. But a thread it started in `blocking` keeps
        running until its script returns, and that script may still start a VM or
        write a file. So this also waits for every thread of `blocking`. Afterwards no
        old action can change the lab.
        """
        tasks = list(self.tasks)
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        while self.threads:
            await asyncio.wait(list(self.threads))

    # --- lookups ----------------------------------------------------------------------

    # one overload per kind, so a lookup returns the kind's record type (as in
    # `State.__getitem__`)
    @overload
    def get_or_404(
        self, kind: Literal["servers"], item_id: int, what: str
    ) -> Server: ...
    @overload
    def get_or_404(
        self, kind: Literal["volumes"], item_id: int, what: str
    ) -> Volume: ...
    @overload
    def get_or_404(
        self, kind: Literal["primary_ips"], item_id: int, what: str
    ) -> PrimaryIP: ...
    @overload
    def get_or_404(
        self, kind: Literal["floating_ips"], item_id: int, what: str
    ) -> FloatingIP: ...
    @overload
    def get_or_404(
        self, kind: Literal["networks"], item_id: int, what: str
    ) -> CloudNetwork: ...
    @overload
    def get_or_404(
        self, kind: Literal["firewalls"], item_id: int, what: str
    ) -> Firewall: ...
    @overload
    def get_or_404(
        self, kind: Literal["ssh_keys"], item_id: int, what: str
    ) -> SSHKey: ...

    def get_or_404(self, kind: str, item_id: int, what: str) -> Record:
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
            finished = [aid for aid, x in actions.items() if x["status"] != "running"]
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
        """Create an action for a change that is already complete, and finish it."""
        action = self.new_action(command, resources)
        self.finish(action)
        return action

    def finish(self, action: Action, message: str | None = None) -> None:
        """Finish an action: success, or error with `message`, and save the state."""
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

        This function takes over the flags that `holding` holds. They stay set while
        the steps run, and this function takes them off when the steps end, however
        they end: success, failure, or cancelled (a lab reset). When the steps fail,
        the action ends with an error, and its next actions fail with it. The server,
        if there is one, then leaves its in-between status (starting, rebuilding, ...)
        and gets the status its VM really has: running or off.
        """
        flags = holding.take() if holding else []

        async def run_steps() -> None:
            message = None
            try:
                await steps()
            # Hetzner reports a failure through the action, and so does the lab
            except Exception as error:
                log.exception("action %s failed", action["command"])
                message = str(error)
                for next_action in next_actions or []:
                    if next_action["status"] == "running":
                        self.finish(next_action, message)
                if server is not None and server["status"] in IN_BETWEEN:
                    alive = await self.blocking(self.fabric.vms.is_alive, server)
                    self.set_status(server, "running" if alive else "off")
            except asyncio.CancelledError:
                message = "the action was cancelled (lab stopped or reset)"
                raise
            finally:
                for item, flag in flags:
                    self.release(item, flag)
                self.finish(action, message)  # saved with the flags off

        steps_run = run_steps()
        try:
            self.spawn(steps_run)
        except BaseException:  # the steps never started: nothing else frees the flags
            steps_run.close()
            for item, flag in flags:
                self.release(item, flag)
            raise

    async def act[R: ActionRecord](
        self,
        actions: ActionTable[R],
        command: str,
        item: R,
        item_type: str,
        body: dict,
        flag: Flag | None = None,
        busy_message: str = "",
    ) -> dict:
        """Run one action of a resource from its table and return the answer.

        The table's function checks the request and returns a `Plan`; if the function
        is `async def`, it is awaited. Before that, the resource gets `flag` (423 with
        `busy_message` if the flag is set already). An action whose row says
        `flag=False` takes no flag. If the plan names another server, that server gets
        its `_locked` flag too (423 if another action runs on it).

        The flags are held through `holding()`. If the function refuses the request,
        or a flag is set already, they come off when the block ends. If the plan has no
        steps, they come off before the answer. Otherwise `run_action()` takes them off
        when the steps end, whether the steps succeed, fail or are cancelled.

        The answer has the action under "action". If the row says `answer="actions"`,
        it has the action and the plan's `finished` actions under "actions" instead.
        The plan's `extra` fields follow.
        """
        if command not in actions:
            raise unsupported(f"the action {command!r}")
        spec = actions[command]
        with self.holding() as held:
            if flag and spec.flag:
                held.claim(item, flag, busy_message)
            planned = spec.plan(self, item, body)
            plan = await planned if inspect.isawaitable(planned) else planned
            if plan.server is not None and plan.server is not item:
                held.claim(plan.server, "_locked", SERVER_LOCKED)
            resources = [(item["id"], item_type), *plan.resources]
            if plan.steps is None:
                with self.state.change():  # flags off, then the block saves
                    held.release()
                action = self.finished_action(spec.name, resources)
            else:
                action = self.new_action(spec.name, resources)
                self.run_action(action, plan.steps, plan.server, holding=held)
        if spec.answer == "actions":
            finished = [self.finished_action(*more) for more in plan.finished]
            return {"actions": [action, *finished], **plan.extra}
        return {"action": action, **plan.extra}

    @contextmanager
    def holding(self) -> Iterator[HeldFlags]:
        """Hold flags for a block; every flag still held when the block ends comes off.

        This is the one place that takes flags off on every path: a refused request,
        an error, or success. Flags handed to `run_action()` or kept (`HeldFlags.keep`)
        are no longer held here.
        """
        held = HeldFlags(self)
        try:
            yield held
        finally:
            held.release()

    def claim(self, item: Record, flag: Flag, message: str) -> None:
        """Set a flag on a resource, or raise 423 (`message`) if it is set already.

        The check and the change run on the loop with no await between them. So when
        two requests come at the same time, exactly one of them wins. Code outside
        this module claims flags through `holding()`.
        """
        fields = flag_fields(item)
        if fields.get(flag):
            raise locked(message)
        fields[flag] = True

    def release(self, item: Record, flag: Flag) -> None:
        """Take a flag off a resource."""
        flag_fields(item).pop(flag, None)

    def clear_flags(self) -> None:
        """Take every action flag off every resource (a lab restart).

        No action survives a restart. State files written before `_locked` existed
        have a server field `locked` (True or False); this removes it too.
        """
        with self.state.change():
            for kind in KINDS:
                for item in self.state[kind].values():
                    for flag in (*FLAGS, "locked"):
                        flag_fields(item).pop(flag, None)

    def set_status(self, server: Server, status: str) -> None:
        """Set a server's status and save the state."""
        with self.state.change():
            server["status"] = status

    # --- watching VMs -----------------------------------------------------------------

    async def vm_watcher(self) -> None:
        """Check the VMs every five seconds, as long as the lab runs.

        When a guest powers itself off (after a shutdown, or on its own) or crashes,
        its server becomes off, as on Hetzner. The check also keeps console logs small.
        """
        while True:
            await asyncio.sleep(5)
            try:
                await self.watch_vms()
            except Exception:  # the watcher keeps going; the error is in the log
                log.exception("VM watcher")

    async def watch_vms(self) -> None:
        """Trim console logs and mark servers off whose VM has ended."""
        # a copy: servers may be deleted while the scripts run
        servers = list(self.state["servers"].values())
        for server in servers:
            await self.blocking(self.fabric.vms.trim_console, server)
            if server["status"] == "running" and not server.get("_locked"):
                await self.stop_if_ended(server)

    async def stop_if_ended(self, server: Server) -> None:
        """Mark a running server off if its VM has ended; remove its taps and DHCP."""
        if await self.blocking(self.fabric.vms.is_alive, server):
            return
        with self.holding() as held:
            try:
                held.claim(server, "_locked", SERVER_LOCKED)
            except ApiError:
                # an action started in the meantime: it owns the server now
                return
            if server["status"] != "running" or await self.blocking(
                self.fabric.vms.is_alive, server
            ):
                return
            log.info("server %s: its VM ended", server["name"])
            await system.kill_vm(self, server)
            self.set_status(server, "off")
