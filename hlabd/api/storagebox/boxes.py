"""Storage Boxes: their types, the boxes themselves, their actions and folders."""

import posixpath

from fastapi import FastAPI, Request

from ...errors import ApiError, invalid, locked, not_found, unique_name, unsupported
from ...fabric.storagebox import TB, data_dir, snap_dir, username
from ...lab import now
from .. import hetzner as H
from ..catalogue import resolve
from ..web import read_body, filtered, paginate
from .base import access, box_or_404, check_home, check_password, quota, sync_box, update_fields
from .snapshots import rollback


def box_type_id(cfg: dict, name: str) -> int:
    return 1330 + list(cfg["storage_box_types"]).index(name)


def box_type(cfg: dict, name: str) -> dict:
    entry = cfg["storage_box_types"][name]
    prices = [{"location": loc, "price_hourly": {"net": "0.0050", "gross": "0.0060"},
               "price_monthly": {"net": "3.2000", "gross": "3.8080"}, "setup_fee": {"net": "0.0000", "gross": "0.0000"}}
              for loc in cfg["locations"]]
    return {"id": box_type_id(cfg, name), "name": name, "description": entry.get("description", name.upper()),
            "snapshot_limit": entry["snapshots"], "automatic_snapshot_limit": entry["snapshots"],
            "subaccounts_limit": entry["subaccounts"], "size": int(entry["size_tb"] * TB), "prices": prices,
            "deprecation": None}


def box_type_name(cfg: dict, ref) -> str:
    """A box type by name or ID."""
    for n in cfg["storage_box_types"]:
        if str(ref) in (n, str(box_type_id(cfg, n))):
            return n
    raise invalid("Storage Box type not found", "storage_box_type")


def stats(lab, box: dict) -> dict:
    data, snaps = lab.fabric.box.usage(data_dir(box)), lab.fabric.box.usage(snap_dir(box))
    return {"size": data + snaps, "size_data": data, "size_snapshots": snaps}


def box_json(lab, box: dict) -> dict:
    """Reads the box's usage from disk: call it off the event loop."""
    ready = box["status"] != "initializing"
    return {"id": box["id"], "name": box["name"], "storage_box_type": box_type(lab.cfg, box["type"]),
            "location": H.location_json(lab.cfg, box["location"]), "access_settings": box["access"],
            "snapshot_plan": box.get("plan"), "protection": box["protection"], "labels": box["labels"],
            "status": box["status"], "username": username(box) if ready else None,
            "server": f"{username(box)}.your-storagebox.de" if ready else None,
            "system": f"{box['location'].upper()}-BX{box['id'] % 1000}", "stats": stats(lab, box),
            "created": box["created"]}


def check_unique_name(lab, name, current: dict | None) -> None:
    unique_name(lab.state["storage_boxes"].values(), name, current, "Storage Box")


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


def set_up(lab, box: dict, password: str, keys: list[str]) -> None:
    """The box's account; without it, nothing of the box stays."""
    try:
        lab.fabric.box.account(username(box), data_dir(box), password, quota(lab, box), str(box["id"]))
        if keys:   # like on the Storage Box: they end up in the box's .ssh/authorized_keys
            lab.fabric.box.authorized_keys(username(box), keys)
        sync_box(lab, box)
    except Exception:
        lab.fabric.box.remove(username(box), check=False)
        lab.fabric.box.rmtree(data_dir(box), check=False)
        with lab.state.lock:
            lab.state["storage_boxes"].pop(box["id"], None)
            lab.state.save()
        raise


def remove(lab, box: dict) -> None:
    """The box with its sub-accounts, snapshots and data."""
    state = lab.state
    for x in [x for x in state["subaccounts"].values() if x["box"] == box["id"]]:
        lab.fabric.box.remove(x["username"])
        del state["subaccounts"][x["id"]]
    for s in [s for s in state["box_snapshots"].values() if s["box"] == box["id"]]:
        del state["box_snapshots"][s["id"]]
    lab.fabric.box.remove(username(box))
    lab.fabric.box.rmtree(data_dir(box))
    lab.fabric.box.rmtree(snap_dir(box))
    state["storage_boxes"].pop(box["id"], None)
    state.save()


# --- box actions: each gets the box and the request; done at once unless it returns its own (running) action -------

def change_protection(lab, box: dict, body: dict) -> None:
    box["protection"]["delete"] = bool(body.get("delete", box["protection"]["delete"]))


def change_type(lab, box: dict, body: dict) -> None:
    type_name = box_type_name(lab.cfg, body.get("storage_box_type"))
    if lab.cfg["storage_box_types"][type_name]["size_tb"] * TB < stats(lab, box)["size"]:
        raise invalid("the type offers less space than the box uses", "storage_box_type")
    box["type"] = type_name
    sync_box(lab, box)
    for a in [a for a in lab.state["subaccounts"].values() if a["box"] == box["id"]]:   # they share the quota
        lab.fabric.box.access(a["username"], a["access"], quota(lab, box))


