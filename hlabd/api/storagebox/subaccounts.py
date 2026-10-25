"""Sub-accounts of a box: accounts of their own (u<box>-sub<n>) with a home directory inside the box."""

from fastapi import FastAPI, Request

from ...fabric.storagebox import username
from .. import hetzner as H
from ..spec import sort_items
from ..web import ApiError, body, filtered, limit_exceeded, not_found
from .base import access, box_or_404, check_home, check_password, done, sync_subaccount, update_fields


def subaccount_json(a: dict) -> dict:
    return {"storage_box": a["box"], "id": a["id"], "name": a.get("name") or a["username"],
            "home_directory": a["home_directory"], "access_settings": a["access"],
            "description": a.get("description") or "", "labels": a["labels"], "username": a["username"],
            "server": f"{a['username']}.your-storagebox.de", "created": a["created"]}


def subaccount_or_404(lab, b: dict, aid: int) -> dict:
    a = lab.state["subaccounts"].get(aid)
    if not a or a["box"] != b["id"]:
        raise not_found("subaccount")
    return a


def check_unique_name(lab, b: dict, name, me: dict | None) -> None:
    """Sub-account names are unique per box (an unnamed one is called by its username)."""
    mine = [x for x in lab.state["subaccounts"].values() if x["box"] == b["id"]]
    if name and any(x is not me and (x.get("name") or x["username"]) == name for x in mine):
        raise ApiError(409, "uniqueness_error", "a sub-account has this name already", {"fields": [{"name": "name"}]})


def change_home_directory(lab, b: dict, a: dict, bd: dict) -> None:
    old, a["home_directory"] = a["home_directory"], check_home(bd["home_directory"])
    try:
        sync_subaccount(lab, b, a)
    except Exception:
        a["home_directory"] = old
        raise


def reset_subaccount_password(lab, b: dict, a: dict, bd: dict) -> None:
    a["_password"] = check_password(bd.get("password"))
    sync_subaccount(lab, b, a, bd["password"])


def update_access_settings(lab, b: dict, a: dict, bd: dict) -> None:
    old = dict(a["access"])
    a["access"].update(access(bd, a["access"]))
    try:
        sync_subaccount(lab, b, a)
    except Exception:   # not in effect: not reported either
        a["access"] = old
        raise


# the actions of sub-accounts; each one is done at once
ACTIONS = {"change_home_directory": change_home_directory, "reset_subaccount_password": reset_subaccount_password,
           "update_access_settings": update_access_settings}


def add_routes(app: FastAPI, lab) -> None:
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/storage_boxes/{bid}/subaccounts")
    def subaccounts(bid: int, request: Request):
        box_or_404(lab, bid)
        items = filtered(request, [a for a in state["subaccounts"].values() if a["box"] == bid],
                         {"username": lambda a, v: a["username"] == v,
                          "name": lambda a, v: (a.get("name") or a["username"]) == v})   # the default name
        return {"subaccounts": sort_items([subaccount_json(a) for a in items], request.query_params.getlist("sort"))}

    @app.post("/v1/storage_boxes/{bid}/subaccounts", status_code=201)
    async def subaccount_create(bid: int, request: Request):
        b = box_or_404(lab, bid, ready=True)
        bd = await body(request)
        acc = {"reachable_externally": False, "samba_enabled": False, "ssh_enabled": False, "webdav_enabled": False,
               "readonly": False}
        acc.update(access(bd.get("access_settings"), acc))
        home, password = check_home(bd.get("home_directory")), check_password(bd.get("password"))
        with state.lock:   # the number is taken here: a second create at the same time gets the next one
            check_unique_name(lab, b, bd.get("name"), None)
            mine = [a for a in state["subaccounts"].values() if a["box"] == bid]
            if len(mine) >= cfg["storage_box_types"][b["type"]]["subaccounts"]:
                raise limit_exceeded("sub-account limit of the Storage Box type reached", "subaccounts")
            if b.get("_deleting") or b.get("_busy"):   # the box started going meanwhile
                raise ApiError(423, "locked", "the Storage Box is busy with another action")
            n = 1 + max([a["n"] for a in mine], default=0)
            aid = state.new_id("subaccounts")
            a = {"id": aid, "box": bid, "n": n, "username": f"{username(b)}-sub{n}", "name": bd.get("name"),
                 "home_directory": home, "access": acc, "description": bd.get("description") or "",
                 "labels": bd.get("labels") or {}, "created": H.now(), "_password": password,
                 "_creating": True}   # the box is not deleted while its account is set up
            state["subaccounts"][aid] = a
        try:
            await lab.blocking(sync_subaccount, lab, b, a, password)
        except Exception:
            await lab.blocking(lab.fabric.box.remove, a["username"], check=False)
            with state.lock:
                state["subaccounts"].pop(aid, None)
            raise
        finally:
            a.pop("_creating", None)
        state.save()
        return {"subaccount": subaccount_json(a),
                "action": done(lab, "create_subaccount", [(bid, "storage_box"), (aid, "storage_box_subaccount")])}

    @app.get("/v1/storage_boxes/{bid}/subaccounts/{aid}")
    def subaccount_get(bid: int, aid: int):
        return {"subaccount": subaccount_json(subaccount_or_404(lab, box_or_404(lab, bid), aid))}

    @app.put("/v1/storage_boxes/{bid}/subaccounts/{aid}")
    async def subaccount_update(bid: int, aid: int, request: Request):
        b = box_or_404(lab, bid)
        a = subaccount_or_404(lab, b, aid)
        bd = await body(request)
        check_unique_name(lab, b, bd.get("name"), a)
        update_fields(a, bd, ("name", "description", "labels"))
        state.save()
        return {"subaccount": subaccount_json(a)}

    @app.delete("/v1/storage_boxes/{bid}/subaccounts/{aid}", status_code=201)
    def subaccount_delete(bid: int, aid: int):
        a = subaccount_or_404(lab, box_or_404(lab, bid, ready=True), aid)
        lab.fabric.box.remove(a["username"])
        del state["subaccounts"][aid]
        state.save()
        return {"action": done(lab, "delete_subaccount", [(bid, "storage_box"), (aid, "storage_box_subaccount")])}

    @app.post("/v1/storage_boxes/{bid}/subaccounts/{aid}/actions/{cmd}", status_code=201)
    async def subaccount_action(bid: int, aid: int, cmd: str, request: Request):
        b = box_or_404(lab, bid, ready=True)
        a = subaccount_or_404(lab, b, aid)
        bd = await body(request)
        if cmd not in ACTIONS:
            raise ApiError(422, "unsupported_error", f"the lab does not support the action {cmd!r}")
        await lab.blocking(ACTIONS[cmd], lab, b, a, bd)
        state.save()
        return {"action": done(lab, cmd, [(bid, "storage_box"), (aid, "storage_box_subaccount")])}
