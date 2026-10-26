"""The core of the lab: its state, its fabric, and the actions.

This module connects `api/` (which decides) and `fabric/` (which does the work); every
API module gets the same `Lab` object. It runs the action tables (`ActionSpec`, `Plan`,
`act()`, `run_action()`), holds the action flags (`FLAGS`, `holding()`), runs blocking
work in threads (`blocking()`) and keeps Hetzner's timings.

The rules for the state, actions and flags are in DEVELOPING.md (Actions and flags,
Where code runs, The state).
"""

import asyncio
import logging
from collections.abc import Awaitable, Callable, Coroutine, Iterator
from contextlib import contextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Literal, cast, overload

from .errors import locked, not_found, unsupported
from .fabric import Fabric
from .records import (
    Action,
    ActionRecord,
    BoxSnapshot,
    Client,
    CloudNetwork,
    Firewall,
    FloatingIP,
    PrimaryIP,
    Record,
    Server,
    SSHKey,
    StorageBox,
    Subaccount,
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

# How long a lab reset waits for the threads of `blocking` (`stop_actions`). A script
# ends after its timeout (fabric/shell.py), so normally every thread has ended by then.
THREADS_WAIT_SECONDS = 150


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
    # checks the request for a record of the table's kind and plans the action; always
    # `async def`, so `act()` awaits every one alike (most never await: they only
    # check and change the state; some ask the system first, through `Lab.blocking`)
    plan: Callable[["Lab", R, dict], Awaitable[Plan]]
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
        with self.lab.state.change():
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
        # the running threads of `blocking` (a thread cannot be cancelled), each with
        # the name of its work for the log
        self.threads: dict[asyncio.Future, str] = {}
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
        self.threads[future] = getattr(work, "__qualname__", repr(work))
        future.add_done_callback(self.thread_done)
        # shielded: cancelling the caller must not mark the thread's future done early
        return await asyncio.shield(future)

    def thread_done(self, future: asyncio.Future) -> None:
        """Forget a thread of `blocking` that has returned."""
        self.threads.pop(future, None)
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
        write a file. So this also waits for every thread of `blocking`, at most
        THREADS_WAIT_SECONDS: a script that hangs past its timeout must not block a
        reset forever. Threads still running then are logged; otherwise no old action
        can change the lab afterwards.
        """
        tasks = list(self.tasks)
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        loop = asyncio.get_running_loop()
        deadline = loop.time() + THREADS_WAIT_SECONDS
        while self.threads and loop.time() < deadline:
            await asyncio.wait(list(self.threads), timeout=deadline - loop.time())
        if self.threads:
            log.warning(
                "a reset stopped waiting for threads that still run: %s",
                ", ".join(sorted(self.threads.values())),
            )

    # --- lookups ----------------------------------------------------------------------

    # one overload per kind, so a lookup returns the kind's record type (as in
    # `State.__getitem__`; tests/test_state.py compares them with `KIND_TYPES`)
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
    @overload
    def get_or_404(
        self, kind: Literal["actions"], item_id: int, what: str
    ) -> Action: ...
    @overload
    def get_or_404(
        self, kind: Literal["storage_boxes"], item_id: int, what: str
    ) -> StorageBox: ...
    @overload
    def get_or_404(
        self, kind: Literal["subaccounts"], item_id: int, what: str
    ) -> Subaccount: ...
    @overload
    def get_or_404(
        self, kind: Literal["box_snapshots"], item_id: int, what: str
    ) -> BoxSnapshot: ...
    @overload
    def get_or_404(
        self, kind: Literal["clients"], item_id: int, what: str
    ) -> Client: ...

    def get_or_404(self, kind: str, item_id: int, what: str) -> Record | Action:
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
        with self.state.change():
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
        """Create an action for a change that is already complete, and finish it."""
        with self.state.change():  # saved once, finished
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
        # The exit paths, and who finishes the action and frees the flags in each:
        # - the steps succeed: `run_steps`' finally block frees the flags and
        #   finishes the action (success), in one change block.
        # - the steps fail (Exception): the except block fails the next actions and
        #   moves the server out of its in-between status; then the same finally
        #   block frees the flags and finishes the action with the error.
        # - the steps are cancelled (a lab stop or reset, `stop_actions`): the
        #   finally block frees the flags and finishes the action with an error, and
        #   the cancellation goes on to the task.
        # - the task never starts (`spawn` raises): no finally block will run, so the
        #   except block below frees the flags and fails the action, then re-raises.
        # From here on only this function holds the flags (`holding.take()`); the
        # caller's `holding()` block no longer frees them.
        flags = holding.take() if holding else []

        def end(message: str | None) -> None:
            with self.state.change():  # saved once: flags off, action finished
                for item, flag in flags:
                    self.release(item, flag)
                self.finish(action, message)

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
                    alive = await self.blocking(self.fabric.vms.is_alive, server["id"])
                    self.set_status(server, "running" if alive else "off")
            except asyncio.CancelledError:
                message = "the action was cancelled (lab stopped or reset)"
                raise
            finally:
                end(message)

        steps_run = run_steps()
        try:
            self.spawn(steps_run)
        except BaseException:  # the steps never started: nothing else ends them
            steps_run.close()
            end("the action could not be started")
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

        The table's function (`async def`) checks the request and returns a `Plan`.
        Before that, the resource gets `flag` (423 with
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
            plan = await spec.plan(self, item, body)
            if plan.server is not None and plan.server is not item:
                held.claim(plan.server, "_locked", SERVER_LOCKED)
            resources = [(item["id"], item_type), *plan.resources]
            if plan.steps is None:
                with self.state.change():  # saved once: flags off, action finished
                    held.release()
                    action = self.finished_action(spec.name, resources)
            else:
                action = self.new_action(spec.name, resources)
                self.run_action(action, plan.steps, plan.server, holding=held)
        if spec.answer == "actions":
            with self.state.change():
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
        with self.state.change():
            fields[flag] = True

    def release(self, item: Record, flag: Flag) -> None:
        """Take a flag off a resource."""
        with self.state.change():
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