def reset_password(lab, box: dict, body: dict) -> None:
    password = check_password(body.get("password"))
    lab.fabric.box.account(username(box), data_dir(box), password, quota(lab, box), str(box["id"]))
    box["_password"] = password   # only once it is set (a lab start sets the stored one again)
    sync_box(lab, box)


def update_access_settings(lab, box: dict, body: dict) -> None:
    old = dict(box["access"])
    box["access"].update(access(body, box["access"]))
    try:
        sync_box(lab, box)
    except Exception:   # not in effect: not reported either
        box["access"] = old
        raise


def enable_snapshot_plan(lab, box: dict, body: dict) -> None:
    if not 1 <= int(body["max_snapshots"]) <= lab.cfg["storage_box_types"][box["type"]]["snapshots"]:
        raise invalid("exceeds the snapshot limit of the type", "max_snapshots")
    box["plan"] = {k: body.get(k) for k in ("max_snapshots", "minute", "hour", "day_of_week", "day_of_month")}


def disable_snapshot_plan(lab, box: dict, body: dict) -> None:
    box["plan"] = None


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
        query = request.query_params
        ids = {int(i) for i in query.getlist("id") if i.isdigit()}
        statuses = {s for v in query.getlist("status") for s in v.split(",")}
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
        action = state["actions"].get(aid)
        mine = action and any(r["type"] == "storage_box" and (not bid or r["id"] == bid) for r in action["resources"])
        if not mine:
            raise not_found("action")
        return {"action": action}

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
        body = await read_body(request)
        type_name = box_type_name(cfg, body["storage_box_type"])
        location = resolve(lab, "location", body["location"])
        check_unique_name(lab, body["name"], None)
        access_settings = {"reachable_externally": False, "samba_enabled": False, "ssh_enabled": False,
                           "webdav_enabled": False, "zfs_enabled": False}
        access_settings.update(access(body.get("access_settings"), access_settings))
        keys = check_ssh_keys(body.get("ssh_keys") or [])
        password = check_password(body.get("password"))
        with state.lock:
            bid = state.new_id("storage_boxes")
            box = {"id": bid, "name": body["name"], "type": type_name, "location": location, "access": access_settings,
                   "plan": None,
                   "protection": {"delete": False}, "labels": body.get("labels") or {}, "status": "initializing",
                   "created": now(), "_password": password}
            state["storage_boxes"][bid] = box
            state.save()
        action = lab.new_action("create", [(bid, "storage_box")])

        async def steps():
            await lab.blocking(set_up, lab, box, password, keys)
            await lab.sleep(5)
            box["status"] = "active"
            state.save()
        lab.run_action(action, steps)
        return {"storage_box": await lab.blocking(box_json, lab, box), "action": action}

    @app.get("/v1/storage_boxes/{bid}")
    def box_get(bid: int):
        return {"storage_box": box_json(lab, box_or_404(lab, bid))}

    @app.put("/v1/storage_boxes/{bid}")
    async def box_update(bid: int, request: Request):
        box = box_or_404(lab, bid)
        body = await read_body(request)
        check_unique_name(lab, body.get("name"), box)
        update_fields(box, body, ("name", "labels"))
        state.save()
        return {"storage_box": await lab.blocking(box_json, lab, box)}

    @app.delete("/v1/storage_boxes/{bid}", status_code=201)
    def box_delete(bid: int):
        box = box_or_404(lab, bid)
        if box["protection"]["delete"]:
            raise ApiError(423, "protected", "Storage Box is delete protected")
        with state.lock:   # not while it is set up, rolled back, copied, getting a sub-account or already going
            busy = box["status"] == "initializing" or box.get("_deleting") or box.get("_busy")
            busy = busy or any(s["box"] == bid and s.get("_copying") for s in state["box_snapshots"].values())
            busy = busy or any(a["box"] == bid and a.get("_creating") for a in state["subaccounts"].values())
            if busy:
                raise locked("the Storage Box is busy with another action")
            box["_deleting"] = True
        action = lab.new_action("delete", [(bid, "storage_box")])

        async def steps():
            try:
                await lab.blocking(remove, lab, box)
            except Exception:
                box.pop("_deleting", None)   # it can be deleted again
                state.save()
                raise
        lab.run_action(action, steps)
        return {"action": action}

    @app.get("/v1/storage_boxes/{bid}/folders")
    def folders(bid: int, request: Request):
        box = box_or_404(lab, bid)
        path = check_home(request.query_params.get("path", "."))
        return {"folders": lab.fabric.box.folders(posixpath.join(data_dir(box), path))}

    @app.post("/v1/storage_boxes/{bid}/actions/{cmd}", status_code=201)
    async def box_action(bid: int, cmd: str, request: Request):
        box = box_or_404(lab, bid, ready=True)
        body = await read_body(request)
        if cmd not in ACTIONS:
            raise unsupported(f"the action {cmd!r}")
        running = await lab.blocking(ACTIONS[cmd], lab, box, body)
        state.save()
        return {"action": running or lab.done(cmd, [(bid, "storage_box")])}
