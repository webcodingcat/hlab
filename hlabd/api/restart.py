"""After a (re)start of the lab, in this order: the Storage Box comes up with its accounts; what was half done is
finished or dropped, and every lab-own `_` mark is cleared; firewalls and faults are in place before any VM runs;
servers that should run are powered on again; clients come back. One broken server or box does not keep the lab
from starting."""

import logging

from ..lab import now
from . import storagebox
from .servers import ensure_disk, forget_server

log = logging.getLogger("hlabd.lab")


async def restart(lab) -> None:
    state, fabric = lab.state, lab.fabric
    box = lab.cfg["storage_box"]
    if box.get("enabled"):
        await lab.blocking(fabric.box.start, int(box.get("size_gb") or 0))
        for user, key in state.data.get("box_users", {}).items():
            await lab.blocking(fabric.box.lab_account, state.data.setdefault("box_uids", {}), user, key)
        await lab.blocking(storagebox.restore, lab)   # also clears the boxes' `_busy`, `_creating`, `_copying`
    for a in state["actions"].values():
        if a["status"] == "running":
            a.update(status="error", error={"code": "action_failed", "message": "lab restarted"},
                     finished=now(), progress=100)
    for v in list(state["volumes"].values()):
        if v["status"] == "creating":   # cut off (maybe in the middle of mkfs): it goes, its action failed
            fabric.delete_volume(v)
            del state["volumes"][v["id"]]
    for b in state["storage_boxes"].values():
        if b["status"] == "initializing":   # its account was set up again above
            b["status"] = "active"
        b.pop("_deleting", None)   # a delete cut off can be asked for again
    for f in state["floating_ips"].values():
        f.pop("_moving", None)
    for n in state["networks"].values():
        n.pop("_deleting", None)
    for v in state["volumes"].values():
        v.pop("_busy", None)
    state.save()
    # firewalls and faults before any VM runs: a server is never unfiltered
    await lab.blocking(fabric.apply_firewalls)
    await lab.blocking(fabric.apply_faults)
    for s in list(state["servers"].values()):
        lab.release(s)
        try:
            if s["status"] == "deleting":
                await lab.blocking(forget_server, lab, s)
            elif s["status"] == "initializing" and not s["_lab"].get("start", True):
                await ensure_disk(lab, s)
                s["status"] = "off"
            elif s["status"] == "rebuilding" and s["_lab"].get("after_rebuild") == "off":
                await ensure_disk(lab, s)
                s["status"] = "off"
            elif s["status"] in ("running", "starting", "initializing", "rebuilding"):
                await ensure_disk(lab, s)
                await lab.blocking(fabric.start_vm, s)
                s["status"] = "running"
            else:
                s["status"] = "off"
        except Exception:  # noqa: BLE001 – the server stays off, the lab comes up
            log.exception("server %s could not be started again", s["name"])
            s["status"] = "off"
    for c in state["clients"].values():
        try:
            await lab.blocking(fabric.add_client, c)
        except Exception:  # noqa: BLE001
            log.exception("client %s not restored", c["name"])
    state.save()
    for step in (fabric.apply_faults, fabric.apply_latency, fabric.apply_firewalls):
        try:   # the lab comes up; what failed is in the log
            await lab.blocking(step)
        except Exception:  # noqa: BLE001
            log.exception("restoring the lab's network state")
