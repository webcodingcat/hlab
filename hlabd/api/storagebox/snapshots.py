"""Snapshots of a box: taken by hand or by its snapshot plan, rolled back to, deleted.

As on Hetzner a snapshot is there at once and its action copies the data (the record is
marked `_copying` until then).
"""

import asyncio
import logging
from datetime import datetime, timedelta, timezone

from fastapi import FastAPI, Request

from ...errors import limit_exceeded, locked, not_found
from ...fabric.storagebox import data_dir, snap_dir
from ...lab import Lab, Plan, now
from ...records import BoxSnapshot, StorageBox
from ..web import filtered, read_body, sort_items, update_fields
from .base import box_busy, box_or_404, sync_subaccount

log = logging.getLogger(__name__)


def snapshot_json(lab: Lab, snapshot: BoxSnapshot) -> dict:
    """Return the API object of a snapshot."""
    size = snapshot.get("size", 0)
    return {
        "storage_box": snapshot["box"],
        "id": snapshot["id"],
        "name": snapshot["name"],
        "description": snapshot.get("description") or "",
        "labels": snapshot["labels"],
        "stats": {"size": size, "size_filesystem": size},
        "is_automatic": snapshot["is_automatic"],
        "created": snapshot["created"],
    }


def snapshot_or_404(lab: Lab, box: StorageBox, ref) -> BoxSnapshot:
    """Return a snapshot of the box by ID or name, or raise 404."""
    with lab.state.lock:
        for s in lab.state["box_snapshots"].values():
            if s["box"] == box["id"] and str(ref) in (str(s["id"]), s["name"]):
                return s
    raise not_found("snapshot")


def new_snapshot(
    lab: Lab,
    box: StorageBox,
    automatic: bool,
    description: str = "",
    labels: dict | None = None,
) -> BoxSnapshot:
    """Create the record of a snapshot, named by the time.

    The copy follows, see copy_snapshot.
    """
    state = lab.state
    with state.lock:
        snapshot_id = state.new_id("box_snapshots")
        name = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H-%M-%S")
        while any(
            s["box"] == box["id"] and s["name"] == name
            for s in state["box_snapshots"].values()
        ):
            name += "-1"
        snapshot: BoxSnapshot = {
            "id": snapshot_id,
            "box": box["id"],
            "name": name,
            "description": description,
            "labels": labels or {},
            "is_automatic": automatic,
            "created": now(),
            "size": 0,
            "_copying": True,
        }
        state["box_snapshots"][snapshot_id] = snapshot
        state.save()
    return snapshot


def copy_snapshot(lab: Lab, box: StorageBox, snapshot: BoxSnapshot) -> None:
    """Copy the box's data behind a snapshot record.

    If the copy fails, the record is removed again.
    """
    try:
        lab.fabric.box.take_snapshot(data_dir(box), snap_dir(box, snapshot["name"]))
    except Exception:  # undo the half-done change, then raise
        with lab.state.lock:
            lab.state["box_snapshots"].pop(snapshot["id"], None)
            lab.state.save()
        raise
    # deleted (or a lab reset): no orphan copy
    if lab.state["box_snapshots"].get(snapshot["id"]) is not snapshot:
        lab.fabric.box.remove_tree(snap_dir(box, snapshot["name"]), check=False)
        return
    size = lab.fabric.box.bytes_used(snap_dir(box, snapshot["name"]))
    with lab.state.lock:
        snapshot["size"] = size
        snapshot.pop("_copying", None)
        lab.state.save()


def take_automatic_snapshot(lab: Lab, box: StorageBox) -> BoxSnapshot:
    """Take a snapshot of the box for its plan, which keeps max_snapshots of them."""
    state = lab.state
    snapshot = new_snapshot(lab, box, automatic=True)
    copy_snapshot(lab, box, snapshot)
    with state.lock:
        automatic = sorted(
            (
                s
                for s in state["box_snapshots"].values()
                if s["box"] == box["id"] and s["is_automatic"]
            ),
            key=lambda s: s["id"],
        )
    assert box["plan"] is not None  # the scheduler takes them for a plan only
    for old in automatic[: -max(1, box["plan"]["max_snapshots"])]:
        remove_snapshot(lab, box, old)
    state.save()
    return snapshot


def remove_snapshot(lab: Lab, box: StorageBox, snapshot: BoxSnapshot) -> None:
    """Remove a snapshot's copy and its record."""
    lab.fabric.box.remove_tree(snap_dir(box, snapshot["name"]))
    with lab.state.lock:
        del lab.state["box_snapshots"][snapshot["id"]]


def create_snapshot(lab: Lab, bid: int, body: dict) -> dict:
    """Take a snapshot by hand, within the snapshot limit of the box's type.

    The record is there at once; the action copies the data.
    """
    state = lab.state
    box = box_or_404(lab, bid, ready=True)
    with state.lock:
        manual = [
            s
            for s in state["box_snapshots"].values()
            if s["box"] == bid and not s["is_automatic"]
        ]
    if len(manual) >= lab.cfg["storage_box_types"][box["type"]]["snapshots"]:
        raise limit_exceeded(
            "snapshot limit of the Storage Box type reached", "snapshots"
        )
    snapshot = new_snapshot(
        lab, box, False, body.get("description") or "", body.get("labels")
    )
    action = lab.new_action(
        "create_snapshot",
        [(bid, "storage_box"), (snapshot["id"], "storage_box_snapshot")],
    )

    async def steps():
        await lab.blocking(copy_snapshot, lab, box, snapshot)

    lab.run_action(action, steps)
    return {"snapshot": snapshot_json(lab, snapshot), "action": action}


