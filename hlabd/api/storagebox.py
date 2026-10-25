"""The Storage Box API of api.hetzner.com (served by the lab on its own address, see config.HETZNER_API): boxes,
sub-accounts, snapshots, snapshot plans, access settings. The accounts and files behind them: fabric/storagebox.py."""

import asyncio
import logging
import posixpath
from datetime import datetime, timedelta, timezone

from fastapi import FastAPI, Request

from ..fabric.storagebox import BOX, TB, data_dir, snap_dir, username
from . import hetzner as H
from .catalogue import resolve
from .spec import sort_items
from .web import ApiError, body, filtered, install, invalid, limit_exceeded, not_found, paginate

log = logging.getLogger("hlabd.box")


# --- JSON shapes -----------------------------------------------------------------------------------------------------

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


def host(user: str) -> str:
    return f"{user}.your-storagebox.de"


def box_json(lab, b: dict) -> dict:
    return {"id": b["id"], "name": b["name"], "storage_box_type": box_type(lab.cfg, b["type"]),
            "location": H.location(lab.cfg, b["location"]), "access_settings": b["access"],
            "snapshot_plan": b.get("plan"),
            "protection": b["protection"], "labels": b["labels"], "status": b["status"],
            "username": username(b) if b["status"] != "initializing" else None,
            "server": host(username(b)) if b["status"] != "initializing" else None,
            "system": f"{b['location'].upper()}-BX{b['id'] % 1000}", "stats": stats(lab, b),
            "created": b["created"]}


def subaccount_json(lab, a: dict) -> dict:
    return {"storage_box": a["box"], "id": a["id"], "name": a.get("name") or a["username"],
            "home_directory": a["home_directory"], "access_settings": a["access"],
            "description": a.get("description") or "", "labels": a["labels"], "username": a["username"],
            "server": host(a["username"]), "created": a["created"]}


def snapshot_json(lab, s: dict) -> dict:
    return {"storage_box": s["box"], "id": s["id"], "name": s["name"], "description": s.get("description") or "",
            "labels": s["labels"], "stats": {"size": s.get("size", 0), "size_filesystem": s.get("size", 0)},
            "is_automatic": s["is_automatic"], "created": s["created"]}


def stats(lab, b: dict) -> dict:
    data, snaps = lab.fabric.box.usage(data_dir(b)), lab.fabric.box.usage(snap_dir(b))
    return {"size": data + snaps, "size_data": data, "size_snapshots": snaps}


def quota(lab, b: dict) -> int:
    return int(lab.cfg["storage_box_types"][b["type"]]["size_tb"] * TB)


# --- accounts on the box ---------------------------------------------------------------------------------------------

def sync_box(lab, b: dict) -> None:
    """Access settings of the box's main account and its snapshot view (.zfs)."""
    lab.fabric.box.access(username(b), b["access"], quota(lab, b))
    lab.fabric.box.zfs_view(username(b), snap_dir(b) if b["access"]["zfs_enabled"] else None)


def sub_home(b: dict, a: dict) -> str:
    return posixpath.normpath(posixpath.join(data_dir(b), a["home_directory"].lstrip("/")))


def sync_subaccount(lab, b: dict, a: dict, password: str | None = None) -> None:
    try:
        if password is not None:
            lab.fabric.box.account(a["username"], sub_home(b, a), password, quota(lab, b), username(b))
        else:
            lab.fabric.box.home(a["username"], sub_home(b, a))
    except RuntimeError as e:
        if "leaves the Storage Box" in str(e):   # a symlink in the box pointing elsewhere (box.sh resolves it)
            raise invalid("must stay inside the Storage Box", "home_directory") from None
        raise
    lab.fabric.box.access(a["username"], a["access"], quota(lab, b))


