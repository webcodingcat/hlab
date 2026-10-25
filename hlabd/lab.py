"""The lab's core: its state, the fabric that carries out changes, and actions.

`api/` decides what happens (checks requests, changes the state, starts actions);
`fabric/` carries it out (VMs, taps, nftables, the Storage Box, through scripts/*.sh).
This module ties them together: every API module gets one `Lab` and works the same way –
check the request, change the state under `state.lock`, and let an action do the slow
part (VMs, disks, networks) in the background.

How things are kept apart while an action runs:

- A server has `locked`: set by `reserve()` before an action starts (423 if one already
  runs, as on Hetzner) and cleared by `run_action()` when the action's steps end, or by
  `release()` when nothing was started after all.
- Other resources mark themselves for the few moments that need it: `_moving` (a
  Floating IP being moved), `_busy` (a volume in an action, a Storage Box being rolled
  back), `_deleting` (a network or Storage Box being deleted), `_creating` (a
  sub-account being set up), `_copying` (a Storage Box snapshot being copied). Fields
  starting with `_` are the lab's own and never shown by the API; a restart of the lab
  clears them (api/restart.py).

Changes that are done at once still get an action, finished right away (`done()`), as on
Hetzner.
"""

import asyncio
import logging
from datetime import datetime, timezone

from .errors import ApiError, locked, not_found
from .fabric import Fabric

log = logging.getLogger("hlabd.lab")


def now() -> str:
    """Return a timestamp as the API shows it."""
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat()


