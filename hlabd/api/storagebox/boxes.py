"""Storage Boxes: their types, the boxes themselves, their actions and folders."""

import posixpath

from fastapi import FastAPI, Request

from ...fabric.storagebox import TB, data_dir, snap_dir, username
from .. import hetzner as H
from ..catalogue import resolve
from ..web import ApiError, body, filtered, invalid, not_found, paginate
from .base import access, box_or_404, check_home, check_password, done, quota, sync_box, update_fields
from .snapshots import rollback


def box_type_id(cfg: dict, name: str) -> int:
    return 1330 + list(cfg["storage_box_types"]).index(name)


def box_type(cfg: dict, name: str) -> dict:
    t = cfg["storage_box_types"][name]
    prices = [{"location": loc, "price_hourly": {"net": "0.0050", "gross": "0.0060"},
               "price_monthly": {"net": "3.2000", "gross": "3.8080"}, "setup_fee": {"net": "0.0000", "gross": "0.0000"}}
              for loc in cfg["locations"]]
    return {"id": box_type_id(cfg, name), "name": name, "description": t.get("description", name.upper()),
            "snapshot_limit": t["snapshots"], "automatic_snapshot_limit": t["snapshots"],
            "subaccounts_limit": t["subaccounts"], "size": int(t["size_tb"] * TB), "prices": prices,
            "deprecation": None}


def box_type_name(cfg: dict, ref) -> str:
    """A box type by name or ID."""
    for n in cfg["storage_box_types"]:
        if str(ref) in (n, str(box_type_id(cfg, n))):
            return n
    raise invalid("Storage Box type not found", "storage_box_type")


def stats(lab, b: dict) -> dict:
    data, snaps = lab.fabric.box.usage(data_dir(b)), lab.fabric.box.usage(snap_dir(b))
    return {"size": data + snaps, "size_data": data, "size_snapshots": snaps}


def box_json(lab, b: dict) -> dict:
    """Reads the box's usage from disk: call it off the event loop."""
    ready = b["status"] != "initializing"
    return {"id": b["id"], "name": b["name"], "storage_box_type": box_type(lab.cfg, b["type"]),
            "location": H.location(lab.cfg, b["location"]), "access_settings": b["access"],
            "snapshot_plan": b.get("plan"), "protection": b["protection"], "labels": b["labels"],
            "status": b["status"], "username": username(b) if ready else None,
            "server": f"{username(b)}.your-storagebox.de" if ready else None,
            "system": f"{b['location'].upper()}-BX{b['id'] % 1000}", "stats": stats(lab, b), "created": b["created"]}


def check_unique_name(lab, name, me: dict | None) -> None:
    if name and any(x["name"] == name and x is not me for x in lab.state["storage_boxes"].values()):
        raise ApiError(409, "uniqueness_error", "Storage Box name is already used", {"fields": [{"name": "name"}]})


def check_ssh_keys(keys) -> list[str]:
    if not isinstance(keys, list) or not all(isinstance(k, str) for k in keys) or sum(map(len, keys)) > 65536:
        raise invalid("ssh_keys is a list of public keys (64 KiB in all)", "ssh_keys")
    for k in keys:
        try:
            H.ssh_fingerprint(k)
        except Exception:  # noqa: BLE001
            raise invalid("not an SSH public key", "ssh_keys") from None
        if any(ord(ch) < 32 for ch in k):
            raise invalid("one key per entry", "ssh_keys")
    return keys


def set_up(lab, b: dict, password: str, keys: list[str]) -> None:
    """The box's account; without it, nothing of the box stays."""
    box = lab.fabric.box
    try:
        box.account(username(b), data_dir(b), password, quota(lab, b), str(b["id"]))
        if keys:   # like on the Storage Box: they end up in the box's .ssh/authorized_keys
            box.authorized_keys(username(b), keys)
        sync_box(lab, b)
    except Exception:
        box.remove(username(b), check=False)
        box.rmtree(data_dir(b), check=False)
        with lab.state.lock:
            lab.state["storage_boxes"].pop(b["id"], None)
            lab.state.save()
        raise


