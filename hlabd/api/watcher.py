"""The VM watcher: it notices VMs that ended on their own.

When a guest powers itself off (after a shutdown, or on its own) or crashes, its
server becomes off, as on Hetzner. The daemon runs `vm_watcher` as long as the lab
runs.
"""

import asyncio
import logging

from ..errors import ApiError
from ..lab import SERVER_LOCKED, Lab
from ..records import Server
from . import system

log = logging.getLogger(__name__)


async def vm_watcher(lab: Lab) -> None:
    """Check the VMs every five seconds, as long as the lab runs.

    The check also keeps console logs small.
    """
    while True:
        await asyncio.sleep(5)
        try:
            await watch_vms(lab)
        except Exception:  # the watcher keeps going; the error is in the log
            log.exception("VM watcher")


async def watch_vms(lab: Lab) -> None:
    """Trim console logs and mark servers off whose VM has ended."""
    # a copy: servers may be deleted while the scripts run
    servers = list(lab.state["servers"].values())
    for server in servers:
        await lab.blocking(lab.fabric.vms.trim_console, server["id"])
        if server["status"] == "running" and not server.get("_locked"):
            await stop_if_ended(lab, server)


async def stop_if_ended(lab: Lab, server: Server) -> None:
    """Mark a running server off if its VM has ended; remove its taps and DHCP."""
    if await lab.blocking(lab.fabric.vms.is_alive, server["id"]):
        return
    with lab.holding() as held:
        try:
            held.claim(server, "_locked", SERVER_LOCKED)
        except ApiError:
            # an action started in the meantime: it owns the server now
            return
        if server["status"] != "running" or await lab.blocking(
            lab.fabric.vms.is_alive, server["id"]
        ):
            return
        log.info("server %s: its VM ended", server["name"])
        await system.kill_vm(lab, server)
        lab.set_status(server, "off")
