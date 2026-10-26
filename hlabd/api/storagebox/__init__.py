"""The Storage Box API of api.hetzner.com.

The lab serves it on a separate address (config.HETZNER_API): boxes (boxes.py),
sub-accounts (subaccounts.py), snapshots and snapshot plans (snapshots.py).
fabric/storagebox.py handles the accounts and files behind them.
"""

import logging

from fastapi import FastAPI

from ...fabric.storagebox import BOX, snap_dir
from ...lab import Lab
from ..web import install
from . import boxes, snapshots, subaccounts
from .base import box_account, sync_box, sync_subaccount

log = logging.getLogger(__name__)


def create_app(lab: Lab) -> FastAPI:
    """Build the FastAPI app that serves the Storage Box API."""
    app = FastAPI(
        title="hlab: fake Hetzner API (Storage Boxes)",
        docs_url="/_docs",
        openapi_url="/_openapi.json",
    )
    install(app, lab, "hetzner.json", "/v1")
    # first, so that /v1/storage_boxes/actions is not read as /v1/storage_boxes/{id}
    boxes.add_routes(app, lab)
    subaccounts.add_routes(app, lab)
    snapshots.add_routes(app, lab)
    return app


async def restore(lab: Lab) -> None:
    """Set up the accounts of all boxes again after a lab start.

    Their roots are in /run, which a restart empties. Passwords are set again from the
    state, because /etc/shadow belongs to the container. A box that cannot be set up
    again is skipped and logged; the lab starts anyway. Snapshots whose copy the
    restart cut off are removed.
    """
    state, fabric = lab.state, lab.fabric
    # copies: the loops below run scripts, so the state may change in between
    cut_off = [s for s in state["box_snapshots"].values() if s.get("_copying")]
    boxes = list(state["storage_boxes"].values())
    for s in cut_off:
        box = state["storage_boxes"].get(s["box"])
        if box:
            path = snap_dir(box["id"], s["name"])
            await lab.blocking(fabric.box.remove_tree, path, check=False)
        with state.change():
            del state["box_snapshots"][s["id"]]
    # snapshots whose copy was not finished
    await lab.blocking(fabric.box.remove_tree, f"{BOX}/snapshots/.work", check=False)
    for box in boxes:
        try:
            await box_account(lab, box, box.get("_password"))
            await sync_box(lab, box)
        # one box that fails must not keep the lab from coming up
        except Exception:
            log.exception("Storage Box %s not restored", box["id"])
            continue
        accounts = [a for a in state["subaccounts"].values() if a["box"] == box["id"]]
        for account in accounts:
            try:
                await sync_subaccount(lab, box, account, account.get("_password"))
            # nor one sub-account that fails
            except Exception:
                log.exception("sub-account %s not restored", account["username"])
