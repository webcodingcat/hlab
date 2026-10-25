"""Snapshots of a box: taken by hand or by its snapshot plan, rolled back to, deleted. As on Hetzner a snapshot is
there at once and its action copies the data (the record is marked `_copying` until then)."""

import asyncio
import logging
from datetime import datetime, timedelta, timezone

from fastapi import FastAPI, Request

from ...errors import limit_exceeded, locked, not_found
from ...fabric.storagebox import data_dir, snap_dir
from ...lab import now
from ..spec import sort_items
from ..web import read_body, filtered
from .base import box_or_404, sync_subaccount, update_fields

log = logging.getLogger("hlabd.box")


def snapshot_json(snapshot: dict) -> dict:
    size = snapshot.get("size", 0)
    return {"storage_box": snapshot["box"], "id": snapshot["id"], "name": snapshot["name"],
            "description": snapshot.get("description") or "", "labels": snapshot["labels"],
            "stats": {"size": size, "size_filesystem": size},
            "is_automatic": snapshot["is_automatic"], "created": snapshot["created"]}


def snapshot_or_404(lab, box: dict, ref) -> dict:
    """A snapshot of the box, by ID or name."""
    for s in lab.state["box_snapshots"].values():
        if s["box"] == box["id"] and str(ref) in (str(s["id"]), s["name"]):
            return s
    raise not_found("snapshot")


def new_snapshot(lab, box: dict, automatic: bool, description: str = "", labels: dict | None = None) -> dict:
    """The record of a snapshot, named by the time (the copy follows, see copy_snapshot)."""
    state = lab.state
    with state.lock:
        sid = state.new_id("box_snapshots")
        name = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H-%M-%S")
        while any(s["box"] == box["id"] and s["name"] == name for s in state["box_snapshots"].values()):
            name += "-1"
        snapshot = {"id": sid, "box": box["id"], "name": name, "description": description, "labels": labels or {},
                    "is_automatic": automatic, "created": now(), "size": 0, "_copying": True}
        state["box_snapshots"][sid] = snapshot
        state.save()
    return snapshot


def copy_snapshot(lab, box: dict, snapshot: dict) -> None:
    """The copy of the box's data behind a snapshot record; without it the record goes again."""
    try:
        lab.fabric.box.snapshot(data_dir(box), snap_dir(box, snapshot["name"]), str(box["id"]))
    except Exception:
        with lab.state.lock:
            lab.state["box_snapshots"].pop(snapshot["id"], None)
            lab.state.save()
        raise
    if lab.state["box_snapshots"].get(snapshot["id"]) is not snapshot:   # deleted (or a lab reset): no orphan copy
        lab.fabric.box.rmtree(snap_dir(box, snapshot["name"]), check=False)
        return
    snapshot["size"] = lab.fabric.box.usage(snap_dir(box, snapshot["name"]))
    snapshot.pop("_copying", None)
    lab.state.save()


def take_snapshot(lab, box: dict, automatic: bool) -> dict:
    """A snapshot taken by the plan, which keeps max_snapshots automatic ones."""
    state = lab.state
    snapshot = new_snapshot(lab, box, automatic)
    copy_snapshot(lab, box, snapshot)
    if automatic:
        auto = sorted((x for x in state["box_snapshots"].values() if x["box"] == box["id"] and x["is_automatic"]),
                      key=lambda x: x["id"])
        for old in auto[:-max(1, box["plan"]["max_snapshots"])]:
            delete_snapshot(lab, box, old)
    state.save()
    return snapshot


def delete_snapshot(lab, box: dict, snapshot: dict) -> None:
    lab.fabric.box.rmtree(snap_dir(box, snapshot["name"]))
    del lab.state["box_snapshots"][snapshot["id"]]


def rollback(lab, box: dict, body: dict) -> dict:
    """The box action rollback_snapshot: the data goes back to the snapshot, newer snapshots go (as on Hetzner).
    The box is busy meanwhile: no snapshot is taken, deleted or pruned."""
    state = lab.state
    snapshot = snapshot_or_404(lab, box, body.get("snapshot"))
    with state.lock:
        if snapshot.get("_copying"):
            raise locked("the snapshot is still being taken")
        if box.get("_busy"):
            raise locked("the Storage Box is busy with another action")
        box["_busy"] = True
    action = lab.new_action("rollback_snapshot", [(box["id"], "storage_box"), (snapshot["id"], "storage_box_snapshot")])

    async def steps():
        try:
            await lab.blocking(lab.fabric.box.rollback, snap_dir(box, snapshot["name"]), data_dir(box), str(box["id"]))
            for x in [x for x in state["subaccounts"].values() if x["box"] == box["id"]]:   # homes are paths
                try:
                    await lab.blocking(sync_subaccount, lab, box, x)
                except Exception:  # noqa: BLE001 – a home the snapshot does not have: as before
                    log.exception("sub-account %s keeps its home after the rollback", x["username"])
            newer_snapshots = [x for x in state["box_snapshots"].values()
                               if x["box"] == box["id"] and x["id"] > snapshot["id"]]
            for newer in newer_snapshots:
                await lab.blocking(delete_snapshot, lab, box, newer)
            state.save()
        finally:
            box.pop("_busy", None)
    lab.run_action(action, steps)
    return action


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
        box = box_or_404(lab, bid, ready=True)
        body = await read_body(request)
        manual = [s for s in state["box_snapshots"].values() if s["box"] == bid and not s["is_automatic"]]
        if len(manual) >= cfg["storage_box_types"][box["type"]]["snapshots"]:
            raise limit_exceeded("snapshot limit of the Storage Box type reached", "snapshots")
        snapshot = new_snapshot(lab, box, False, body.get("description") or "", body.get("labels"))
        action = lab.new_action("create_snapshot", [(bid, "storage_box"), (snapshot["id"], "storage_box_snapshot")])

        async def steps():
            await lab.blocking(copy_snapshot, lab, box, snapshot)
        lab.run_action(action, steps)
        return {"snapshot": snapshot_json(snapshot), "action": action}

    @app.get("/v1/storage_boxes/{bid}/snapshots/{ref}")
    def snapshot_get(bid: int, ref: str):
        return {"snapshot": snapshot_json(snapshot_or_404(lab, box_or_404(lab, bid), ref))}

    @app.put("/v1/storage_boxes/{bid}/snapshots/{ref}")
    async def snapshot_update(bid: int, ref: str, request: Request):
        snapshot = snapshot_or_404(lab, box_or_404(lab, bid), ref)
        update_fields(snapshot, await read_body(request), ("description", "labels"))
        state.save()
        return {"snapshot": snapshot_json(snapshot)}

    @app.delete("/v1/storage_boxes/{bid}/snapshots/{ref}", status_code=201)
    def snapshot_delete(bid: int, ref: str):
        box = box_or_404(lab, bid, ready=True)
        snapshot = snapshot_or_404(lab, box, ref)
        delete_snapshot(lab, box, snapshot)
        state.save()
        return {"action": lab.done("delete_snapshot", [(bid, "storage_box"), (snapshot["id"], "storage_box_snapshot")])}
