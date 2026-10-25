"""The Storage Box API of api.hetzner.com, served by the lab on its own address (config.HETZNER_API): boxes
(boxes.py), sub-accounts (subaccounts.py), snapshots and snapshot plans (snapshots.py). The accounts and files behind
them are fabric/storagebox.py."""

import logging

from fastapi import FastAPI

from ...fabric.storagebox import BOX, data_dir, snap_dir, username
from ..web import install
from . import boxes, snapshots, subaccounts
from .base import quota, sub_home, sync_box

log = logging.getLogger("hlabd.box")


def create_app(lab) -> FastAPI:
    app = FastAPI(title="hlab: fake Hetzner API (Storage Boxes)", docs_url="/_docs", openapi_url="/_openapi.json")
    install(app, lab, "hetzner.json", "/v1")
    boxes.add_routes(app, lab)   # first: /v1/storage_boxes/actions must not be taken for /v1/storage_boxes/{id}
    subaccounts.add_routes(app, lab)
    snapshots.add_routes(app, lab)
    return app


def restore(lab) -> None:
    """After a lab start: the accounts of all boxes (their roots live in /run). Passwords stay as set (shadow is
    in the container, so they are set again from the state). A box that cannot be set up again is skipped and
    logged; the lab comes up anyway. Snapshots whose copy a restart cut off go."""
    state, fabric = lab.state, lab.fabric
    for s in [s for s in state["box_snapshots"].values() if s.get("_copying")]:
        box = state["storage_boxes"].get(s["box"])
        if box:
            fabric.box.rmtree(snap_dir(box, s["name"]), check=False)
        del state["box_snapshots"][s["id"]]
    fabric.box.rmtree(f"{BOX}/snapshots/.work", check=False)   # copies cut off
    for box in state["storage_boxes"].values():
        box.pop("_busy", None)
    for a in state["subaccounts"].values():
        a.pop("_creating", None)
    state.save()
    for box in state["storage_boxes"].values():
        try:
            fabric.box.account(username(box), data_dir(box), box.get("_password"), quota(lab, box), str(box["id"]))
            sync_box(lab, box)
        except Exception:  # noqa: BLE001
            log.exception("Storage Box %s not restored", box["id"])
            continue
        for a in [a for a in state["subaccounts"].values() if a["box"] == box["id"]]:
            try:
                fabric.box.account(a["username"], sub_home(box, a), a.get("_password"), quota(lab, box), username(box))
                fabric.box.access(a["username"], a["access"], quota(lab, box))
            except Exception:  # noqa: BLE001
                log.exception("sub-account %s not restored", a["username"])
