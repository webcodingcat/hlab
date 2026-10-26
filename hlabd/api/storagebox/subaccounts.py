"""Sub-accounts of a box.

Each one is a separate account (u<box>-sub<n>) with a home directory inside the box.
"""

from fastapi import FastAPI, Request

from ...errors import limit_exceeded, locked, not_found, not_unique
from ...fabric.storagebox import username
from ...lab import ActionSpec, ActionTable, Lab, Plan, now
from ...records import StorageBox, Subaccount
from ..web import filtered, read_body, sort_items, update_fields
from .base import (
    BUSY,
    box_busy,
    box_or_404,
    checked_access_settings,
    checked_password,
    normalized_box_path,
    sync_subaccount,
)


def subaccount_json(lab: Lab, subaccount: Subaccount) -> dict:
    """Return the API object of a sub-account."""
    account = subaccount["username"]
    return {
        "storage_box": subaccount["box"],
        "id": subaccount["id"],
        "name": subaccount.get("name") or account,
        "home_directory": subaccount["home_directory"],
        "access_settings": subaccount["access"],
        "description": subaccount.get("description") or "",
        "labels": subaccount["labels"],
        "username": account,
        "server": f"{subaccount['username']}.your-storagebox.de",
        "created": subaccount["created"],
    }


def subaccount_or_404(lab: Lab, box: StorageBox, subaccount_id: int) -> Subaccount:
    """Return a sub-account of the box by ID, or raise 404."""
    subaccount = lab.state["subaccounts"].get(subaccount_id)
    if not subaccount or subaccount["box"] != box["id"]:
        raise not_found("subaccount")
    return subaccount


def check_unique_subaccount_name(
    lab: Lab, box: StorageBox, name, current: Subaccount | None
) -> None:
    """Check that no other sub-account of the box has this name.

    Names are unique per box. A sub-account without a name goes by its username.
    """
    others = [
        x
        for x in lab.state["subaccounts"].values()
        if x["box"] == box["id"] and x is not current
    ]
    if name and any((x.get("name") or x["username"]) == name for x in others):
        raise not_unique("a sub-account has this name already", "name")


def box_of(lab: Lab, subaccount: Subaccount) -> StorageBox:
    """Return a sub-account's box."""
    return lab.state["storage_boxes"][subaccount["box"]]


def box_plan(subaccount: Subaccount) -> Plan:
    """Return the plan of a sub-account action: no steps; it is also about the box."""
    return Plan(resources=[(subaccount["box"], "storage_box")])


# --- sub-account actions --------------------------------------------------------------
# Each function gets the sub-account and the request and returns a plan without steps.


async def change_home_directory(lab: Lab, subaccount: Subaccount, body: dict) -> Plan:
    """Change a sub-account's home; on failure the old one stays."""
    box = box_of(lab, subaccount)
    old = subaccount["home_directory"]
    home = normalized_box_path(body["home_directory"], "home_directory")
    with lab.state.change():
        subaccount["home_directory"] = home
    try:
        await sync_subaccount(lab, box, subaccount)
    except Exception:  # undo the partial change, then raise
        with lab.state.change():
            subaccount["home_directory"] = old
        raise
    return box_plan(subaccount)


async def reset_subaccount_password(
    lab: Lab, subaccount: Subaccount, body: dict
) -> Plan:
    """Set a new password for a sub-account."""
    box = box_of(lab, subaccount)
    password = checked_password(body.get("password"))
    with lab.state.change():
        subaccount["_password"] = password
    await sync_subaccount(lab, box, subaccount, password)
    return box_plan(subaccount)


async def update_access_settings(lab: Lab, subaccount: Subaccount, body: dict) -> Plan:
    """Change a sub-account's access settings; on failure the old ones stay."""
    box = box_of(lab, subaccount)
    old = dict(subaccount["access"])
    access = checked_access_settings(body, subaccount["access"])
    with lab.state.change():
        subaccount["access"].update(access)
    try:
        await sync_subaccount(lab, box, subaccount)
    except Exception:  # the settings did not take effect, so do not store them
        with lab.state.change():
            subaccount["access"] = old
        raise
    return box_plan(subaccount)


# the sub-account actions: Hetzner's name of the action and the function that plans it
SUBACCOUNT_ACTIONS: ActionTable[Subaccount] = {
    "change_home_directory": ActionSpec("change_home_directory", change_home_directory),
    "reset_subaccount_password": ActionSpec(
        "reset_subaccount_password",
        reset_subaccount_password,
    ),
    "update_access_settings": ActionSpec(
        "update_access_settings", update_access_settings
    ),
}


# --- route logic ----------------------------------------------------------------------