def remove(lab, b: dict) -> None:
    """The box with its sub-accounts, snapshots and data."""
    state, box = lab.state, lab.fabric.box
    for x in [x for x in state["subaccounts"].values() if x["box"] == b["id"]]:
        box.remove(x["username"])
        del state["subaccounts"][x["id"]]
    for s in [s for s in state["box_snapshots"].values() if s["box"] == b["id"]]:
        del state["box_snapshots"][s["id"]]
    box.remove(username(b))
    box.rmtree(data_dir(b))
    box.rmtree(snap_dir(b))
    state["storage_boxes"].pop(b["id"], None)
    state.save()


# --- box actions: each gets the box and the request; done at once unless it returns its own (running) action -------

def change_protection(lab, b: dict, bd: dict) -> None:
    b["protection"]["delete"] = bool(bd.get("delete", b["protection"]["delete"]))


def change_type(lab, b: dict, bd: dict) -> None:
    t = box_type_name(lab.cfg, bd.get("storage_box_type"))
    if lab.cfg["storage_box_types"][t]["size_tb"] * TB < stats(lab, b)["size"]:
        raise invalid("the type offers less space than the box uses", "storage_box_type")
    b["type"] = t
    sync_box(lab, b)


def reset_password(lab, b: dict, bd: dict) -> None:
    pw = check_password(bd.get("password"))
    lab.fabric.box.account(username(b), data_dir(b), pw, quota(lab, b), str(b["id"]))
    b["_password"] = pw   # only once it is set (a lab start sets the stored one again)
    sync_box(lab, b)


def update_access_settings(lab, b: dict, bd: dict) -> None:
    old = dict(b["access"])
    b["access"].update(access(bd, b["access"]))
    try:
        sync_box(lab, b)
    except Exception:   # not in effect: not reported either
        b["access"] = old
        raise


def enable_snapshot_plan(lab, b: dict, bd: dict) -> None:
    if not 1 <= int(bd["max_snapshots"]) <= lab.cfg["storage_box_types"][b["type"]]["snapshots"]:
        raise invalid("exceeds the snapshot limit of the type", "max_snapshots")
    b["plan"] = {k: bd.get(k) for k in ("max_snapshots", "minute", "hour", "day_of_week", "day_of_month")}


def disable_snapshot_plan(lab, b: dict, bd: dict) -> None:
    b["plan"] = None


ACTIONS = {"change_protection": change_protection, "change_type": change_type, "reset_password": reset_password,
           "update_access_settings": update_access_settings, "enable_snapshot_plan": enable_snapshot_plan,
           "disable_snapshot_plan": disable_snapshot_plan, "rollback_snapshot": rollback}


