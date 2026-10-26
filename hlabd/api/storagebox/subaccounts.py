"""Sub-accounts of a box.

Accounts of their own (u<box>-sub<n>) with a home directory inside the box.
"""

from fastapi import FastAPI, Request

from ...errors import limit_exceeded, locked, not_found, not_unique
from ...fabric.storagebox import username
from ...lab import Lab, Plan, now
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

    Names are unique per box; an unnamed sub-account is called by its username.
    """
    with lab.state.lock:
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
    """Return the plan of a sub-account action: done, and it concerns the box too."""
    return Plan(resources=[(subaccount["box"], "storage_box")])


# --- sub-account actions --------------------------------------------------------------
# Each gets the sub-account and the request and returns its plan; each is done at once.


def change_home_directory(lab: Lab, subaccount: Subaccount, body: dict) -> Plan:
    """Change a sub-account's home; on failure the old one stays."""
    box = box_of(lab, subaccount)
    old = subaccount["home_directory"]
    home = normalized_box_path(body["home_directory"], "home_directory")
    with lab.state.lock:
        subaccount["home_directory"] = home
    try:
        sync_subaccount(lab, box, subaccount)
    except Exception:  # undo the half-done change, then raise
        with lab.state.lock:
            subaccount["home_directory"] = old
        raise
    return box_plan(subaccount)


def reset_subaccount_password(lab: Lab, subaccount: Subaccount, body: dict) -> Plan:
    """Set a new password for a sub-account."""
    box = box_of(lab, subaccount)
    password = checked_password(body.get("password"))
    with lab.state.lock:
        subaccount["_password"] = password
    sync_subaccount(lab, box, subaccount, password)
    return box_plan(subaccount)


def update_access_settings(lab: Lab, subaccount: Subaccount, body: dict) -> Plan:
    """Change a sub-account's access settings; on failure the old ones stay."""
    box = box_of(lab, subaccount)
    old = dict(subaccount["access"])
    access = checked_access_settings(body, subaccount["access"])
    with lab.state.lock:
        subaccount["access"].update(access)
    try:
        sync_subaccount(lab, box, subaccount)
    except Exception:  # not in effect: not reported either
        with lab.state.lock:
            subaccount["access"] = old
        raise
    return box_plan(subaccount)


# the sub-account actions: Hetzner's name of the action and the function that plans it
SUBACCOUNT_ACTIONS = {
    "change_home_directory": ("change_home_directory", change_home_directory),
    "reset_subaccount_password": (
        "reset_subaccount_password",
        reset_subaccount_password,
    ),
    "update_access_settings": ("update_access_settings", update_access_settings),
}


# --- route logic ----------------------------------------------------------------------