async def create_subaccount(lab: Lab, bid: int, body: dict) -> dict:
    """Create a sub-account of a box with its account on the box.

    Sub-accounts are numbered per box (u<box>-sub<n>). The number is taken together
    with the record, so a second create at the same time gets the next number. The
    box cannot be deleted while the account is set up (`_creating`). If the account
    cannot be set up, the sub-account is removed again. While the box is busy, the
    request gets 423.
    """
    state = lab.state
    box = box_or_404(lab, bid, ready=True)
    access_settings = {
        "reachable_externally": False,
        "samba_enabled": False,
        "ssh_enabled": False,
        "webdav_enabled": False,
        "readonly": False,
    }
    access_settings.update(
        checked_access_settings(body.get("access_settings"), access_settings)
    )
    home = normalized_box_path(body.get("home_directory"), "home_directory")
    password = checked_password(body.get("password"))
    with lab.holding() as held:
        check_unique_subaccount_name(lab, box, body.get("name"), None)
        mine = [a for a in state["subaccounts"].values() if a["box"] == box["id"]]
        if len(mine) >= lab.cfg["storage_box_types"][box["type"]]["subaccounts"]:
            raise limit_exceeded(
                "sub-account limit of the Storage Box type reached", "subaccounts"
            )
        if box_busy(lab, box):  # the box became busy in the meantime (e.g. a delete)
            raise locked(BUSY)
        number = 1 + max([a["number"] for a in mine], default=0)
        with state.change():
            subaccount_id = state.new_id("subaccounts")
            subaccount: Subaccount = {
                "id": subaccount_id,
                "box": box["id"],
                "number": number,
                "username": f"{username(box['id'])}-sub{number}",
                "name": body.get("name"),
                "home_directory": home,
                "access": access_settings,
                "description": body.get("description") or "",
                "labels": body.get("labels") or {},
                "created": now(),
                "_password": password,
            }
            state["subaccounts"][subaccount_id] = subaccount
            held.claim(subaccount, "_creating", BUSY)  # new: nobody holds it yet
        try:
            await sync_subaccount(lab, box, subaccount, password)
        except Exception:  # undo the partial change, then raise
            await lab.blocking(
                lab.fabric.box.remove_account, subaccount["username"], check=False
            )
            with state.change():
                state["subaccounts"].pop(subaccount_id, None)
            raise
        with state.change():  # the sub-account is ready
            held.release()
    resources = [(box["id"], "storage_box"), (subaccount_id, "storage_box_subaccount")]
    return {
        "subaccount": subaccount_json(lab, subaccount),
        "action": lab.finished_action("create_subaccount", resources),
    }


def update_subaccount(lab: Lab, bid: int, subaccount_id: int, body: dict) -> dict:
    """Change a sub-account's name, description and labels."""
    box = box_or_404(lab, bid)
    subaccount = subaccount_or_404(lab, box, subaccount_id)
    with lab.state.change():
        check_unique_subaccount_name(lab, box, body.get("name"), subaccount)
        update_fields(lab, subaccount, body, ("name", "description", "labels"))
    return {"subaccount": subaccount_json(lab, subaccount)}


async def delete_subaccount(lab: Lab, bid: int, subaccount_id: int) -> dict:
    """Delete a sub-account and its account at once; its files stay in the box."""
    box = box_or_404(lab, bid, ready=True)
    subaccount = subaccount_or_404(lab, box, subaccount_id)
    await lab.blocking(lab.fabric.box.remove_account, subaccount["username"])
    with lab.state.change():
        del lab.state["subaccounts"][subaccount_id]
    return {
        "action": lab.finished_action(
            "delete_subaccount",
            [(bid, "storage_box"), (subaccount_id, "storage_box_subaccount")],
        )
    }


async def run_subaccount_action(
    lab: Lab, bid: int, subaccount_id: int, cmd: str, body: dict
) -> dict:
    """Run an action of a sub-account (not while its box is busy)."""
    box = box_or_404(lab, bid, ready=True)
    subaccount = subaccount_or_404(lab, box, subaccount_id)
    return await lab.act(
        SUBACCOUNT_ACTIONS, cmd, subaccount, "storage_box_subaccount", body
    )


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of a box's sub-accounts and their actions to the app."""
    state = lab.state

    @app.get("/v1/storage_boxes/{bid}/subaccounts")
    async def subaccount_list(bid: int, request: Request):
        box_or_404(lab, bid)
        items = filtered(
            request,
            [a for a in state["subaccounts"].values() if a["box"] == bid],
            {
                "username": lambda subaccount, value: subaccount["username"] == value,
                # a sub-account without a name goes by its username
                "name": lambda subaccount, value: (
                    (subaccount.get("name") or subaccount["username"]) == value
                ),
            },
        )
        return {
            "subaccounts": sort_items(
                [subaccount_json(lab, a) for a in items],
                request.query_params.getlist("sort"),
            )
        }

    @app.post("/v1/storage_boxes/{bid}/subaccounts", status_code=201)
    async def subaccount_create(bid: int, request: Request):
        body = await read_body(request)
        return await create_subaccount(lab, bid, body)

    @app.get("/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}")
    async def subaccount_get(bid: int, subaccount_id: int):
        subaccount = subaccount_or_404(lab, box_or_404(lab, bid), subaccount_id)
        return {"subaccount": subaccount_json(lab, subaccount)}

    @app.put("/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}")
    async def subaccount_update(bid: int, subaccount_id: int, request: Request):
        body = await read_body(request)
        return update_subaccount(lab, bid, subaccount_id, body)

    @app.delete("/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}", status_code=201)
    async def subaccount_delete(bid: int, subaccount_id: int):
        return await delete_subaccount(lab, bid, subaccount_id)

    @app.post(
        "/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}/actions/{cmd}",
        status_code=201,
    )
    async def subaccount_action(
        bid: int, subaccount_id: int, cmd: str, request: Request
    ):
        body = await read_body(request)
        return await run_subaccount_action(lab, bid, subaccount_id, cmd, body)
