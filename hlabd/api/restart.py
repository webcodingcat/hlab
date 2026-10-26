"""Bringing the lab back after a (re)start.

In this order: the flags of actions are cleared; the Storage Box comes up with its
accounts; what was half done is finished or dropped; firewalls and faults are in place
before any VM runs; servers that should run are powered on again; clients come back.
One broken server or box does not keep the lab from starting.
"""

import logging

from ..lab import FLAGS, Lab, now
from ..records import Server
from ..state import KINDS
from . import storagebox
from .servers import ensure_disk, remove_server
from .storagebox.base import set_up_box_user

log = logging.getLogger(__name__)


def clear_flags(lab: Lab) -> None:
    """Take every action flag off every resource, and unlock every server.

    None of their actions survived the restart.
    """
    state = lab.state
    with state.lock:
        for kind in KINDS:
            for item in state[kind].values():
                for flag in FLAGS:
                    item.pop(flag, None)
        for server in state["servers"].values():
            server["locked"] = False


async def restore_storage_box(lab: Lab) -> None:
    """Start the Storage Box with the lab's own accounts and the API's boxes.

    `storagebox.restore` also removes snapshots whose copy was cut off (`_copying`).
    """
    box_cfg = lab.cfg["storage_box"]
    if not box_cfg.get("enabled"):
        return
    await lab.blocking(lab.fabric.box.start_box, int(box_cfg.get("size_gb") or 0))
    with lab.state.lock:  # a copy: the loop runs scripts
        box_users = list(lab.state.data["box_users"].items())
    for user, key in box_users:
        await lab.blocking(set_up_box_user, lab, user, key)
    await lab.blocking(storagebox.restore, lab)


def fail_running_actions(lab: Lab) -> None:
    """Fail the actions that were running: the restart cut them off."""
    with lab.state.lock:
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


def drop_half_created_volumes(lab: Lab) -> None:
    """Delete the volumes cut off while being created (maybe in the middle of mkfs).

    Their create action failed.
    """
    state = lab.state
    with state.lock:  # a copy: the loop runs scripts
        creating = [v for v in state["volumes"].values() if v["status"] == "creating"]
    for volume in creating:
        lab.fabric.delete_volume(volume)
        with state.lock:
            del state["volumes"][volume["id"]]


def finish_box_setup(lab: Lab) -> None:
    """Mark Storage Boxes that were being set up active (they got their account)."""
    with lab.state.lock:
        for box in lab.state["storage_boxes"].values():
            if box["status"] == "initializing":
                box["status"] = "active"


def status_after_restart(server: Server) -> str:
    """Return the status a server ends in after a restart: "running" or "off".

    A server cut off while being created or rebuilt ends as its action would have
    (`target`); one that was running or starting runs again.
    """
    if server["status"] in ("initializing", "rebuilding"):
        return server["_lab"].get("target", "running")
    return "running" if server["status"] in ("running", "starting") else "off"


async def restart_servers(lab: Lab) -> None:
    """Finish deleting servers, and power on those that should run.

    A server that cannot be started stays off; the lab comes up anyway.
    """
    with lab.state.lock:  # a copy: the loop runs scripts
        servers = list(lab.state["servers"].values())
    for server in servers:
        try:
            if server["status"] == "deleting":
                await lab.blocking(remove_server, lab, server)
                continue
            target = status_after_restart(server)
            if target == "running":
                await ensure_disk(lab, server)
                await lab.blocking(lab.fabric.start_vm, server)
            elif server["status"] in ("initializing", "rebuilding"):
                # cut off while its disk was made: it gets the disk and stays off
                await ensure_disk(lab, server)
            lab.set_status(server, target)
        except Exception:  # the server stays off, the lab comes up
            log.exception("server %s could not be started again", server["name"])
            lab.set_status(server, "off")


async def restore_clients(lab: Lab) -> None:
    """Bring the Internet clients back; one that fails is left out (see the log)."""
    with lab.state.lock:  # a copy: the loop runs scripts
        clients = list(lab.state["clients"].values())
    for client in clients:
        try:
            await lab.blocking(lab.fabric.add_client, client)
        except Exception:  # the lab comes up without this client
            log.exception("client %s not restored", client["name"])


async def restore_lab(lab: Lab) -> None:
    """Restore the Storage Box, state, network, servers and clients after a start."""
    state, fabric = lab.state, lab.fabric
    clear_flags(lab)
    await restore_storage_box(lab)
    fail_running_actions(lab)
    drop_half_created_volumes(lab)
    finish_box_setup(lab)
    state.save()
    # firewalls and faults before any VM runs: a server is never unfiltered
    await lab.blocking(fabric.apply_firewalls)
    await lab.blocking(fabric.apply_faults)
    await restart_servers(lab)
    await restore_clients(lab)
    state.save()
    for step in (fabric.apply_faults, fabric.apply_latency, fabric.apply_firewalls):
        try:
            await lab.blocking(step)
        except Exception:  # the lab comes up; what failed is in the log
            log.exception("restoring the lab's network state")