class Lab:
    """The lab's core: configuration, state, fabric, and the actions that run."""

    def __init__(self, cfg: dict, state, fabric: Fabric):
        """Set up the lab from its configuration, state and fabric."""
        self.cfg, self.state, self.fabric = cfg, state, fabric
        # factor on the cloud's timings (0 = instant)
        self.scale = float(cfg["api"]["time_scale"])
        # injected API faults (/_lab/api-fault)
        self.fault = {
            "status": 0,
            "count": 0,
            "delay_ms": 0,
        }
        self.tasks: set = set()  # running background tasks (actions)
        # system image name → its file (loaded at start)
        self.images: dict[str, str] = {}
        self.loop: asyncio.AbstractEventLoop | None = None

    # --- running things ---------------------------------------------------------------

    async def sleep(self, seconds: float) -> None:
        """Wait as long as the cloud would (scaled by time_scale)."""
        if self.scale > 0:
            await asyncio.sleep(seconds * self.scale)

    async def blocking(self, fn, *args, **kwargs):
        """Run shell work (scripts, files) in a thread, so the API keeps answering."""
        return await asyncio.to_thread(fn, *args, **kwargs)

    def spawn(self, coro) -> None:
        """Run a coroutine on the server's event loop.

        This works also from the worker threads of sync endpoints.
        """
        try:
            task = asyncio.get_running_loop().create_task(coro)
        except RuntimeError:
            task = asyncio.run_coroutine_threadsafe(coro, self.loop)
        self.tasks.add(task)
        task.add_done_callback(self.tasks.discard)

    # --- actions ----------------------------------------------------------------------

    def new_action(self, command: str, resources: list[tuple[int, str]]) -> dict:
        """Create a new action in status running, for the given (id, type) resources."""
        with self.state.lock:
            action = {
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
            # the state stays small: the oldest finished actions go
            if len(actions) > 10000:
                for aid in [i for i, x in actions.items() if x["status"] != "running"][
                    : len(actions) - 5000
                ]:
                    del actions[aid]
        log.info(
            "action %s %s %s",
            action["id"],
            command,
            " ".join(f"{t}:{i}" for i, t in resources),
        )
        return action

    def done(self, command: str, resources: list[tuple[int, str]]) -> dict:
        """Create an action for a change that is done at once."""
        action = self.new_action(command, resources)
        self.finish(action)
        return action

    def finish(self, action: dict, message: str | None = None) -> None:
        """Finish an action: success, or error with `message`."""
        with self.state.lock:
            action.update(
                progress=100, finished=now(), status="error" if message else "success"
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
        action: dict,
        steps,
        server: dict | None = None,
        next_actions: list[dict] | None = None,
    ) -> None:
        """Run an action's steps in the background and finish it.

        A server is locked until the steps end. When they fail, the next actions fail
        with them, and the server leaves its in-between status (starting, rebuilding,
        ...) for what its VM really does.
        """
        if server is not None:
            server["locked"] = True

        async def go():
            message = None
            try:
                await steps()
            # reported through the action, like the cloud does
            except Exception as error:  # noqa: BLE001
                log.exception("action %s failed", action["command"])
                message = str(error)
                for n in next_actions or []:
                    if n["status"] == "running":
                        self.finish(n, message)
                if server is not None and server["status"] in (
                    "initializing",
                    "starting",
                    "stopping",
                    "rebuilding",
                    "deleting",
                ):
                    alive = await self.blocking(self.fabric.vm_alive, server)
                    self.set_status(server, "running" if alive else "off")
            except asyncio.CancelledError:
                message = "the action was cancelled (lab stopped or reset)"
                raise
            finally:
                if server is not None:
                    self.release(server)
                self.finish(action, message)

        self.spawn(go())

    # --- servers ----------------------------------------------------------------------

    def server(self, sid: int) -> dict:
        """Return the server with this ID, or raise 404."""
        server = self.state["servers"].get(sid)
        if not server:
            raise not_found("server")
        return server

    def server_by_name(self, name: str) -> dict:
        """Return the server with this name, or raise 404."""
        for s in self.state["servers"].values():
            if s["name"] == name:
                return s
        raise not_found(f"server {name!r}")

    def reserve(self, server: dict) -> None:
        """Lock a server for an action that is about to start.

        Raises 423 if an action already runs; checks and sets in one step.
        """
        with self.state.lock:
            if server.get("locked"):
                raise locked("server is locked by another action")
            server["locked"] = True

    def release(self, server: dict) -> None:
        """Unlock a server reserved for an action that did not start (or ended)."""
        server["locked"] = False

    def set_status(self, server: dict, status: str) -> None:
        """Set a server's status and save the state."""
        with self.state.lock:
            server["status"] = status
            self.state.save()

    # --- watching VMs -----------------------------------------------------------------

    async def off_when_down(self, server: dict, timeout: float = 600) -> None:
        """Mark a server off once its VM has ended after a shutdown.

        Waits up to ten minutes.
        """
        for _ in range(int(timeout)):
            await asyncio.sleep(1)
            if (
                self.state["servers"].get(server["id"]) is not server
                or server["status"] != "running"
            ):
                return  # deleted, or something else changed it
            if not server.get("locked") and await self.blocking(
                self.stop_if_ended, server
            ):
                return

    async def vm_watcher(self) -> None:
        """Watch the VMs every five seconds, for good.

        A guest that powers itself off (or crashes) leaves its server off, as on
        Hetzner; console logs stay small.
        """
        while True:
            await asyncio.sleep(5)
            try:
                await self.blocking(self.watch_vms)
            except Exception:  # noqa: BLE001 – the watcher keeps going
                log.exception("VM watcher")

    def watch_vms(self) -> None:
        """Trim console logs and mark servers off whose VM has ended."""
        for s in list(self.state["servers"].values()):
            self.fabric.trim_console(s)
            if s["status"] == "running" and not s.get("locked"):
                self.stop_if_ended(s)

    def stop_if_ended(self, server: dict) -> bool:
        """Mark a running server off if its VM has ended.

        Its taps and DHCP go. Returns True if that happened.
        """
        if self.fabric.vm_alive(server):
            return False
        try:
            self.reserve(server)  # an action that started meanwhile wins
        except ApiError:
            return False
        try:
            if server["status"] != "running" or self.fabric.vm_alive(server):
                return False
            log.info("server %s: its VM ended", server["name"])
            self.fabric.kill_vm(server)
            self.set_status(server, "off")
            return True
        finally:
            self.release(server)
