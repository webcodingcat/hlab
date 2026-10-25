"""Sub-accounts of a box.

Accounts of their own (u<box>-sub<n>) with a home directory inside the box.
"""

from fastapi import FastAPI, Request

from ...errors import ApiError, limit_exceeded, locked, not_found, unsupported
from ...fabric.storagebox import username
from ...lab import now
from ..spec import sort_items
from ..web import filtered, read_body
from .base import (
    access,
    box_or_404,
    check_home,
    check_password,
    sync_subaccount,
    update_fields,
)


def subaccount_json(subaccount: dict) -> dict:
    """Return the API view of a sub-account."""
    username = subaccount["username"]
    return {
        "storage_box": subaccount["box"],
        "id": subaccount["id"],
        "name": subaccount.get("name") or username,
        "home_directory": subaccount["home_directory"],
        "access_settings": subaccount["access"],
        "description": subaccount.get("description") or "",
        "labels": subaccount["labels"],
        "username": username,
        "server": f"{subaccount['username']}.your-storagebox.de",
        "created": subaccount["created"],
    }


def subaccount_or_404(lab, box: dict, aid: int) -> dict:
    """Return a sub-account of the box by ID, or raise 404."""
    subaccount = lab.state["subaccounts"].get(aid)
    if not subaccount or subaccount["box"] != box["id"]:
        raise not_found("subaccount")
    return subaccount


def check_unique_name(lab, box: dict, name, current: dict | None) -> None:
    """Check that no other sub-account of the box has this name.

    Names are unique per box; an unnamed sub-account is called by its username.
    """
    mine = [x for x in lab.state["subaccounts"].values() if x["box"] == box["id"]]
    if name and any(
        x is not current and (x.get("name") or x["username"]) == name for x in mine
    ):
        raise ApiError(
            409,
            "uniqueness_error",
            "a sub-account has this name already",
            {"fields": [{"name": "name"}]},
        )


def change_home_directory(lab, box: dict, subaccount: dict, body: dict) -> None:
    """Change a sub-account's home; on failure the old one stays."""
    old, subaccount["home_directory"] = (
        subaccount["home_directory"],
        check_home(body["home_directory"]),
    )
    try:
        sync_subaccount(lab, box, subaccount)
    except Exception:
        subaccount["home_directory"] = old
        raise


def reset_subaccount_password(lab, box: dict, subaccount: dict, body: dict) -> None:
    """Set a new password for a sub-account."""
    subaccount["_password"] = check_password(body.get("password"))
    sync_subaccount(lab, box, subaccount, body["password"])


def update_access_settings(lab, box: dict, subaccount: dict, body: dict) -> None:
    """Change a sub-account's access settings; on failure the old ones stay."""
    old = dict(subaccount["access"])
    subaccount["access"].update(access(body, subaccount["access"]))
    try:
        sync_subaccount(lab, box, subaccount)
    except Exception:  # not in effect: not reported either
        subaccount["access"] = old
        raise


# the actions of sub-accounts; each one is done at once
ACTIONS = {
    "change_home_directory": change_home_directory,
    "reset_subaccount_password": reset_subaccount_password,
    "update_access_settings": update_access_settings,
}