def restore(lab) -> None:
    """After a lab start: the accounts of all boxes (their roots live in /run). Passwords stay as set (shadow is
    in the container, so they are set again from the state). A box that cannot be set up again is skipped and
    logged; the lab comes up anyway. Snapshots whose copy a restart cut off go."""
    box = lab.fabric.box
    for s in [s for s in lab.state["box_snapshots"].values() if s.get("_copying")]:
        b = lab.state["storage_boxes"].get(s["box"])
        if b:
            box.rmtree(snap_dir(b, s["name"]), check=False)
        del lab.state["box_snapshots"][s["id"]]
    box.rmtree(f"{BOX}/snapshots/.work", check=False)   # copies cut off
    for b in lab.state["storage_boxes"].values():
        b.pop("_busy", None)
    for a in lab.state["subaccounts"].values():
        a.pop("_creating", None)
    lab.state.save()
    for b in lab.state["storage_boxes"].values():
        try:
            box.account(username(b), data_dir(b), b.get("_password"), quota(lab, b), str(b["id"]))
            sync_box(lab, b)
        except Exception:  # noqa: BLE001
            log.exception("Storage Box %s not restored", b["id"])
            continue
        for a in lab.state["subaccounts"].values():
            if a["box"] != b["id"]:
                continue
            try:
                box.account(a["username"], sub_home(b, a), a.get("_password"), quota(lab, b), username(b))
                box.access(a["username"], a["access"], quota(lab, b))
            except Exception:  # noqa: BLE001
                log.exception("sub-account %s not restored", a["username"])


PASSWORD_CHARS = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÜäöüß0123456789^°!§$%/()=?+#-.,;:~*@{}_&")
PASSWORD_SPECIAL = set("^°!§$%/()=?+#-.,;:~*@{}_&")


def check_password(pw, field: str = "password") -> str:
    """Hetzner's password policy for boxes and sub-accounts (except the check against known breaches)."""
    if not isinstance(pw, str) or not 12 <= len(pw) <= 128:
        raise invalid("must be 12 to 128 characters long", field)
    if not set(pw) <= PASSWORD_CHARS:
        raise invalid("contains characters outside the allowed set", field)
    if not (any(c.isupper() for c in pw) and any(c.islower() for c in pw) and any(c.isdigit() for c in pw)
            and set(pw) & PASSWORD_SPECIAL):
        raise invalid("needs an upper case letter, a lower case letter, a number and a special character", field)
    return pw


def check_home(path: str) -> str:
    if ".." in (path or "").split("/"):
        raise invalid("must stay inside the Storage Box", "home_directory")
    p = posixpath.normpath("/" + (path or "").lstrip("/"))
    return p.lstrip("/") or "."


# --- snapshots and plans -----------------------------------------------------------------------------------------------

def access(given, known: dict) -> dict:
    """Access settings of a request: known keys only, null leaves a setting as it is (partial updates)."""
    if given is not None and not isinstance(given, dict):
        raise invalid("access_settings is an object", "access_settings")
    return {k: bool(v) for k, v in (given or {}).items() if k in known and v is not None}


def new_snapshot(lab, b: dict, automatic: bool, description: str = "", labels: dict | None = None) -> dict:
    """The record of a snapshot (the copy follows, see copy_snapshot)."""
    state = lab.state
    with state.lock:
        sid = state.new_id("box_snapshots")
        name = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H-%M-%S")
        while any(s["box"] == b["id"] and s["name"] == name for s in state["box_snapshots"].values()):
            name += "-1"
        s = {"id": sid, "box": b["id"], "name": name, "description": description, "labels": labels or {},
             "is_automatic": automatic, "created": H.now(), "size": 0, "_copying": True}
        state["box_snapshots"][sid] = s
        state.save()
    return s


def copy_snapshot(lab, b: dict, s: dict) -> None:
    """The copy of the box's data behind a snapshot record; without it the record goes again."""
    try:
        lab.fabric.box.snapshot(data_dir(b), snap_dir(b, s["name"]), str(b["id"]))
    except Exception:
        with lab.state.lock:
            lab.state["box_snapshots"].pop(s["id"], None)
            lab.state.save()
        raise
    if lab.state["box_snapshots"].get(s["id"]) is not s:   # deleted (or the lab reset) meanwhile: no orphan copy
        lab.fabric.box.rmtree(snap_dir(b, s["name"]), check=False)
        return
    s["size"] = lab.fabric.box.usage(snap_dir(b, s["name"]))
    s.pop("_copying", None)
    lab.state.save()


