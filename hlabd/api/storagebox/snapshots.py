"""Snapshots of a box: taken by hand or by its snapshot plan, rolled back to, deleted. As on Hetzner a snapshot is
there at once and its action copies the data (the record is marked `_copying` until then)."""

import asyncio
import logging
from datetime import datetime, timedelta, timezone

from fastapi import FastAPI, Request

from ...fabric.storagebox import data_dir, snap_dir
from .. import hetzner as H
from ..spec import sort_items
from ..web import ApiError, body, filtered, limit_exceeded, not_found
from .base import box_or_404, done, sync_subaccount, update_fields

log = logging.getLogger("hlabd.box")


def snapshot_json(s: dict) -> dict:
    return {"storage_box": s["box"], "id": s["id"], "name": s["name"], "description": s.get("description") or "",
            "labels": s["labels"], "stats": {"size": s.get("size", 0), "size_filesystem": s.get("size", 0)},
            "is_automatic": s["is_automatic"], "created": s["created"]}


def snapshot_or_404(lab, b: dict, ref) -> dict:
    """A snapshot of the box, by ID or name."""
    for s in lab.state["box_snapshots"].values():
        if s["box"] == b["id"] and str(ref) in (str(s["id"]), s["name"]):
            return s
    raise not_found("snapshot")


def new_snapshot(lab, b: dict, automatic: bool, description: str = "", labels: dict | None = None) -> dict:
    """The record of a snapshot, named by the time (the copy follows, see copy_snapshot)."""
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


def take_snapshot(lab, b: dict, automatic: bool) -> dict:
    """A snapshot taken by the plan, which keeps max_snapshots automatic ones."""
    state = lab.state
    s = new_snapshot(lab, b, automatic)
    copy_snapshot(lab, b, s)
    if automatic:
        auto = sorted((x for x in state["box_snapshots"].values() if x["box"] == b["id"] and x["is_automatic"]),
                      key=lambda x: x["id"])
        for old in auto[:-max(1, b["plan"]["max_snapshots"])]:
            delete_snapshot(lab, b, old)
    state.save()
    return s


def delete_snapshot(lab, b: dict, s: dict) -> None:
    lab.fabric.box.rmtree(snap_dir(b, s["name"]))
    del lab.state["box_snapshots"][s["id"]]


def rollback(lab, b: dict, bd: dict) -> dict:
    """The box action rollback_snapshot: the data goes back to the snapshot, newer snapshots go (as on Hetzner).
    The box is busy meanwhile: no snapshot is taken, deleted or pruned."""
    state = lab.state
    s = snapshot_or_404(lab, b, bd.get("snapshot"))
    with state.lock:
        if s.get("_copying"):
            raise ApiError(423, "locked", "the snapshot is still being taken")
        if b.get("_busy"):
            raise ApiError(423, "locked", "the Storage Box is busy with another action")
        b["_busy"] = True
    a = lab.new_action("rollback_snapshot", [(b["id"], "storage_box"), (s["id"], "storage_box_snapshot")])

    async def steps():
        try:
            await lab.blocking(lab.fabric.box.rollback, snap_dir(b, s["name"]), data_dir(b), str(b["id"]))
            for x in [x for x in state["subaccounts"].values() if x["box"] == b["id"]]:   # homes are paths
                try:
                    await lab.blocking(sync_subaccount, lab, b, x)
                except Exception:  # noqa: BLE001 – a home the snapshot does not have: as before
                    log.exception("sub-account %s keeps its home after the rollback", x["username"])
            for newer in [x for x in state["box_snapshots"].values() if x["box"] == b["id"] and x["id"] > s["id"]]:
                await lab.blocking(delete_snapshot, lab, b, newer)
            state.save()
        finally:
            b.pop("_busy", None)
    lab.run_action(a, steps)
    return a


# --- snapshot plans --------------------------------------------------------------------------------------------------

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


# --- routes ----------------------------------------------------------------------------------------------------------

def add_routes(app: FastAPI, lab) -> None:
    cfg, state = lab.cfg, lab.state

    @app.get("/v1/storage_boxes/{bid}/snapshots")
    def snapshots(bid: int, request: Request):
        box_or_404(lab, bid)
        items = filtered(request, [s for s in state["box_snapshots"].values() if s["box"] == bid],
                         {"is_automatic": lambda s, v: str(s["is_automatic"]).lower() == v.lower()})
        return {"snapshots": sort_items([snapshot_json(s) for s in items], request.query_params.getlist("sort"))}

    @app.post("/v1/storage_boxes/{bid}/snapshots", status_code=201)
    async def snapshot_create(bid: int, request: Request):
        b = box_or_404(lab, bid, ready=True)
        bd = await body(request)
        manual = [s for s in state["box_snapshots"].values() if s["box"] == bid and not s["is_automatic"]]
        if len(manual) >= cfg["storage_box_types"][b["type"]]["snapshots"]:
            raise limit_exceeded("snapshot limit of the Storage Box type reached", "snapshots")
        s = new_snapshot(lab, b, False, bd.get("description") or "", bd.get("labels"))
        a = lab.new_action("create_snapshot", [(bid, "storage_box"), (s["id"], "storage_box_snapshot")])

        async def steps():
            await lab.blocking(copy_snapshot, lab, b, s)
        lab.run_action(a, steps)
        return {"snapshot": snapshot_json(s), "action": a}

    @app.get("/v1/storage_boxes/{bid}/snapshots/{ref}")
    def snapshot_get(bid: int, ref: str):
        return {"snapshot": snapshot_json(snapshot_or_404(lab, box_or_404(lab, bid), ref))}

    @app.put("/v1/storage_boxes/{bid}/snapshots/{ref}")
    async def snapshot_update(bid: int, ref: str, request: Request):
        s = snapshot_or_404(lab, box_or_404(lab, bid), ref)
        update_fields(s, await body(request), ("description", "labels"))
        state.save()
        return {"snapshot": snapshot_json(s)}

    @app.delete("/v1/storage_boxes/{bid}/snapshots/{ref}", status_code=201)
    def snapshot_delete(bid: int, ref: str):
        b = box_or_404(lab, bid, ready=True)
        s = snapshot_or_404(lab, b, ref)
        delete_snapshot(lab, b, s)
        state.save()
        return {"action": done(lab, "delete_snapshot", [(bid, "storage_box"), (s["id"], "storage_box_snapshot")])}