def update_snapshot(lab: Lab, bid: int, ref: str, body: dict) -> dict:
    """Change a snapshot's description and labels."""
    snapshot = snapshot_or_404(lab, box_or_404(lab, bid), ref)
    with lab.state.lock:
        update_fields(snapshot, body, ("description", "labels"))
        lab.state.save()
    return {"snapshot": snapshot_json(lab, snapshot)}


def delete_snapshot(lab: Lab, bid: int, ref: str) -> dict:
    """Delete a snapshot at once (not while the box is busy)."""
    box = box_or_404(lab, bid, ready=True)
    snapshot = snapshot_or_404(lab, box, ref)
    remove_snapshot(lab, box, snapshot)
    lab.state.save()
    return {
        "action": lab.finished_action(
            "delete_snapshot",
            [(bid, "storage_box"), (snapshot["id"], "storage_box_snapshot")],
        )
    }


def rollback_snapshot(lab: Lab, box: StorageBox, body: dict) -> Plan:
    """Roll the box back to a snapshot (the box action rollback_snapshot).

    The data goes back to the snapshot and newer snapshots are deleted (as on Hetzner).
    The box is busy meanwhile (`_busy`): no snapshot is taken, deleted or pruned.
    """
    state = lab.state
    snapshot = snapshot_or_404(lab, box, body.get("snapshot"))
    if snapshot.get("_copying"):
        raise locked("the snapshot is still being taken")

    async def steps():
        await lab.blocking(
            lab.fabric.box.roll_back, snap_dir(box, snapshot["name"]), data_dir(box)
        )
        # sub-account homes are paths: they follow what the snapshot has there
        with state.lock:
            subaccounts = [
                x for x in state["subaccounts"].values() if x["box"] == box["id"]
            ]
        for subaccount in subaccounts:
            try:
                await lab.blocking(sync_subaccount, lab, box, subaccount)
            # a home the snapshot does not have stays as it was
            except Exception:
                log.exception(
                    "sub-account %s keeps its home after the rollback",
                    subaccount["username"],
                )
        with state.lock:
            newer_snapshots = [
                x
                for x in state["box_snapshots"].values()
                if x["box"] == box["id"] and x["id"] > snapshot["id"]
            ]
        for newer in newer_snapshots:
            await lab.blocking(remove_snapshot, lab, box, newer)
        state.save()

    return Plan(steps, resources=[(snapshot["id"], "storage_box_snapshot")])


# --- snapshot plans -------------------------------------------------------------------


def snapshot_plan_due(plan: dict, moment: datetime) -> bool:
    """Check whether a snapshot plan is due in the given minute."""
    return (
        moment.minute == plan["minute"]
        and moment.hour == plan["hour"]
        and (
            plan.get("day_of_week") is None
            or moment.isoweekday() % 7 == plan["day_of_week"] % 7
        )
        and (plan.get("day_of_month") is None or moment.day == plan["day_of_month"])
    )


async def snapshot_plan_scheduler(lab: Lab) -> None:
    """Take the automatic snapshots of all boxes with a snapshot plan.

    The plans are checked once a minute; minutes that went by while snapshots ran (an
    hour at most) are checked too, so a slow snapshot of one box does not skip another's
    plan.
    """
    last_minute = datetime.now(timezone.utc).replace(second=0, microsecond=0)
    while True:
        await asyncio.sleep(60 - datetime.now(timezone.utc).second)
        this_minute = datetime.now(timezone.utc).replace(second=0, microsecond=0)
        missed = min(60, int((this_minute - last_minute).total_seconds() // 60))
        minutes = [this_minute - timedelta(minutes=i) for i in range(missed)]
        last_minute = this_minute
        with lab.state.lock:
            boxes = list(lab.state["storage_boxes"].values())
        for box in boxes:
            # a rollback or delete runs: the plan waits for them
            if box_busy(lab, box):
                continue
            plan = box.get("plan")
            if plan and any(snapshot_plan_due(plan, m) for m in minutes):
                try:
                    await lab.blocking(take_automatic_snapshot, lab, box)
                # one failed snapshot must not end the plans of all boxes
                except Exception:
                    log.exception("automatic snapshot of box %s failed", box["id"])


# --- routes ---------------------------------------------------------------------------


def add_routes(app: FastAPI, lab: Lab) -> None:
    """Add the routes of a box's snapshots to the app."""
    state = lab.state

    @app.get("/v1/storage_boxes/{bid}/snapshots")
    def snapshot_list(bid: int, request: Request):
        with state.lock:
            box_or_404(lab, bid)
            items = filtered(
                request,
                [s for s in state["box_snapshots"].values() if s["box"] == bid],
                {
                    "is_automatic": lambda snapshot, value: (
                        str(snapshot["is_automatic"]).lower() == value.lower()
                    )
                },
            )
            return {
                "snapshots": sort_items(
                    [snapshot_json(lab, s) for s in items],
                    request.query_params.getlist("sort"),
                )
            }

    @app.post("/v1/storage_boxes/{bid}/snapshots", status_code=201)
    async def snapshot_create(bid: int, request: Request):
        body = await read_body(request)
        return await lab.blocking(create_snapshot, lab, bid, body)

    @app.get("/v1/storage_boxes/{bid}/snapshots/{ref}")
    def snapshot_get(bid: int, ref: str):
        snapshot = snapshot_or_404(lab, box_or_404(lab, bid), ref)
        return {"snapshot": snapshot_json(lab, snapshot)}

    @app.put("/v1/storage_boxes/{bid}/snapshots/{ref}")
    async def snapshot_update(bid: int, ref: str, request: Request):
        body = await read_body(request)
        return await lab.blocking(update_snapshot, lab, bid, ref, body)

    @app.delete("/v1/storage_boxes/{bid}/snapshots/{ref}", status_code=201)
    def snapshot_delete(bid: int, ref: str):
        return delete_snapshot(lab, bid, ref)