# --- routes ----------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/storage_box_types")
    def types(request: Request):
        return paginate(request, filtered(request, [box_type(cfg, n) for n in cfg["storage_box_types"]]),
                        "storage_box_types")

    @app.get("/v1/storage_box_types/{tid}")
    def type_get(tid: int):
        for n in cfg["storage_box_types"]:
            if box_type_id(cfg, n) == tid:
                return {"storage_box_type": box_type(cfg, n)}
        raise not_found("Storage Box type")

    # actions: of Storage Boxes only (of one box, under its path); registered before /v1/storage_boxes/{bid}
    def box_actions(request: Request, bid: int | None) -> dict:
        q = request.query_params
        ids = {int(i) for i in q.getlist("id") if i.isdigit()}
        statuses = {s for v in q.getlist("status") for s in v.split(",")}
        items = [a for a in state["actions"].values()
                 if any(r["type"] == "storage_box" and (bid is None or r["id"] == bid) for r in a["resources"])
                 and (not ids or a["id"] in ids) and (not statuses or a["status"] in statuses)]
        return paginate(request, sorted(items, key=lambda a: a["id"]), "actions")

    @app.get("/v1/storage_boxes/actions")
    def actions(request: Request):
        return box_actions(request, None)

    @app.get("/v1/storage_boxes/actions/{aid}")
    @app.get("/v1/storage_boxes/{bid}/actions/{aid}")
    def action_get(aid: int, bid: int = 0):
        a = state["actions"].get(aid)
        if not a or not any(r["type"] == "storage_box" and (not bid or r["id"] == bid) for r in a["resources"]):
            raise not_found("action")
        return {"action": a}

    @app.get("/v1/storage_boxes/{bid}/actions")
    def actions_of_box(request: Request, bid: int):
        box_or_404(lab, bid)
        return box_actions(request, bid)

    # boxes
    @app.get("/v1/storage_boxes")
    def boxes(request: Request):
        return paginate(request, [box_json(lab, b) for b in filtered(request, list(state["storage_boxes"].values()))],
                        "storage_boxes")

    @app.post("/v1/storage_boxes", status_code=201)
    async def box_create(request: Request):
        bd = await body(request)
        t = box_type_name(cfg, bd["storage_box_type"])
        loc = resolve(lab, "location", bd["location"])
        check_unique_name(lab, bd["name"], None)
        acc = {"reachable_externally": False, "samba_enabled": False, "ssh_enabled": False, "webdav_enabled": False,
               "zfs_enabled": False}
        acc.update(access(bd.get("access_settings"), acc))
        keys = check_ssh_keys(bd.get("ssh_keys") or [])
        password = check_password(bd.get("password"))
        with state.lock:
            bid = state.new_id("storage_boxes")
            b = {"id": bid, "name": bd["name"], "type": t, "location": loc, "access": acc, "plan": None,
                 "protection": {"delete": False}, "labels": bd.get("labels") or {}, "status": "initializing",
                 "created": H.now(), "_password": password}
            state["storage_boxes"][bid] = b
            state.save()
        a = lab.new_action("create", [(bid, "storage_box")])

        async def steps():
            await lab.blocking(set_up, lab, b, password, keys)
            await lab.sleep(5)
            b["status"] = "active"
            state.save()
        lab.run_action(a, steps)
        return {"storage_box": await lab.blocking(box_json, lab, b), "action": a}

    @app.get("/v1/storage_boxes/{bid}")
    def box_get(bid: int):
        return {"storage_box": box_json(lab, box_or_404(lab, bid))}

    @app.put("/v1/storage_boxes/{bid}")
    async def box_update(bid: int, request: Request):
        b = box_or_404(lab, bid)
        bd = await body(request)
        check_unique_name(lab, bd.get("name"), b)
        update_fields(b, bd, ("name", "labels"))
        state.save()
        return {"storage_box": await lab.blocking(box_json, lab, b)}

    @app.delete("/v1/storage_boxes/{bid}", status_code=201)
    def box_delete(bid: int):
        b = box_or_404(lab, bid)
        if b["protection"]["delete"]:
            raise ApiError(423, "protected", "Storage Box is delete protected")
        with state.lock:   # not while it is set up, rolled back, copied, getting a sub-account or already going
            busy = b["status"] == "initializing" or b.get("_deleting") or b.get("_busy")
            busy = busy or any(s["box"] == bid and s.get("_copying") for s in state["box_snapshots"].values())
            busy = busy or any(a["box"] == bid and a.get("_creating") for a in state["subaccounts"].values())
            if busy:
                raise ApiError(423, "locked", "the Storage Box is busy with another action")
            b["_deleting"] = True
        a = lab.new_action("delete", [(bid, "storage_box")])

        async def steps():
            try:
                await lab.blocking(remove, lab, b)
            except Exception:
                b.pop("_deleting", None)   # it can be deleted again
                state.save()
                raise
        lab.run_action(a, steps)
        return {"action": a}

    @app.get("/v1/storage_boxes/{bid}/folders")
    def folders(bid: int, request: Request):
        b = box_or_404(lab, bid)
        path = check_home(request.query_params.get("path", "."))
        return {"folders": lab.fabric.box.folders(posixpath.join(data_dir(b), path))}

    @app.post("/v1/storage_boxes/{bid}/actions/{cmd}", status_code=201)
    async def box_action(bid: int, cmd: str, request: Request):
        b = box_or_404(lab, bid, ready=True)
        bd = await body(request)
        if cmd not in ACTIONS:
            raise ApiError(422, "unsupported_error", f"the lab does not support the action {cmd!r}")
        running = await lab.blocking(ACTIONS[cmd], lab, b, bd)
        state.save()
        return {"action": running or done(lab, cmd, [(bid, "storage_box")])}