def take_snapshot(lab, b: dict, automatic: bool, description: str = "", labels: dict | None = None) -> dict:
    state = lab.state
    s = new_snapshot(lab, b, automatic, description, labels)
    copy_snapshot(lab, b, s)
    if automatic:  # the plan keeps max_snapshots automatic ones
        auto = sorted((x for x in state["box_snapshots"].values() if x["box"] == b["id"] and x["is_automatic"]),
                      key=lambda x: x["id"])
        for old in auto[:-max(1, b["plan"]["max_snapshots"])]:
            delete_snapshot(lab, b, old)
    state.save()
    return s


def delete_snapshot(lab, b: dict, s: dict) -> None:
    lab.fabric.box.rmtree(snap_dir(b, s["name"]))
    del lab.state["box_snapshots"][s["id"]]


def plan_due(plan: dict, now: datetime) -> bool:
    return (now.minute == plan["minute"] and now.hour == plan["hour"]
            and (plan.get("day_of_week") is None or now.isoweekday() % 7 == plan["day_of_week"] % 7)
            and (plan.get("day_of_month") is None or now.day == plan["day_of_month"]))


async def plan_scheduler(lab) -> None:
    """Automatic snapshots of all boxes with a snapshot plan, checked once a minute; minutes that went by while
    snapshots ran (an hour at most) are checked too, so a slow snapshot of one box does not skip another's plan."""
    last = datetime.now(timezone.utc).replace(second=0, microsecond=0)
    while True:
        now = datetime.now(timezone.utc)
        await asyncio.sleep(60 - now.second)
        now = datetime.now(timezone.utc).replace(second=0, microsecond=0)
        minutes = [now - timedelta(minutes=i) for i in range(min(60, int((now - last).total_seconds() // 60)))]
        last = now
        for b in list(lab.state["storage_boxes"].values()):
            if b.get("_busy") or b.get("_deleting"):   # a rollback or delete runs: the plan waits for them
                continue
            if b.get("plan") and any(plan_due(b["plan"], m) for m in minutes):
                try:
                    await lab.blocking(take_snapshot, lab, b, True)
                except Exception:  # noqa: BLE001 – one failed snapshot must not end the plans of all boxes
                    log.exception("automatic snapshot of box %s failed", b["id"])


# --- the API -------------------------------------------------------------------------------------------------------------

def create_app(lab) -> FastAPI:
    app = FastAPI(title="hlab: fake Hetzner API (Storage Boxes)", docs_url="/_docs", openapi_url="/_openapi.json")
    install(app, lab, "hetzner.json", "/v1")
    cfg, state, box = lab.cfg, lab.state, lab.fabric.box

    def box_or_404(bid: int, ready: bool = False) -> dict:
        """A box by ID; ready: it must not be busy being set up or deleted (Hetzner: 423 locked)."""
        b = state["storage_boxes"].get(bid)
        if not b:
            raise not_found("Storage Box")
        if ready and (b["status"] == "initializing" or b.get("_deleting") or b.get("_busy")):
            raise ApiError(423, "locked", "the Storage Box is busy with another action")
        return b

    def unique_sub_name(mine: list[dict], name, me: dict | None) -> None:
        """Sub-account names are unique per box (an unnamed one is called by its username)."""
        if name and any(x is not me and (x.get("name") or x["username"]) == name for x in mine):
            raise ApiError(409, "uniqueness_error", "a sub-account has this name already", {"fields": [{"name": "name"}]})

    def sub(b: dict, aid: int) -> dict:
        a = state["subaccounts"].get(aid)
        if not a or a["box"] != b["id"]:
            raise not_found("subaccount")
        return a

    def snap(b: dict, ref) -> dict:
        for s in state["box_snapshots"].values():
            if s["box"] == b["id"] and str(ref) in (str(s["id"]), s["name"]):
                return s
        raise not_found("snapshot")

    def action(cmd: str, resources: list[tuple[int, str]]) -> dict:
        a = lab.new_action(cmd, resources)
        lab.finish(a)
        return a

    # catalogue
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

    # actions
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
        a = state["actions"].get(aid)   # Storage Box actions only (of that box, under its path)
        if not a or not any(r["type"] == "storage_box" and (not bid or r["id"] == bid) for r in a["resources"]):
            raise not_found("action")
        return {"action": a}

    @app.get("/v1/storage_boxes/{bid}/actions")
    def actions_of_box(request: Request, bid: int):
        box_or_404(bid)
        return box_actions(request, bid)

    # boxes
    @app.get("/v1/storage_boxes")
    def boxes(request: Request):
        items = filtered(request, list(state["storage_boxes"].values()))
        return paginate(request, [box_json(lab, b) for b in items], "storage_boxes")

    @app.post("/v1/storage_boxes", status_code=201)
    async def box_create(request: Request):
        bd = await body(request)
        t = next((n for n in cfg["storage_box_types"] if str(bd["storage_box_type"]) in (n, str(box_type_id(cfg, n)))),
                 None)
        if not t:
            raise invalid("Storage Box type not found", "storage_box_type")
        loc = resolve(lab, "location", bd["location"])
        if any(b["name"] == bd["name"] for b in state["storage_boxes"].values()):
            raise ApiError(409, "uniqueness_error", "Storage Box name is already used", {"fields": [{"name": "name"}]})
        acc = {"reachable_externally": False, "samba_enabled": False, "ssh_enabled": False, "webdav_enabled": False,
               "zfs_enabled": False}
        acc.update(access(bd.get("access_settings"), acc))
        keys = bd.get("ssh_keys") or []
        if not isinstance(keys, list) or not all(isinstance(k, str) for k in keys) or sum(map(len, keys)) > 65536:
            raise invalid("ssh_keys is a list of public keys (64 KiB in all)", "ssh_keys")
        for k in keys:
            try:
                H.ssh_fingerprint(k)
            except Exception:  # noqa: BLE001
                raise invalid("not an SSH public key", "ssh_keys") from None
            if any(ord(ch) < 32 for ch in k):
                raise invalid("one key per entry", "ssh_keys")
        with state.lock:
            bid = state.new_id("storage_boxes")
            b = {"id": bid, "name": bd["name"], "type": t, "location": loc, "access": acc, "plan": None,
                 "protection": {"delete": False}, "labels": bd.get("labels") or {}, "status": "initializing",
                 "created": H.now(), "_password": check_password(bd.get("password"))}
            state["storage_boxes"][bid] = b
            state.save()
        a = lab.new_action("create", [(bid, "storage_box")])

        async def steps():
            try:
                await lab.blocking(box.account, username(b), data_dir(b), bd["password"], quota(lab, b),
                                   str(b["id"]))
                if bd.get("ssh_keys"):  # like on the Storage Box: they end up in the box's .ssh/authorized_keys
                    await lab.blocking(box.authorized_keys, username(b), bd["ssh_keys"])
                await lab.blocking(sync_box, lab, b)
            except Exception:   # no box stays "initializing" without its account
                await lab.blocking(box.remove, username(b), check=False)
                await lab.blocking(box.rmtree, data_dir(b), check=False)
                with state.lock:
                    state["storage_boxes"].pop(bid, None)
                    state.save()
                raise
            await lab.sleep(5)
            b["status"] = "active"
            state.save()
        lab.run_action(a, steps)
        return {"storage_box": await lab.blocking(box_json, lab, b), "action": a}   # du runs off the loop

    @app.get("/v1/storage_boxes/{bid}")
    def box_get(bid: int):
        return {"storage_box": box_json(lab, box_or_404(bid))}

    @app.put("/v1/storage_boxes/{bid}")
    async def box_update(bid: int, request: Request):
        b = box_or_404(bid)
        bd = await body(request)
        if bd.get("name") and any(x["name"] == bd["name"] and x is not b for x in state["storage_boxes"].values()):
            raise ApiError(409, "uniqueness_error", "Storage Box name is already used", {"fields": [{"name": "name"}]})
        for k in ("name", "labels"):
            if k in bd:
                b[k] = bd[k] if k != "labels" else (bd[k] or {})
        state.save()
        return {"storage_box": await lab.blocking(box_json, lab, b)}

    @app.delete("/v1/storage_boxes/{bid}", status_code=201)
    def box_delete(bid: int):
        b = box_or_404(bid)
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
                await remove_box()
            except Exception:
                b.pop("_deleting", None)   # it can be deleted again
                state.save()
                raise

        async def remove_box():
            for x in [x for x in state["subaccounts"].values() if x["box"] == bid]:
                await lab.blocking(box.remove, x["username"])
                del state["subaccounts"][x["id"]]
            for s in [s for s in state["box_snapshots"].values() if s["box"] == bid]:
                del state["box_snapshots"][s["id"]]
            await lab.blocking(box.remove, username(b))
            await lab.blocking(box.rmtree, data_dir(b))
            await lab.blocking(box.rmtree, snap_dir(b))
            state["storage_boxes"].pop(bid, None)
            state.save()
        lab.run_action(a, steps)
        return {"action": a}

    @app.get("/v1/storage_boxes/{bid}/folders")
    def folders(bid: int, request: Request):
        b = box_or_404(bid)
        path = check_home(request.query_params.get("path", "."))
        return {"folders": box.folders(posixpath.join(data_dir(b), path))}

    @app.post("/v1/storage_boxes/{bid}/actions/{cmd}", status_code=201)
    async def box_action(bid: int, cmd: str, request: Request):
        b = box_or_404(bid, ready=True)
        bd = await body(request)
        if cmd == "change_protection":
            b["protection"]["delete"] = bool(bd.get("delete", b["protection"]["delete"]))
        elif cmd == "change_type":
            t = next((n for n in cfg["storage_box_types"]
                      if str(bd.get("storage_box_type")) in (n, str(box_type_id(cfg, n)))), None)
            if not t:
                raise invalid("Storage Box type not found", "storage_box_type")
            used = (await lab.blocking(stats, lab, b))["size"]
            if cfg["storage_box_types"][t]["size_tb"] * TB < used:
                raise invalid("the type offers less space than the box uses", "storage_box_type")
            b["type"] = t
            await lab.blocking(sync_box, lab, b)
        elif cmd == "reset_password":
            pw = check_password(bd.get("password"))
            await lab.blocking(box.account, username(b), data_dir(b), bd["password"], quota(lab, b),
                               str(b["id"]))
            b["_password"] = pw   # only once it is set (a lab start sets the stored one again)
            await lab.blocking(sync_box, lab, b)
        elif cmd == "update_access_settings":
            old = dict(b["access"])
            b["access"].update(access(bd, b["access"]))
            try:
                await lab.blocking(sync_box, lab, b)
            except Exception:   # not in effect: not reported either
                b["access"] = old
                raise
        elif cmd == "enable_snapshot_plan":
            if not 1 <= int(bd["max_snapshots"]) <= cfg["storage_box_types"][b["type"]]["snapshots"]:
                raise invalid("exceeds the snapshot limit of the type", "max_snapshots")
            b["plan"] = {k: bd.get(k) for k in ("max_snapshots", "minute", "hour", "day_of_week", "day_of_month")}
        elif cmd == "disable_snapshot_plan":
            b["plan"] = None
        elif cmd == "rollback_snapshot":
            s = snap(b, bd.get("snapshot"))
            with state.lock:   # the box is busy: no snapshot is taken, deleted or pruned meanwhile
                if s.get("_copying"):
                    raise ApiError(423, "locked", "the snapshot is still being taken")
                if b.get("_busy"):
                    raise ApiError(423, "locked", "the Storage Box is busy with another action")
                b["_busy"] = True
            a = lab.new_action("rollback_snapshot", [(bid, "storage_box"), (s["id"], "storage_box_snapshot")])

            async def steps():
                try:
                    await rollback()
                finally:
                    b.pop("_busy", None)

            async def rollback():
                await lab.blocking(box.rollback, snap_dir(b, s["name"]), data_dir(b), str(b["id"]))
                # sub-account homes are bound to directories; the rollback may have replaced them (Hetzner
                # resolves a home by its path)
                for x in [x for x in state["subaccounts"].values() if x["box"] == bid]:
                    try:
                        await lab.blocking(sync_subaccount, lab, b, x)
                    except Exception:  # noqa: BLE001 – a home the snapshot does not have: as before
                        log.exception("sub-account %s keeps its home after the rollback", x["username"])
                for newer in [x for x in state["box_snapshots"].values() if x["box"] == bid and x["id"] > s["id"]]:
                    await lab.blocking(delete_snapshot, lab, b, newer)   # Hetzner removes them too
                state.save()
            lab.run_action(a, steps)
            return {"action": a}
        else:
            raise ApiError(422, "unsupported_error", f"the lab does not support the action {cmd!r}")
        state.save()
        return {"action": action(cmd, [(bid, "storage_box")])}

    # sub-accounts
    @app.get("/v1/storage_boxes/{bid}/subaccounts")
    def subaccounts(bid: int, request: Request):
        box_or_404(bid)
        items = filtered(request, [a for a in state["subaccounts"].values() if a["box"] == bid],
                         {"username": lambda a, v: a["username"] == v,
                          "name": lambda a, v: (a.get("name") or a["username"]) == v})   # the default name
        return {"subaccounts": sort_items([subaccount_json(lab, a) for a in items], request.query_params.getlist("sort"))}

    @app.post("/v1/storage_boxes/{bid}/subaccounts", status_code=201)
    async def subaccount_create(bid: int, request: Request):
        b = box_or_404(bid, ready=True)
        bd = await body(request)
        acc = {"reachable_externally": False, "samba_enabled": False, "ssh_enabled": False, "webdav_enabled": False,
               "readonly": False}
        acc.update(access(bd.get("access_settings"), acc))
        home, password = check_home(bd.get("home_directory")), check_password(bd.get("password"))
        with state.lock:   # the number is taken here: a second create at the same time gets the next one
            mine = [a for a in state["subaccounts"].values() if a["box"] == bid]
            unique_sub_name(mine, bd.get("name"), None)
            if len(mine) >= cfg["storage_box_types"][b["type"]]["subaccounts"]:
                raise limit_exceeded("sub-account limit of the Storage Box type reached", "subaccounts")
            n = 1 + max([a["n"] for a in mine], default=0)
            if b.get("_deleting") or b.get("_busy"):   # the box started going meanwhile
                raise ApiError(423, "locked", "the Storage Box is busy with another action")
            aid = state.new_id("subaccounts")
            a = {"id": aid, "box": bid, "n": n, "username": f"{username(b)}-sub{n}", "name": bd.get("name"),
                 "home_directory": home, "access": acc, "description": bd.get("description") or "",
                 "labels": bd.get("labels") or {}, "created": H.now(), "_password": password,
                 "_creating": True}   # the box is not deleted while its account is set up
            state["subaccounts"][aid] = a
        try:
            await lab.blocking(sync_subaccount, lab, b, a, password)
        except Exception:
            await lab.blocking(box.remove, a["username"], check=False)
            with state.lock:
                state["subaccounts"].pop(aid, None)
            raise
        finally:
            a.pop("_creating", None)
        state.save()
        return {"subaccount": subaccount_json(lab, a),
                "action": action("create_subaccount", [(bid, "storage_box"), (aid, "storage_box_subaccount")])}

    @app.get("/v1/storage_boxes/{bid}/subaccounts/{aid}")
    def subaccount_get(bid: int, aid: int):
        return {"subaccount": subaccount_json(lab, sub(box_or_404(bid), aid))}

    @app.put("/v1/storage_boxes/{bid}/subaccounts/{aid}")
    async def subaccount_update(bid: int, aid: int, request: Request):
        a = sub(box_or_404(bid), aid)
        bd = await body(request)
        unique_sub_name([x for x in state["subaccounts"].values() if x["box"] == bid], bd.get("name"), a)
        for k in ("name", "description", "labels"):
            if k in bd:
                a[k] = bd[k] if k != "labels" else (bd[k] or {})
        state.save()
        return {"subaccount": subaccount_json(lab, a)}

    @app.delete("/v1/storage_boxes/{bid}/subaccounts/{aid}", status_code=201)
    def subaccount_delete(bid: int, aid: int):
        a = sub(box_or_404(bid, ready=True), aid)
        box.remove(a["username"])
        del state["subaccounts"][aid]
        state.save()
        return {"action": action("delete_subaccount", [(bid, "storage_box"), (aid, "storage_box_subaccount")])}

    @app.post("/v1/storage_boxes/{bid}/subaccounts/{aid}/actions/{cmd}", status_code=201)
    async def subaccount_action(bid: int, aid: int, cmd: str, request: Request):
        b = box_or_404(bid, ready=True)
        a = sub(b, aid)
        bd = await body(request)
        if cmd == "change_home_directory":
            old, a["home_directory"] = a["home_directory"], check_home(bd["home_directory"])
            try:
                await lab.blocking(sync_subaccount, lab, b, a)
            except Exception:
                a["home_directory"] = old
                raise
        elif cmd == "reset_subaccount_password":
            a["_password"] = check_password(bd.get("password"))
            await lab.blocking(sync_subaccount, lab, b, a, bd["password"])
        elif cmd == "update_access_settings":
            old = dict(a["access"])
            a["access"].update(access(bd, a["access"]))
            try:
                await lab.blocking(sync_subaccount, lab, b, a)
            except Exception:
                a["access"] = old
                raise
        else:
            raise ApiError(422, "unsupported_error", f"the lab does not support the action {cmd!r}")
        state.save()
        return {"action": action(cmd, [(bid, "storage_box"), (aid, "storage_box_subaccount")])}

    # snapshots
    @app.get("/v1/storage_boxes/{bid}/snapshots")
    def snapshots(bid: int, request: Request):
        box_or_404(bid)
        items = filtered(request, [s for s in state["box_snapshots"].values() if s["box"] == bid],
                         {"is_automatic": lambda s, v: str(s["is_automatic"]).lower() == v.lower()})
        return {"snapshots": sort_items([snapshot_json(lab, s) for s in items], request.query_params.getlist("sort"))}

    @app.post("/v1/storage_boxes/{bid}/snapshots", status_code=201)
    async def snapshot_create(bid: int, request: Request):
        b = box_or_404(bid, ready=True)
        bd = await body(request)
        manual = [s for s in state["box_snapshots"].values() if s["box"] == bid and not s["is_automatic"]]
        if len(manual) >= cfg["storage_box_types"][b["type"]]["snapshots"]:
            raise limit_exceeded("snapshot limit of the Storage Box type reached", "snapshots")
        s = new_snapshot(lab, b, False, bd.get("description") or "", bd.get("labels"))
        a = lab.new_action("create_snapshot", [(bid, "storage_box"), (s["id"], "storage_box_snapshot")])

        async def steps():   # as on Hetzner, the snapshot is there at once and the action copies
            await lab.blocking(copy_snapshot, lab, b, s)
        lab.run_action(a, steps)
        return {"snapshot": snapshot_json(lab, s), "action": a}

    @app.get("/v1/storage_boxes/{bid}/snapshots/{ref}")
    def snapshot_get(bid: int, ref: str):
        return {"snapshot": snapshot_json(lab, snap(box_or_404(bid), ref))}

    @app.put("/v1/storage_boxes/{bid}/snapshots/{ref}")
    async def snapshot_update(bid: int, ref: str, request: Request):
        s = snap(box_or_404(bid), ref)
        bd = await body(request)
        for k in ("description", "labels"):
            if k in bd:
                s[k] = bd[k] if k != "labels" else (bd[k] or {})
        state.save()
        return {"snapshot": snapshot_json(lab, s)}

    @app.delete("/v1/storage_boxes/{bid}/snapshots/{ref}", status_code=201)
    def snapshot_delete(bid: int, ref: str):
        b = box_or_404(bid, ready=True)
        s = snap(b, ref)
        delete_snapshot(lab, b, s)
        state.save()
        return {"action": action("delete_snapshot", [(bid, "storage_box"), (s["id"], "storage_box_snapshot")])}

    return app
