"""After a (re)start of the lab: what was half done is finished or dropped, servers that should run are powered on
again, clients come back. One broken server does not keep the lab from starting."""

import logging

from . import hetzner as H
from .servers import ensure_disk, forget_server
from .volumes import volume_file

log = logging.getLogger("hlabd.lab")


async def reconcile(lab) -> None:
    st, fabric = lab.state, lab.fabric
    for a in st["actions"].values():
        if a["status"] == "running":
            a.update(status="error", error={"code": "action_failed", "message": "lab restarted"},
                     finished=H.now(), progress=100)
    for v in list(st["volumes"].values()):
        if v["status"] == "creating":   # cut off (maybe in the middle of mkfs): it goes, its action failed
            volume_file(lab, v).unlink(missing_ok=True)
            del st["volumes"][v["id"]]
    for b in st["storage_boxes"].values():
        if b["status"] == "initializing":   # its account was set up again before (storagebox.restore)
            b["status"] = "active"
        b.pop("_deleting", None)   # a delete cut off can be asked for again
    for f in st["floating_ips"].values():
        f.pop("_moving", None)
    for n in st["networks"].values():
        n.pop("_deleting", None)
    for v in st["volumes"].values():
        v.pop("_busy", None)
    st.save()
    # firewalls and faults before any VM runs: a server is never unfiltered
    await lab.blocking(fabric.apply_firewalls)
    await lab.blocking(fabric.apply_faults)
    for s in list(st["servers"].values()):
        s["locked"] = False
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
    for c in st["clients"].values():
        try:
            await lab.blocking(fabric.sh.run, "client-add", c["name"], c["veth"], c["ip"], c["ip6"])
        except Exception:  # noqa: BLE001
            log.exception("client %s not restored", c["name"])
    st.save()
    for step in (fabric.apply_faults, fabric.apply_latency, fabric.apply_firewalls):
        try:   # the lab comes up; what failed is in the log
            await lab.blocking(step)
        except Exception:  # noqa: BLE001
            log.exception("restoring the lab's network state")
