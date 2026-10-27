"""The Storage Box API of api.hetzner.com.

The lab serves it on a separate address (config.HETZNER_API): boxes (boxes.py),
sub-accounts (subaccounts.py), snapshots and snapshot plans (snapshots.py).
fabric/storagebox.py handles the accounts and files behind them.
"""

import logging

from fastapi import FastAPI

from ...lab import Lab
from ...state import BOX_SNAPSHOTS, STORAGE_BOXES, SUBACCOUNTS
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
    # the IDs: the loops below run scripts, so the state may change in between; each
    # record is looked up again after every await
    cut_off = [sid for sid, s in state[BOX_SNAPSHOTS].items() if s.get("_copying")]
    bids = list(state[STORAGE_BOXES])
    for snapshot_id in cut_off:
        snapshot = state.get(BOX_SNAPSHOTS, snapshot_id)
        if snapshot is None:
            continue
        if snapshot["box"] in state[STORAGE_BOXES]:
            await lab.blocking(
                fabric.box.remove_snapshot,
                snapshot["box"],
                snapshot["name"],
                check=False,
            )
        with state.change():
            state[BOX_SNAPSHOTS].pop(snapshot_id, None)
    # snapshots whose copy was not finished
    await lab.blocking(fabric.box.clear_snapshot_work)
    for bid in bids:
        box = state.get(STORAGE_BOXES, bid)
        if box is None:
            continue
        try:
            await box_account(lab, box, box.get("_password"))
            await sync_box(lab, lab.current(STORAGE_BOXES, bid))
        # one box that fails must not keep the lab from coming up
        except Exception:
            log.exception("Storage Box %s not restored", bid)
            continue
        accounts = [aid for aid, a in state[SUBACCOUNTS].items() if a["box"] == bid]
        for account_id in accounts:
            account = state.get(SUBACCOUNTS, account_id)
            if account is None:
                continue
            name = account["username"]
            try:
                await sync_subaccount(
                    lab,
                    lab.current(STORAGE_BOXES, bid),
                    account,
                    account.get("_password"),
                )
            # nor one sub-account that fails
            except Exception:
                log.exception("sub-account %s not restored", name)
