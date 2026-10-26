"""What happens when the lab starts again.

The steps, in order: clear the action flags; start the Storage Box with its accounts;
finish or drop the work that a restart cut off; put firewalls and faults in place
before any VM runs; power on the servers that should run; bring the clients back. One
broken server or box does not stop the lab from starting.
"""

import logging

from ..lab import Lab, now
from ..records import Server
from . import storagebox, system
from .servers import ensure_disk, remove_server
from .storagebox.base import set_up_box_user

log = logging.getLogger(__name__)


async def restore_storage_box(lab: Lab) -> None:
    """Start the Storage Box with the API's boxes and the accounts made outside the API.

    `storagebox.restore` also removes snapshots whose copy was cut off (`_copying`).
    """
    box_cfg = lab.cfg["storage_box"]
    if not box_cfg.get("enabled"):
        return
    await lab.blocking(lab.fabric.box.start_box, int(box_cfg.get("size_gb") or 0))
    # a copy: other tasks may change the state while the scripts run
    box_users = list(lab.state.data["box_users"].items())
    for user, key in box_users:
        await set_up_box_user(lab, user, key)
    await storagebox.restore(lab)


def fail_running_actions(lab: Lab) -> None:
    """Fail the actions that were running: the restart cut them off."""
    with lab.state.change():
        for action in lab.state["actions"].values():
            if action["status"] == "running":
                action.update(
                    {
                        "status": "error",
                        "error": {"code": "action_failed", "message": "lab restarted"},
                        "finished": now(),
                        "progress": 100,
                    }
                )


async def drop_half_created_volumes(lab: Lab) -> None:
    """Delete the volumes whose creation the restart cut off (maybe during mkfs).

    Their create action has failed already.
    """
    state = lab.state
    # a copy: other tasks may change the state while the scripts run
    creating = [v for v in state["volumes"].values() if v["status"] == "creating"]
    for volume in creating:
        await lab.blocking(lab.fabric.volumes.delete, volume)
        with state.change():
            del state["volumes"][volume["id"]]


def finish_box_setup(lab: Lab) -> None:
    """Mark Storage Boxes that were being set up as active; the restart set them up."""
    with lab.state.change():
        for box in lab.state["storage_boxes"].values():
            if box["status"] == "initializing":
                box["status"] = "active"


def status_after_restart(server: Server) -> str:
    """Return the status a server ends in after a restart: "running" or "off".

    If the restart cut off the creation or rebuild of a server, the server gets the
    status its action would have given it (`target`). A server that was running or
    starting runs again.
    """
    if server["status"] in ("initializing", "rebuilding"):
        return server["_lab"].get("target", "running")
    return "running" if server["status"] in ("running", "starting") else "off"


async def restart_servers(lab: Lab) -> None:
    """Finish deleting servers, and power on those that should run.

    A server that cannot be started stays off; the lab comes up anyway.
    """
    # a copy: other tasks may change the state while the scripts run
    servers = list(lab.state["servers"].values())
    for server in servers:
        try:
            if server["status"] == "deleting":
                await remove_server(lab, server)
                continue
            target = status_after_restart(server)
            if target == "running":
                await ensure_disk(lab, server)
                await system.start_vm(lab, server)
            elif server["status"] in ("initializing", "rebuilding"):
                # the restart cut off the disk: create it; the server stays off
                await ensure_disk(lab, server)
            lab.set_status(server, target)
        except Exception:  # the server stays off, the lab comes up
            log.exception("server %s could not be started again", server["name"])
            lab.set_status(server, "off")


async def restore_clients(lab: Lab) -> None:
    """Bring the Internet clients back; one that fails is left out (see the log)."""
    # a copy: other tasks may change the state while the scripts run
    clients = list(lab.state["clients"].values())
    for client in clients:
        try:
            await system.add_client(lab, client)
        except Exception:  # the lab comes up without this client
            log.exception("client %s not restored", client["name"])


async def restore_lab(lab: Lab) -> None:
    """Restore the Storage Box, state, network, servers and clients after a start."""
    lab.clear_flags()
    await restore_storage_box(lab)
    fail_running_actions(lab)
    await drop_half_created_volumes(lab)
    finish_box_setup(lab)
    # firewalls and faults before any VM runs, so no server ever runs unfiltered
    await system.apply_firewall(lab)
    await system.apply_faults(lab)
    await restart_servers(lab)
    await restore_clients(lab)
    for step in (
        system.apply_faults,
        system.apply_latency,
        system.apply_firewall,
    ):
        try:
            await step(lab)
        except Exception:  # the lab comes up; what failed is in the log
            log.exception("restoring the lab's network state")