def add_routes(app: FastAPI, lab) -> None:
    """Register the routes of a box's sub-accounts and their actions."""
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/storage_boxes/{bid}/subaccounts")
    def subaccounts(bid: int, request: Request):
        box_or_404(lab, bid)
        items = filtered(
            request,
            [a for a in state["subaccounts"].values() if a["box"] == bid],
            {
                "username": lambda a, v: a["username"] == v,
                "name": lambda a, v: (a.get("name") or a["username"]) == v,
            },
        )  # the default name
        return {
            "subaccounts": sort_items(
                [subaccount_json(a) for a in items],
                request.query_params.getlist("sort"),
            )
        }

    @app.post("/v1/storage_boxes/{bid}/subaccounts", status_code=201)
    async def subaccount_create(bid: int, request: Request):
        box = box_or_404(lab, bid, ready=True)
        body = await read_body(request)
        access_settings = {
            "reachable_externally": False,
            "samba_enabled": False,
            "ssh_enabled": False,
            "webdav_enabled": False,
            "readonly": False,
        }
        access_settings.update(access(body.get("access_settings"), access_settings))
        home, password = (
            check_home(body.get("home_directory")),
            check_password(body.get("password")),
        )
        # the number is taken here: a second create at the same time gets the next one
        with state.lock:
            check_unique_name(lab, box, body.get("name"), None)
            mine = [a for a in state["subaccounts"].values() if a["box"] == bid]
            if len(mine) >= cfg["storage_box_types"][box["type"]]["subaccounts"]:
                raise limit_exceeded(
                    "sub-account limit of the Storage Box type reached", "subaccounts"
                )
            if box.get("_deleting") or box.get(
                "_busy"
            ):  # the box started going meanwhile
                raise locked("the Storage Box is busy with another action")
            number = 1 + max([a["n"] for a in mine], default=0)
            aid = state.new_id("subaccounts")
            subaccount = {
                "id": aid,
                "box": bid,
                "n": number,
                "username": f"{username(box)}-sub{number}",
                "name": body.get("name"),
                "home_directory": home,
                "access": access_settings,
                "description": body.get("description") or "",
                "labels": body.get("labels") or {},
                "created": now(),
                "_password": password,
                "_creating": True,
            }  # the box is not deleted while its account is set up
            state["subaccounts"][aid] = subaccount
        try:
            await lab.blocking(sync_subaccount, lab, box, subaccount, password)
        except Exception:
            await lab.blocking(
                lab.fabric.box.remove, subaccount["username"], check=False
            )
            with state.lock:
                state["subaccounts"].pop(aid, None)
            raise
        finally:
            subaccount.pop("_creating", None)
        state.save()
        return {
            "subaccount": subaccount_json(subaccount),
            "action": lab.done(
                "create_subaccount",
                [(bid, "storage_box"), (aid, "storage_box_subaccount")],
            ),
        }

    @app.get("/v1/storage_boxes/{bid}/subaccounts/{aid}")
    def subaccount_get(bid: int, aid: int):
        return {
            "subaccount": subaccount_json(
                subaccount_or_404(lab, box_or_404(lab, bid), aid)
            )
        }

    @app.put("/v1/storage_boxes/{bid}/subaccounts/{aid}")
    async def subaccount_update(bid: int, aid: int, request: Request):
        box = box_or_404(lab, bid)
        subaccount = subaccount_or_404(lab, box, aid)
        body = await read_body(request)
        check_unique_name(lab, box, body.get("name"), subaccount)
        update_fields(subaccount, body, ("name", "description", "labels"))
        state.save()
        return {"subaccount": subaccount_json(subaccount)}

    @app.delete("/v1/storage_boxes/{bid}/subaccounts/{aid}", status_code=201)
    def subaccount_delete(bid: int, aid: int):
        subaccount = subaccount_or_404(lab, box_or_404(lab, bid, ready=True), aid)
        lab.fabric.box.remove(subaccount["username"])
        del state["subaccounts"][aid]
        state.save()
        return {
            "action": lab.done(
                "delete_subaccount",
                [(bid, "storage_box"), (aid, "storage_box_subaccount")],
            )
        }

    @app.post(
        "/v1/storage_boxes/{bid}/subaccounts/{aid}/actions/{cmd}", status_code=201
    )
    async def subaccount_action(bid: int, aid: int, cmd: str, request: Request):
        box = box_or_404(lab, bid, ready=True)
        subaccount = subaccount_or_404(lab, box, aid)
        body = await read_body(request)
        if cmd not in ACTIONS:
            raise unsupported(f"the action {cmd!r}")
        await lab.blocking(ACTIONS[cmd], lab, box, subaccount, body)
        state.save()
        return {
            "action": lab.done(
                cmd, [(bid, "storage_box"), (aid, "storage_box_subaccount")]
            )
        }
