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
from ..state import SERVERS
from . import system

log = logging.getLogger(__name__)


async def vm_watcher(lab: Lab) -> None:
    """Check the VMs every five seconds, as long as the lab runs.

    The check also keeps console logs small.
    """
    while True:
        await asyncio.sleep(5)
        if lab.resetting:
            continue
        try:
            await watch_vms(lab)
        except Exception:  # the watcher keeps going; the error is in the log
            log.exception("VM watcher")


async def watch_vms(lab: Lab) -> None:
    """Trim console logs and mark servers off whose VM has ended."""
    # the IDs: servers may be deleted while the scripts run
    sids = list(lab.state[SERVERS])
    for sid in sids:
        await lab.blocking(lab.fabric.vms.trim_console, sid)
        server = lab.state.get(SERVERS, sid)  # looked up again after the await
        if server and server["status"] == "running" and not server.get("_locked"):
            await stop_if_ended(lab, server)


async def stop_if_ended(lab: Lab, server: Server) -> None:
    """Mark a running server off if its VM has ended; remove its taps and DHCP.

    The server is looked up again after each await; one that is gone is left alone.
    """
    sid = server["id"]
    if await lab.blocking(lab.fabric.vms.is_alive, sid):
        return
    current = lab.state.get(SERVERS, sid)
    if current is None:
        return
    with lab.holding() as held:
        try:
            held.claim(current, "_locked", SERVER_LOCKED)
        except ApiError:
            # an action started in the meantime: it owns the server now
            return
        if current["status"] != "running" or await lab.blocking(
            lab.fabric.vms.is_alive, sid
        ):
            return
        # the claim keeps actions and deletes away, but not a lab reset
        current = lab.state.get(SERVERS, sid)
        if current is None:
            return
        log.info("server %s: its VM ended", current["name"])
        await system.kill_vm(lab, current)
        current = lab.state.get(SERVERS, sid)
        if current is not None:
            lab.set_status(current, "off")
