"""The lab's core: its state, the fabric that carries out changes, and actions.

`api/` decides what happens (checks requests, changes the state, starts actions); `fabric/` carries it out (VMs,
taps, nftables, the Storage Box, through scripts/*.sh). This module ties them together: every API module gets one
`Lab` and works the same way – check the request, change the state under `state.lock`, and let an action do the slow
part (VMs, disks, networks) in the background.

How things are kept apart while an action runs:

- A server has `locked`: set by `reserve()` before an action starts (423 if one already runs, as on Hetzner) and
  cleared by `run_action()` when the action's steps end.
- Other resources mark themselves for the few moments that need it: `_moving` (a Floating IP being moved), `_busy` (a
  volume in an action, a Storage Box being rolled back), `_deleting` (a network or Storage Box being deleted),
  `_creating` (a sub-account being set up), `_copying` (a Storage Box snapshot being copied). Fields starting with
  `_` are the lab's own and never shown by the API; a restart of the lab clears them (api/restart.py).
"""

import asyncio
import logging

from .api.hetzner import now
from .api.web import ApiError, not_found
from .fabric import Fabric

log = logging.getLogger("hlabd.lab")



class Lab:
    def __init__(self, cfg: dict, state, fabric: Fabric):
        self.cfg, self.state, self.fabric = cfg, state, fabric
        self.scale = float(cfg["api"]["time_scale"])          # factor on the cloud's timings (0 = instant)
        self.fault = {"status": 0, "count": 0, "delay_ms": 0}  # injected API faults (/_lab/api-fault)
        self.tasks: set = set()                                # running background tasks (actions)
        self.images: dict[str, str] = {}                       # system image name → its file (loaded at start)
        self.loop: asyncio.AbstractEventLoop | None = None

    # --- running things ------------------------------------------------------------------------------------------

    async def sleep(self, seconds: float) -> None:
        """Wait as long as the cloud would (scaled by time_scale)."""
        if self.scale > 0:
            await asyncio.sleep(seconds * self.scale)

    async def blocking(self, fn, *args, **kwargs):
        """Run shell work (scripts, files) in a thread, so the API keeps answering."""
        return await asyncio.to_thread(fn, *args, **kwargs)

    def spawn(self, coro) -> None:
        """Run a coroutine on the server's event loop, also from the worker threads of sync endpoints."""
        try:
            t = asyncio.get_running_loop().create_task(coro)
        except RuntimeError:
            t = asyncio.run_coroutine_threadsafe(coro, self.loop)
        self.tasks.add(t)
        t.add_done_callback(self.tasks.discard)

    # --- actions -------------------------------------------------------------------------------------------------

    def new_action(self, command: str, resources: list[tuple[int, str]]) -> dict:
        """A new action in status running, for the given (id, type) resources."""
        with self.state.lock:
            a = {"id": self.state.new_id("actions"), "command": command, "status": "running", "progress": 0,
                 "started": now(), "finished": None, "error": None,
                 "resources": [{"id": i, "type": t} for i, t in resources]}
            acts = self.state["actions"]
            acts[a["id"]] = a
            if len(acts) > 10000:   # the state stays small: the oldest finished actions go
                for aid in [i for i, x in acts.items() if x["status"] != "running"][:len(acts) - 5000]:
                    del acts[aid]
        log.info("action %s %s %s", a["id"], command, " ".join(f"{t}:{i}" for i, t in resources))
        return a

    def finish(self, a: dict, err: str | None = None) -> None:
        with self.state.lock:
            a.update(progress=100, finished=now(), status="error" if err else "success")
            if err:
                a["error"] = {"code": "action_failed", "message": err}
            self.state.save()
        if err:
            log.warning("action %s %s failed: %s", a["id"], a["command"], err)
        else:
            log.info("action %s %s done", a["id"], a["command"])

    def run_action(self, a: dict, steps, server: dict | None = None, nxt: list[dict] | None = None) -> None:
        """Run an action's steps in the background and finish it. A server is locked until the steps end. When they
        fail, the next actions fail with them, and the server leaves its in-between status (starting, rebuilding,
        ...) for what its VM really does."""
        if server is not None:
            server["locked"] = True

        async def go():
            err = None
            try:
                await steps()
            except Exception as e:  # noqa: BLE001 – reported through the action, like the cloud does
                log.exception("action %s failed", a["command"])
                err = str(e)
                for n in nxt or []:
                    if n["status"] == "running":
                        self.finish(n, err)
                if server is not None and server["status"] in ("initializing", "starting", "stopping", "rebuilding",
                                                               "deleting"):
                    alive = await self.blocking(self.fabric.vm_alive, server)
                    self.set_status(server, "running" if alive else "off")
            except asyncio.CancelledError:
                err = "the action was cancelled (lab stopped or reset)"
                raise
            finally:
                if server is not None:
                    server["locked"] = False
                self.finish(a, err)
        self.spawn(go())

    # --- servers -------------------------------------------------------------------------------------------------

    def server(self, sid: int) -> dict:
        s = self.state["servers"].get(sid)
        if not s:
            raise not_found("server")
        return s

    def server_by_name(self, name: str) -> dict:
        for s in self.state["servers"].values():
            if s["name"] == name:
                return s
        raise not_found(f"server {name!r}")

    def reserve(self, s: dict) -> None:
        """Lock a server for an action that is about to start, or 423 if one runs (check and set in one step)."""
        with self.state.lock:
            if s.get("locked"):
                raise ApiError(423, "locked", "server is locked by another action")
            s["locked"] = True

    def set_status(self, s: dict, status: str) -> None:
        with self.state.lock:
            s["status"] = status
            self.state.save()

    # --- watching VMs --------------------------------------------------------------------------------------------

    async def off_when_down(self, s: dict, timeout: float = 600) -> None:
        """After a shutdown: the server is off once its VM has ended (within ten minutes)."""
        for _ in range(int(timeout)):
            await asyncio.sleep(1)
            if self.state["servers"].get(s["id"]) is not s or s["status"] != "running":
                return   # deleted, or something else changed it
            if not s.get("locked") and await self.blocking(self.stop_if_ended, s):
                return

    async def vm_watcher(self) -> None:
        """A guest that powers itself off (or crashes) leaves its server off, as on Hetzner; console logs stay
        small."""
        while True:
            await asyncio.sleep(5)
            try:
                await self.blocking(self.watch_vms)
            except Exception:  # noqa: BLE001 – the watcher keeps going
                log.exception("VM watcher")

    def watch_vms(self) -> None:
        for s in list(self.state["servers"].values()):
            self.fabric.trim_console(s)
            if s["status"] == "running" and not s.get("locked"):
                self.stop_if_ended(s)

    def stop_if_ended(self, s: dict) -> bool:
        """A running server whose VM has ended is off (its taps and DHCP go). True if that happened."""
        if self.fabric.vm_alive(s):
            return False
        try:
            self.reserve(s)   # an action that started meanwhile wins
        except ApiError:
            return False
        try:
            if s["status"] != "running" or self.fabric.vm_alive(s):
                return False
            log.info("server %s: its VM ended", s["name"])
            self.fabric.kill_vm(s)
            self.set_status(s, "off")
            return True
        finally:
            s["locked"] = False