def create_subaccount(lab: Lab, bid: int, body: dict) -> dict:
    """Create a sub-account of a box with its account on the box.

    Sub-accounts are numbered per box (u<box>-sub<n>); the number is taken under the
    lock, so a second create at the same time gets the next one. The box is not
    deleted while the account is set up (`_creating`); without its account, the
    sub-account is removed again. Not while the box is busy (423).
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
    with state.lock:
        check_unique_subaccount_name(lab, box, body.get("name"), None)
        mine = [a for a in state["subaccounts"].values() if a["box"] == box["id"]]
        if len(mine) >= lab.cfg["storage_box_types"][box["type"]]["subaccounts"]:
            raise limit_exceeded(
                "sub-account limit of the Storage Box type reached", "subaccounts"
            )
        if box_busy(lab, box):  # the box got busy meanwhile (e.g. it is deleted)
            raise locked(BUSY)
        number = 1 + max([a["number"] for a in mine], default=0)
        subaccount_id = state.new_id("subaccounts")
        subaccount: Subaccount = {
            "id": subaccount_id,
            "box": box["id"],
            "number": number,
            "username": f"{username(box)}-sub{number}",
            "name": body.get("name"),
            "home_directory": home,
            "access": access_settings,
            "description": body.get("description") or "",
            "labels": body.get("labels") or {},
            "created": now(),
            "_password": password,
            "_creating": True,
        }
        state["subaccounts"][subaccount_id] = subaccount
    try:
        sync_subaccount(lab, box, subaccount, password)
    except Exception:  # undo the half-done change, then raise
        lab.fabric.box.remove_account(subaccount["username"], check=False)
        with state.lock:
            state["subaccounts"].pop(subaccount_id, None)
        raise
    finally:
        lab.release(subaccount, "_creating")
    state.save()
    resources = [(box["id"], "storage_box"), (subaccount_id, "storage_box_subaccount")]
    return {
        "subaccount": subaccount_json(lab, subaccount),
        "action": lab.finished_action("create_subaccount", resources),
    }


def update_subaccount(lab: Lab, bid: int, subaccount_id: int, body: dict) -> dict:
    """Change a sub-account's name, description and labels."""
    box = box_or_404(lab, bid)
    subaccount = subaccount_or_404(lab, box, subaccount_id)
    with lab.state.lock:
        check_unique_subaccount_name(lab, box, body.get("name"), subaccount)
        update_fields(subaccount, body, ("name", "description", "labels"))
        lab.state.save()
    return {"subaccount": subaccount_json(lab, subaccount)}


def delete_subaccount(lab: Lab, bid: int, subaccount_id: int) -> dict:
    """Delete a sub-account and its account at once (its files stay in the box)."""
    box = box_or_404(lab, bid, ready=True)
    subaccount = subaccount_or_404(lab, box, subaccount_id)
    lab.fabric.box.remove_account(subaccount["username"])
    with lab.state.lock:
        del lab.state["subaccounts"][subaccount_id]
        lab.state.save()
    return {
        "action": lab.finished_action(
            "delete_subaccount",
            [(bid, "storage_box"), (subaccount_id, "storage_box_subaccount")],
        )
    }


def run_subaccount_action(
    lab: Lab, bid: int, subaccount_id: int, cmd: str, body: dict
) -> dict:
    """Run an action of a sub-account (not while its box is busy)."""
    box = box_or_404(lab, bid, ready=True)
    subaccount = subaccount_or_404(lab, box, subaccount_id)
    return lab.act(SUBACCOUNT_ACTIONS, cmd, subaccount, "storage_box_subaccount", body)


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of a box's sub-accounts and their actions to the app."""
    state = lab.state

    @app.get("/v1/storage_boxes/{bid}/subaccounts")
    def subaccount_list(bid: int, request: Request):
        with state.lock:
            box_or_404(lab, bid)
            items = filtered(
                request,
                [a for a in state["subaccounts"].values() if a["box"] == bid],
                {
                    "username": lambda subaccount, value: (
                        subaccount["username"] == value
                    ),
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
        return await lab.blocking(create_subaccount, lab, bid, body)

    @app.get("/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}")
    def subaccount_get(bid: int, subaccount_id: int):
        subaccount = subaccount_or_404(lab, box_or_404(lab, bid), subaccount_id)
        return {"subaccount": subaccount_json(lab, subaccount)}

    @app.put("/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}")
    async def subaccount_update(bid: int, subaccount_id: int, request: Request):
        body = await read_body(request)
        return await lab.blocking(update_subaccount, lab, bid, subaccount_id, body)

    @app.delete("/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}", status_code=201)
    def subaccount_delete(bid: int, subaccount_id: int):
        return delete_subaccount(lab, bid, subaccount_id)

    @app.post(
        "/v1/storage_boxes/{bid}/subaccounts/{subaccount_id}/actions/{cmd}",
        status_code=201,
    )
    async def subaccount_action(
        bid: int, subaccount_id: int, cmd: str, request: Request
    ):
        body = await read_body(request)
        return await lab.blocking(
            run_subaccount_action, lab, bid, subaccount_id, cmd, body
        )
